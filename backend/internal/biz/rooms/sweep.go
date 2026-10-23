package rooms

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// sweepBatch is how many rooms of each kind one pass ends or finishes. The
// next pass takes the rest.
const sweepBatch = 100

// Swept is what one pass of the sweeper did.
type Swept struct {
	// Idle is the lobbies it ended; Timeout the rounds it finished for
	// running past their limit; Deleted the ended rooms it removed for good.
	Idle    int
	Timeout int
	Deleted int64
}

// Any says whether there is anything to log.
func (s Swept) Any() bool { return s.Idle > 0 || s.Timeout > 0 || s.Deleted > 0 }

// Sweep is the clock's pass: a lobby nobody is connected to and nobody has
// touched for LobbyIdle ends (idle); a round running past MaxPlay is finished
// (timeout) and its room goes back to its lobby, which then idles like any
// other; a room that ended more than Retention ago is deleted with its
// members. Every end publishes ended, so the sockets close; a finish closes
// none. Safe to run on every replica at once: each change re-checks its room
// under the row lock.
func (s *Service) Sweep(ctx context.Context, now time.Time) (Swept, error) {
	var out Swept
	var errs []error

	if s.cfg.LobbyIdle > 0 {
		cutoff := now.Add(-s.cfg.LobbyIdle)
		lobbies, err := idleLobbies(ctx, s.db, cutoff, sweepBatch)
		if err != nil {
			return out, err
		}
		for i := range lobbies {
			r := &lobbies[i]
			if s.anyConnected(ctx, r) {
				continue
			}
			ended, err := s.sweepEnd(ctx, r.ID, enum.EndIdle, func(r *model.Room) bool {
				return r.Status == enum.RoomLobby && r.ActiveAt.Before(cutoff)
			})
			if err != nil {
				errs = append(errs, err)
			} else if ended {
				out.Idle++
			}
		}
	}

	if s.cfg.MaxPlay > 0 {
		cutoff := now.Add(-s.cfg.MaxPlay)
		games, err := overlong(ctx, s.db, cutoff, sweepBatch)
		if err != nil {
			return out, err
		}
		for i := range games {
			finished, err := s.sweepFinish(ctx, games[i].ID, cutoff)
			if err != nil {
				errs = append(errs, err)
			} else if finished {
				out.Timeout++
			}
		}
	}

	deleted, err := deleteEnded(ctx, s.db, now.Add(-s.cfg.Retention))
	if err != nil {
		errs = append(errs, err)
	}
	out.Deleted = deleted
	return out, stderrors.Join(errs...)
}

// anyConnected says whether a member has a socket open to the room. Presence
// that cannot be read keeps the room: ending one that is in use is the worse
// mistake.
func (s *Service) anyConnected(ctx context.Context, r *model.Room) bool {
	if s.Presence == nil {
		return false
	}
	rows, err := currentMembers(ctx, s.db, r.ID)
	if err != nil || len(rows) == 0 {
		return false
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.PlayerID
	}
	states, err := s.Presence.Of(ctx, ids)
	if err != nil {
		applog.From(ctx).Warn("presence not read; the lobby is kept", "room", r.ID, "err", err)
		return true
	}
	for _, st := range states {
		if st.State == players.InRoom && st.Room == r.ID {
			return true
		}
	}
	return false
}

// sweepEnd ends one room for the clock, if it still qualifies once its row
// is held: a join or a start may have landed since it was listed. A lobby's
// end carries the latest round's results.
func (s *Service) sweepEnd(ctx context.Context, roomID string, reason enum.EndReason, still func(*model.Room) bool) (bool, error) {
	var (
		now     time.Time
		round   int
		results []model.Score
	)
	ended := false
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := lockRoom(ctx, s.db, roomID)
		if err != nil || r == nil || !still(r) {
			return err
		}
		if results, err = s.closing(ctx, r); err != nil {
			return err
		}
		now, round = data.Now(), r.Round
		if err := updateRoom(ctx, s.db, r.ID, s.endFields(ctx, r, now, reason, model.Scores(results))); err != nil {
			return err
		}
		ended = true
		return event(ctx, s.db, r, "", enum.EventEnded, map[string]any{"reason": reason, "round": r.Round})
	})
	if err != nil || !ended {
		return false, err
	}
	s.end(ctx, roomID, Ended{T: FrameEnded, At: stamp(now), Reason: string(reason), Round: round, Results: results})
	return true, nil
}

// sweepFinish finishes a round that started before cutoff and is still under
// way once its room's row is held: a finish or an end may have landed since
// it was listed. The game's server may have written the round's results:
// the finish carries them. The room goes back to its lobby, in use as of now,
// and nobody's socket closes.
func (s *Service) sweepFinish(ctx context.Context, roomID string, cutoff time.Time) (bool, error) {
	var (
		now     time.Time
		round   int
		results []model.Score
	)
	var finished, decided bool
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := lockRoom(ctx, s.db, roomID)
		if err != nil || r == nil || r.Status != enum.RoomPlaying || r.StartedAt == nil || !r.StartedAt.Before(cutoff) {
			return err
		}
		if results, _, err = roundResults(ctx, s.db, r, nil); err != nil {
			return err
		}
		// A match room's round is counted like any other; the one that
		// decides its match closes the room (docs/TOURNAMENTS.md).
		if decided, err = s.counted(ctx, r, results); err != nil {
			return err
		}
		now, round = data.Now(), r.Round
		if decided {
			err = s.decidedEnd(ctx, r, "", results, now)
		} else {
			err = s.finishRound(ctx, r, "", enum.EndTimeout, results, now)
		}
		if err != nil {
			return err
		}
		finished = true
		return nil
	})
	if err != nil || !finished {
		return false, err
	}
	if decided {
		s.end(ctx, roomID, Ended{T: FrameEnded, At: stamp(now), Reason: string(enum.EndTournament), Round: round, Results: results})
		return true, nil
	}
	s.publish(ctx, roomID, Finished{
		T: FrameFinished, At: stamp(now), Round: round, Reason: string(enum.EndTimeout), Results: results,
	})
	return true, nil
}
