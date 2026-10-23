package tournaments

// Matches (docs/TOURNAMENTS.md, "Playing a match" and "Deciding"): settling
// a knockout match once its sides are known, deciding it and sending its
// winner on, a withdrawal, the rooms' questions about a match room (who
// comes in, what a round decides, what the Room shows), playing a match and
// the organizer's award.

import (
	"context"

	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// sideKind is what a knockout match's side holds: a decision still to come,
// nobody ever (a bye, or a match nobody won), a player still in it, or a
// player who has withdrawn.
type sideKind int

const (
	sidePending sideKind = iota
	sideVoid
	sidePlayer
	sideGone
)

// side reads one side of a match: the second when b is set.
func (st *state) side(m *model.TournamentMatch, b bool) (string, sideKind) {
	player, void := m.PlayerA, m.VoidA
	if b {
		player, void = m.PlayerB, m.VoidB
	}
	switch {
	case void:
		return "", sideVoid
	case player == nil:
		return "", sidePending
	case st.isCurrent(*player):
		return *player, sidePlayer
	}
	return *player, sideGone
}

// settle is a knockout match whose sides are both known: two players still
// in make it ready; one is through on a bye (the other side void) or a
// walkover (the other withdrew); none decides it for nobody, which leaves the
// next match's side void. A side still to come keeps it waiting.
func (st *state) settle(m *model.TournamentMatch) {
	if m.Status == enum.MatchDone {
		return
	}
	a, ka := st.side(m, false)
	b, kb := st.side(m, true)
	if ka == sidePending || kb == sidePending {
		return
	}
	switch {
	case ka == sidePlayer && kb == sidePlayer:
		if m.Status != enum.MatchReady {
			m.Status, m.ReadyAt = enum.MatchReady, &st.now
			st.touchM(m)
			st.ready = append(st.ready, m)
		}
	case ka == sidePlayer:
		st.decide(m, &a, through(kb))
	case kb == sidePlayer:
		st.decide(m, &b, through(ka))
	default:
		st.decide(m, nil, enum.DecidedByWalkover)
	}
}

// through is how a player goes through facing a side with nobody on it: a
// bye when nobody ever was, a walkover when the opponent withdrew.
func through(other sideKind) enum.MatchDecision {
	if other == sideVoid {
		return enum.DecidedByBye
	}
	return enum.DecidedByWalkover
}

// decide is a knockout match decided for winner (nil for nobody): the loser
// is out at this stage's place, the winner (or a void) goes on to the next
// stage's match, which is settled in turn, and the final decided ends the
// tournament. Its room, if it has one, closes once the change commits.
func (st *state) decide(m *model.TournamentMatch, winner *string, by enum.MatchDecision) {
	if winner != nil {
		w := *winner
		winner = &w
	}
	m.Status, m.WinnerID, m.DecidedBy, m.DecidedAt = enum.MatchDone, winner, &by, &st.now
	st.touchM(m)
	st.closing = append(st.closing, m.ID)

	out := 1<<(st.t.Stages-m.Stage) + 1
	for _, p := range []*string{m.PlayerA, m.PlayerB} {
		if p != nil && (winner == nil || *p != *winner) {
			st.place(*p, out)
		}
	}
	if m.Stage >= st.t.Stages {
		st.finish(winner, enum.TournamentPlayed)
		return
	}
	next := st.at[[2]int{m.Stage + 1, m.Slot / 2}]
	if next == nil {
		return
	}
	if m.Slot%2 == 0 {
		next.PlayerA, next.VoidA = winner, winner == nil
	} else {
		next.PlayerB, next.VoidB = winner, winner == nil
	}
	st.touchM(next)
	st.settle(next)
}

// place is an entrant's final place, the first one given: a knockout's loser
// keeps the place of the stage they went out at.
func (st *state) place(playerID string, place int) {
	e := st.byPlayer[playerID]
	if e == nil || e.Place != nil {
		return
	}
	e.Place = &place
	st.touchE(e)
}

// finish ends the tournament as done, with its champion (placed first) and
// why.
func (st *state) finish(champion *string, why enum.TournamentEnd) {
	t := st.t
	t.Status, t.ChampionID, t.EndReason, t.EndedAt = enum.TournamentDone, champion, &why, &st.now
	st.touchT()
	st.ended = true
	if champion != nil {
		if e := st.byPlayer[*champion]; e != nil {
			first := 1
			e.Place = &first
			st.touchE(e)
		}
	}
}

// withdraw takes an entrant out of a running tournament, in the caller's
// transaction: a knockout's undecided matches of theirs are settled again
// (the opponent through on a walkover, at once or when they arrive); a
// league with fewer than two left is over, its table as it stands.
func (s *Service) withdraw(ctx context.Context, st *state, e *model.TournamentEntrant, removed bool) error {
	e.LeftAt = &st.now
	if removed {
		e.Removed = true
	}
	st.touchE(e)
	if st.t.Format == enum.FormatKnockout {
		for _, m := range st.matches {
			if m.Status != enum.MatchDone && m.Has(e.PlayerID) {
				st.settle(m)
			}
		}
		return nil
	}
	if len(st.current()) >= 2 {
		return nil
	}
	plays, err := st.leaguePlays(ctx, s)
	if err != nil {
		return err
	}
	st.finishLeague(enum.TournamentWithdrawals, plays)
	return nil
}

// leaguePlays is every counted round of the league's match.
func (st *state) leaguePlays(ctx context.Context, s *Service) ([]model.TournamentPlay, error) {
	m := st.league()
	if m == nil {
		return nil, nil
	}
	return playsOf(ctx, s.db, []string{m.ID})
}

// finishLeague ends a league: everybody placed by the table, the first its
// champion, its match decided by the play when its rounds ran out, else by
// the table as it stood.
func (st *state) finishLeague(why enum.TournamentEnd, plays []model.TournamentPlay) {
	table := st.table(plays)
	for i, row := range table {
		e := st.byPlayer[row.player]
		place := i + 1
		e.Place = &place
		st.touchE(e)
	}
	var champion *string
	if len(table) > 0 {
		id := table[0].player
		champion = &id
	}
	if m := st.league(); m != nil && m.Status != enum.MatchDone {
		by := enum.DecidedByStandings
		if why == enum.TournamentPlayed {
			by = enum.DecidedByPlay
		}
		m.Status, m.WinnerID, m.DecidedBy, m.DecidedAt = enum.MatchDone, champion, &by, &st.now
		st.touchM(m)
		st.closing = append(st.closing, m.ID)
	}
	st.finish(champion, why)
}

// keep is a round's results kept to the match's players: a knockout's two
// sides, a league's current entrants.
func (st *state) keep(m *model.TournamentMatch, results []model.Score) []model.Score {
	out := make([]model.Score, 0, len(results))
	seen := map[string]bool{}
	for _, r := range results {
		if seen[r.Player] {
			continue
		}
		in := m.Has(r.Player)
		if st.t.Format == enum.FormatLeague {
			in = st.isCurrent(r.Player)
		}
		if in {
			seen[r.Player] = true
			out = append(out, r)
		}
	}
	return out
}

// roundWinner is a round's one winner: in a knockout the side ranked
// strictly better than the other (a side not ranked is behind one that is);
// in a league the one first place, if only one player has it. Nil is a draw.
func (st *state) roundWinner(m *model.TournamentMatch, kept []model.Score) *string {
	rank := map[string]int{}
	for _, r := range kept {
		rank[r.Player] = r.Rank
	}
	if st.t.Format == enum.FormatKnockout && m.PlayerA != nil && m.PlayerB != nil {
		a, oka := rank[*m.PlayerA]
		b, okb := rank[*m.PlayerB]
		switch {
		case oka && (!okb || a < b):
			return m.PlayerA
		case okb && (!oka || b < a):
			return m.PlayerB
		}
		return nil
	}
	var first *string
	for i := range kept {
		if kept[i].Rank != 1 {
			continue
		}
		if first != nil {
			return nil
		}
		first = &kept[i].Player
	}
	return first
}

// Count is the rooms telling the match that a round of its room closed with
// these results (rooms.Matches), inside the room's own transaction with its
// row held: the tournament's row is held after it, as everywhere. A round
// with none of the match's players ranked counts nothing; a round counted
// once is not counted again. It answers whether the match is decided — by
// this round, or before it — or its tournament no longer running: the room
// then ends with the reason tournament.
func (s *Service) Count(ctx context.Context, r *model.Room, round int, results []model.Score) (bool, error) {
	if r.MatchID == nil {
		return false, nil
	}
	m, err := matchByID(ctx, s.db, *r.MatchID)
	if err != nil || m == nil {
		return false, err
	}
	st, err := load(ctx, s.db, m.TournamentID)
	if err != nil || st == nil {
		return false, err
	}
	m = st.match(m.ID)
	switch {
	case m == nil:
		return false, nil
	case st.t.Status != enum.TournamentRunning || m.Status == enum.MatchDone:
		return true, nil
	case m.Status != enum.MatchReady:
		return false, nil
	}
	kept := st.keep(m, results)
	if len(kept) == 0 {
		return false, nil
	}
	play := model.TournamentPlay{
		MatchID: m.ID, RoomID: r.ID, Round: round, Results: model.Scores(kept),
		WinnerID: st.roundWinner(m, kept), PlayedAt: st.now,
	}
	res := data.Conn(ctx, s.db).Exec(`
		INSERT INTO tournament_plays (match_id, room_id, round, results, winner_id, played_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		play.MatchID, play.RoomID, play.Round, play.Results, play.WinnerID, play.PlayedAt)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}

	plays, err := playsOf(ctx, s.db, []string{m.ID})
	if err != nil {
		return false, err
	}
	switch st.t.Format {
	case enum.FormatKnockout:
		wins := winsOf(plays)
		for _, side := range []*string{m.PlayerA, m.PlayerB} {
			if side != nil && wins[*side] >= st.t.Need() {
				st.decide(m, side, enum.DecidedByPlay)
				break
			}
		}
	case enum.FormatLeague:
		if len(plays) >= st.t.Rounds {
			st.finishLeague(enum.TournamentPlayed, plays)
		}
	}
	if err := s.commit(ctx, st); err != nil {
		return false, err
	}
	return m.Status == enum.MatchDone || st.t.Status != enum.TournamentRunning, nil
}

// winsOf is each player's round wins in these plays.
func winsOf(plays []model.TournamentPlay) map[string]int {
	out := map[string]int{}
	for _, p := range plays {
		if p.WinnerID != nil {
			out[*p.WinnerID]++
		}
	}
	return out
}

// Admits is the match room's door (rooms.Matches): while the tournament runs
// and the match is ready, one of a knockout match's two sides still in it, or
// a league's current entrant.
func (s *Service) Admits(ctx context.Context, matchID, playerID string) (bool, error) {
	m, err := matchByID(ctx, s.db, matchID)
	if err != nil || m == nil || m.Status != enum.MatchReady {
		return false, err
	}
	t, err := tournamentByID(ctx, s.db, m.TournamentID, false)
	if err != nil || t == nil || t.Status != enum.TournamentRunning {
		return false, err
	}
	e, err := entrantRow(ctx, s.db, t.ID, playerID)
	if err != nil || e == nil || !e.Current() {
		return false, err
	}
	return t.Format == enum.FormatLeague || m.Has(playerID), nil
}

// Brief is the match as a room carries it (rooms.Matches): the tournament,
// the stage, the length, and the tally as it stands.
func (s *Service) Brief(ctx context.Context, matchID string) (*rooms.MatchBrief, error) {
	m, err := matchByID(ctx, s.db, matchID)
	if err != nil || m == nil {
		return nil, err
	}
	t, err := tournamentByID(ctx, s.db, m.TournamentID, false)
	if err != nil || t == nil {
		return nil, err
	}
	plays, err := playsOf(ctx, s.db, []string{m.ID})
	if err != nil {
		return nil, err
	}
	out := &rooms.MatchBrief{
		ID: t.ID, Name: t.Name, Format: string(t.Format), Match: m.ID, Stage: m.Stage, Stages: t.Stages,
		BestOf: t.BestOf, Need: t.Need(), Rounds: t.Rounds, Played: len(plays), Tally: []rooms.MatchTally{},
	}
	if t.Format == enum.FormatKnockout {
		wins := winsOf(plays)
		for _, p := range []*string{m.PlayerA, m.PlayerB} {
			if p != nil {
				out.Tally = append(out.Tally, rooms.MatchTally{Player: *p, Wins: wins[*p]})
			}
		}
		return out, nil
	}
	st, err := loadRest(ctx, s.db, t)
	if err != nil {
		return nil, err
	}
	for _, row := range st.table(plays) {
		out.Tally = append(out.Tally, rooms.MatchTally{Player: row.player, Wins: row.wins, Points: row.points})
	}
	return out, nil
}

// Play is the player sitting down to their match: its open room, joined, or
// a new one with them as its host, shaped by the match (a knockout's two, a
// league's table). Every refusal of the tournament's first, then the rooms'.
func (s *Service) Play(ctx context.Context, playerID, tournamentID, matchID string) (*rooms.Room, error) {
	t, err := tournamentByID(ctx, s.db, tournamentID, false)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if t == nil {
		return nil, errNotFound
	}
	st, err := loadRest(ctx, s.db, t)
	if err != nil {
		return nil, errors.Internal(err)
	}
	readable, err := s.readable(ctx, t, st.byPlayer[playerID], playerID)
	if err != nil {
		return nil, asError(err)
	}
	m := st.match(matchID)
	if !readable || m == nil {
		return nil, errNotFound
	}
	switch t.Status {
	case enum.TournamentOpen:
		return nil, errNotRunning
	case enum.TournamentDone, enum.TournamentCancelled:
		return nil, errClosed
	}
	if !st.isCurrent(playerID) || (t.Format == enum.FormatKnockout && !m.Has(playerID)) {
		return nil, errNotInMatch
	}
	switch m.Status {
	case enum.MatchWaiting:
		return nil, errMatchWaiting
	case enum.MatchDone:
		return nil, errMatchDecided
	}
	if s.Rooms == nil {
		return nil, errNotRunning
	}
	return s.Rooms.Seat(ctx, playerID, rooms.MatchSeat{
		MatchID: m.ID, GameID: t.GameID, Config: matchTable(t, len(st.current())),
	})
}

// matchTable is the room block a match's room is made with: the game's, with
// the table the match sets. A knockout needs both its players and holds no
// one else; a league needs two (or the game's own minimum) and holds every
// entrant still in it.
func matchTable(t *model.Tournament, current int) model.RoomBlock {
	b := t.Config
	if t.Format == enum.FormatKnockout {
		b.Players.Min, b.Players.Max = 2, 2
		return b
	}
	b.Players.Min = max(2, b.Players.Min)
	b.Players.Max = max(current, b.Players.Min)
	return b
}

// Award is the organizer's (or an admin's) way to decide a ready knockout
// match for one of its two players: a pair that never plays does not hold
// the bracket up. The match's room closes once it is decided.
func (s *Service) Award(ctx context.Context, by Actor, tournamentID, matchID, playerID string) (*Tournament, error) {
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
		if st.t.Format != enum.FormatKnockout {
			return errNotKnockout
		}
		m := st.match(matchID)
		if m == nil {
			return errNotFound
		}
		switch m.Status {
		case enum.MatchWaiting:
			return errMatchWaiting
		case enum.MatchDone:
			return errMatchDecided
		}
		if !m.Has(playerID) {
			return errors.Invalid(map[string]string{"player": "not_in_match"})
		}
		st.decide(m, &playerID, enum.DecidedByAward)
		return s.commit(ctx, st)
	})
	if err != nil {
		return nil, asError(err)
	}
	if by.Admin != "" {
		s.record(ctx, by.Admin, model.ActionMatchAwarded, matchID, map[string]any{
			"tournament": tournamentID, "player": playerID,
		})
	}
	return s.Get(ctx, by, tournamentID)
}
