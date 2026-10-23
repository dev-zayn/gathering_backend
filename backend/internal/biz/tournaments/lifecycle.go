package tournaments

// A tournament's life after it is made (docs/TOURNAMENTS.md, "Starting: the
// draw", "Deciding", "The clock"): the draw that starts it, a league
// finished early, the cancellation, and the clock's pass.

import (
	"context"
	"time"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// minEntrants is how many a tournament needs to start: two, and for a league
// the game's own minimum if it is more.
func minEntrants(t *model.Tournament) int {
	if t.Format == enum.FormatLeague {
		return max(2, t.Config.Players.Min)
	}
	return 2
}

// loadAsOrganizer is the check every organizer-only change opens with, in the
// order the refusals are named: a tournament the caller may read, then the
// organizer (an admin is one for every tournament). The row is held.
func (s *Service) loadAsOrganizer(ctx context.Context, by Actor, tournamentID string) (*state, error) {
	st, err := load(ctx, s.db, tournamentID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, errNotFound
	}
	if by.Admin != "" {
		return st, nil
	}
	readable, err := s.readable(ctx, st.t, st.byPlayer[by.Player], by.Player)
	if err != nil {
		return nil, err
	}
	if !readable {
		return nil, errNotFound
	}
	if !st.t.Organizer(by.Player) {
		return nil, errOrganizerOnly
	}
	return st, nil
}

// Start is the organizer's (an official one's: an admin's), from open only,
// with enough entrants: the draw, the bracket or the league's match, and the
// notifications.
func (s *Service) Start(ctx context.Context, by Actor, tournamentID string) (*Tournament, error) {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		st, err := s.loadAsOrganizer(ctx, by, tournamentID)
		if err != nil {
			return err
		}
		switch st.t.Status {
		case enum.TournamentRunning:
			return errStarted
		case enum.TournamentDone, enum.TournamentCancelled:
			return errClosed
		}
		if err := s.begin(ctx, st); err != nil {
			return err
		}
		return s.commit(ctx, st)
	})
	if err != nil {
		return nil, asError(err)
	}
	if by.Admin != "" {
		s.record(ctx, by.Admin, model.ActionTournamentStarted, tournamentID, nil)
	}
	return s.Get(ctx, by, tournamentID)
}

// begin is the draw, in the caller's transaction, on a tournament held open:
// the current entrants seeded afresh at random; a knockout's whole bracket
// written, its first stage settled (the byes through at once, the pairs
// ready); a league's one match, ready. Those removed while it was open go:
// nobody joins a running tournament, so their rows have nothing left to
// keep out.
func (s *Service) begin(ctx context.Context, st *state) error {
	t := st.t
	current := st.current()
	if len(current) < minEntrants(t) {
		return errTooFew
	}
	err := data.Conn(ctx, s.db).Where("tournament_id = ? AND removed", t.ID).Delete(&model.TournamentEntrant{}).Error
	if err != nil {
		return err
	}
	st.entrants = current
	st.byPlayer = make(map[string]*model.TournamentEntrant, len(current))
	for _, e := range current {
		st.byPlayer[e.PlayerID] = e
	}

	shuffle(current)
	for i, e := range current {
		e.Seed = i + 1
		st.touchE(e)
	}
	st.sortEntrants()

	switch t.Format {
	case enum.FormatKnockout:
		slots, stages := bracketSize(len(current))
		t.Stages = stages
		order := bracketOrder(slots)
		for stage := 1; stage <= stages; stage++ {
			for slot := 0; slot < slots>>stage; slot++ {
				m := &model.TournamentMatch{
					ID: ids.New(ids.Match), TournamentID: t.ID, Stage: stage, Slot: slot, Status: enum.MatchWaiting,
				}
				if stage == 1 {
					m.PlayerA, m.VoidA = seeded(current, order[2*slot])
					m.PlayerB, m.VoidB = seeded(current, order[2*slot+1])
				}
				st.addMatch(m)
			}
		}
		for slot := 0; slot < slots>>1; slot++ {
			st.settle(st.at[[2]int{1, slot}])
		}
	case enum.FormatLeague:
		t.Stages = 1
		m := &model.TournamentMatch{
			ID: ids.New(ids.Match), TournamentID: t.ID, Stage: 1, Slot: 0,
			Status: enum.MatchReady, ReadyAt: &st.now,
		}
		st.addMatch(m)
		st.ready = append(st.ready, m)
	}

	t.Status, t.StartedAt = enum.TournamentRunning, &st.now
	st.touchT()
	st.began = true
	return nil
}

// seeded is the player the draw gave this seed, or a void side: a bye.
func seeded(entrants []*model.TournamentEntrant, seed int) (*string, bool) {
	if seed > len(entrants) {
		return nil, true
	}
	id := entrants[seed-1].PlayerID
	return &id, false
}

// Finish is a league ended early by its organizer (or an admin): the table
// as it stands decides, so long as a round was counted. A knockout is played
// out, or awarded match by match.
func (s *Service) Finish(ctx context.Context, by Actor, tournamentID string) (*Tournament, error) {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		st, err := s.loadAsOrganizer(ctx, by, tournamentID)
		if err != nil {
			return err
		}
		switch st.t.Status {
		case enum.TournamentOpen:
			return errNotRunning
		case enum.TournamentDone, enum.TournamentCancelled:
			return errClosed
		}
		if st.t.Format != enum.FormatLeague {
			return errNotLeague
		}
		plays, err := st.leaguePlays(ctx, s)
		if err != nil {
			return err
		}
		if len(plays) == 0 {
			return errNothingPlayed
		}
		st.finishLeague(enum.TournamentEarly, plays)
		return s.commit(ctx, st)
	})
	if err != nil {
		return nil, asError(err)
	}
	if by.Admin != "" {
		s.record(ctx, by.Admin, model.ActionTournamentFinished, tournamentID, nil)
	}
	return s.Get(ctx, by, tournamentID)
}

// Cancel calls a tournament off for good, open or running: no champion, no
// places, and every match room it has closes.
func (s *Service) Cancel(ctx context.Context, by Actor, tournamentID string) (*Tournament, error) {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		st, err := s.loadAsOrganizer(ctx, by, tournamentID)
		if err != nil {
			return err
		}
		if !st.t.Status.Live() {
			return errClosed
		}
		why := enum.TournamentByOrganizer
		if by.Admin != "" {
			why = enum.TournamentByAdmin
		}
		st.cancel(why)
		return s.commit(ctx, st)
	})
	if err != nil {
		return nil, asError(err)
	}
	if by.Admin != "" {
		s.record(ctx, by.Admin, model.ActionTournamentCancelled, tournamentID, nil)
	}
	return s.Get(ctx, by, tournamentID)
}

// cancel ends the tournament as cancelled, and has every undecided match's
// room closed.
func (st *state) cancel(why enum.TournamentEnd) {
	t := st.t
	t.Status, t.EndReason, t.EndedAt = enum.TournamentCancelled, &why, &st.now
	st.touchT()
	st.ended = true
	for _, m := range st.matches {
		if m.Status != enum.MatchDone {
			st.closing = append(st.closing, m.ID)
		}
	}
}

// sweepBatch is how many tournaments of each kind one pass starts or calls
// off. The next pass takes the rest.
const sweepBatch = 100

// Swept is what one pass of the clock did.
type Swept struct {
	Started   int
	Cancelled int
}

// Any says whether there is anything to log.
func (s Swept) Any() bool { return s.Started > 0 || s.Cancelled > 0 }

// Sweep is the clock's pass: an open tournament whose start time has come is
// started, or called off short of entrants (too_few); one with no start time
// left open longer than OpenFor is called off (expired). Each re-checks its
// tournament under its row lock, so every replica may sweep at once.
func (s *Service) Sweep(ctx context.Context, now time.Time) (Swept, error) {
	var out Swept
	var due []string
	err := data.Conn(ctx, s.db).Raw(`
		SELECT id FROM tournaments WHERE status = ? AND starts_at IS NOT NULL AND starts_at <= ?
		ORDER BY starts_at LIMIT ?`, enum.TournamentOpen, now, sweepBatch).Scan(&due).Error
	if err != nil {
		return out, err
	}
	for _, id := range due {
		started, err := s.sweepOne(ctx, id, func(t *model.Tournament) bool {
			return t.Status == enum.TournamentOpen && t.StartsAt != nil && !t.StartsAt.After(now)
		}, enum.TournamentTooFew, true)
		if err != nil {
			applog.From(ctx).Warn("tournament not started on time", "tournament", id, "err", err)
			continue
		}
		switch started {
		case swStarted:
			out.Started++
		case swCancelled:
			out.Cancelled++
		}
	}

	var stale []string
	err = data.Conn(ctx, s.db).Raw(`
		SELECT id FROM tournaments WHERE status = ? AND starts_at IS NULL AND created_at < ?
		ORDER BY created_at LIMIT ?`, enum.TournamentOpen, now.Add(-s.cfg.OpenFor), sweepBatch).Scan(&stale).Error
	if err != nil {
		return out, err
	}
	cutoff := now.Add(-s.cfg.OpenFor)
	for _, id := range stale {
		done, err := s.sweepOne(ctx, id, func(t *model.Tournament) bool {
			return t.Status == enum.TournamentOpen && t.StartsAt == nil && t.CreatedAt.Before(cutoff)
		}, enum.TournamentExpired, false)
		if err != nil {
			applog.From(ctx).Warn("tournament not called off", "tournament", id, "err", err)
			continue
		}
		if done == swCancelled {
			out.Cancelled++
		}
	}
	return out, nil
}

// What one sweep of a tournament did.
type swept int

const (
	swNothing swept = iota
	swStarted
	swCancelled
)

// sweepOne starts (when start is set and there are enough entrants) or calls
// off one tournament for the clock, if it still qualifies once its row is
// held.
func (s *Service) sweepOne(ctx context.Context, id string, still func(*model.Tournament) bool,
	why enum.TournamentEnd, start bool) (swept, error) {
	did := swNothing
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		st, err := load(ctx, s.db, id)
		if err != nil || st == nil || !still(st.t) {
			return err
		}
		if start && len(st.current()) >= minEntrants(st.t) {
			if err := s.begin(ctx, st); err != nil {
				return err
			}
			did = swStarted
		} else {
			st.cancel(why)
			did = swCancelled
		}
		return s.commit(ctx, st)
	})
	if err != nil {
		return swNothing, err
	}
	return did, nil
}
