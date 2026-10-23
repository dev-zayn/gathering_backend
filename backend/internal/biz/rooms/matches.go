package rooms

// Match rooms (docs/TOURNAMENTS.md, "Match rooms"): a room made to play a
// tournament's match. Only the match's players come in; nobody is kicked or
// invited; every round that closes with results is counted for the match,
// and the round that decides it closes the room with the reason tournament.
// The tournament's rules are the tournaments package's: the rooms ask them
// through Matches, and hold none of their own.

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// The match rooms' refusals, named as docs/TOURNAMENTS.md names them.
var (
	errNotInMatch     = errors.Forbidden("not_in_match")
	errTournamentRoom = errors.Conflict("tournament_room")
)

// errMatchRoomTaken is a match room made while another was being made for
// the same match: the seat goes to the one already there. Never answered.
var errMatchRoomTaken = stderrors.New("rooms: the match has an open room")

// Matches is the tournaments as the rooms see them. *tournaments.Service is
// one; nil is a platform where every room is an ordinary one.
type Matches interface {
	// Admits says whether the player may sit in the match's room: one of a
	// knockout match's two sides, or a league's current entrant.
	Admits(ctx context.Context, matchID, playerID string) (bool, error)

	// Count is told that a round of the match's room closed with these
	// results, in the room's own transaction with its row held. It answers
	// whether the match is now decided, or its tournament over: the room
	// then ends with the reason tournament.
	Count(ctx context.Context, r *model.Room, round int, results []model.Score) (bool, error)

	// Brief is the match as the Room carries it: nil for a match that is
	// gone.
	Brief(ctx context.Context, matchID string) (*MatchBrief, error)
}

// MatchBrief is the tournament match a room plays, as every member reads it
// on the Room and a game's server on the bridge's context.
type MatchBrief struct {
	ID     string       `json:"id" example:"trn_7f3k9d2m8q1x4b6n" doc:"The tournament."`
	Name   string       `json:"name" example:"بطولة الجمعة"`
	Format string       `json:"format" enum:"knockout,league"`
	Match  string       `json:"match" example:"trm_7f3k9d2m8q1x4b6n"`
	Stage  int          `json:"stage" doc:"Knockout: 1 is the first stage, stages the final. League: 1."`
	Stages int          `json:"stages"`
	BestOf int          `json:"best_of" doc:"Knockout: the match's length, 1, 3 or 5. League: 1."`
	Need   int          `json:"need" doc:"Knockout: the round wins that take the match. League: 1."`
	Rounds int          `json:"rounds" doc:"League: the rounds it plays. Knockout: 0."`
	Played int          `json:"played" doc:"The match's rounds counted so far, in any of its rooms."`
	Tally  []MatchTally `json:"tally" doc:"Knockout: the two sides and their wins. League: the table, best first."`
}

// MatchTally is one player's line of a match's tally.
type MatchTally struct {
	Player string `json:"player" example:"ply_7f3k9d2m8q1x4b6n"`
	Wins   int    `json:"wins" doc:"Knockout: rounds won. League: first places."`
	Points int    `json:"points" doc:"League: a point for every ranked player beaten in a round. Knockout: 0."`
}

// MatchSeat is a match asking for a room: the match, its game, and the room
// block with the table the match sets (a knockout's two, a league's
// entrants).
type MatchSeat struct {
	MatchID string
	GameID  string
	Config  model.RoomBlock
}

// Seat puts the player in the match's room: the open one, joined by its code
// with every rule of a join, or, when the match has none, a new one with the
// player as its host. Two players asking at once share one room: the second
// joins the first's. A room that closes between the look and the join is
// looked for again.
func (s *Service) Seat(ctx context.Context, playerID string, in MatchSeat) (*Room, error) {
	for range 3 {
		code, err := s.matchRoomCode(ctx, in.MatchID)
		if err != nil {
			return nil, errors.Internal(err)
		}
		if code != "" {
			room, err := s.Join(ctx, playerID, code, in.GameID)
			if stderrors.Is(err, errRoomClosed) || stderrors.Is(err, errRoomNotFound) {
				continue
			}
			return room, err
		}
		room, err := s.createForMatch(ctx, playerID, in)
		if stderrors.Is(err, errMatchRoomTaken) {
			continue
		}
		return room, err
	}
	return nil, errors.Internal(fmt.Errorf("rooms: no seat in match %s's room", in.MatchID))
}

// matchRoomCode is the code of the match's open room, or "".
func (s *Service) matchRoomCode(ctx context.Context, matchID string) (string, error) {
	var codes []string
	err := data.Conn(ctx, s.db).Raw(`SELECT code FROM rooms WHERE match_id = ? AND status <> ? LIMIT 1`,
		matchID, enum.RoomEnded).Scan(&codes).Error
	if err != nil || len(codes) == 0 {
		return "", err
	}
	return codes[0], nil
}

// createForMatch makes the match's room with the player as its host, under
// the match's own lock: errMatchRoomTaken when one is open already.
func (s *Service) createForMatch(ctx context.Context, playerID string, in MatchSeat) (*Room, error) {
	g, err := gameRow(ctx, s.db, in.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil || g.Listing != enum.Published {
		return nil, errNotFound
	}
	config := in.Config
	if config.Players.Max > s.cfg.MaxMembers {
		config.Players.Max = s.cfg.MaxMembers
	}
	if config.Players.Min > config.Players.Max {
		config.Players.Min = config.Players.Max
	}
	if config.State == "" {
		config.State = enum.StateBlob
	}

	now := data.Now()
	matchID := in.MatchID
	room := &model.Room{
		GameID: g.ID, HostID: playerID, Status: enum.RoomLobby, Visibility: enum.VisibilityPrivate,
		MatchID: &matchID, Config: config, CreatedAt: now, ActiveAt: now,
	}
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := data.Conn(ctx, s.db).Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
			"match-room:"+matchID).Error; err != nil {
			return err
		}
		if err := lockPlayer(ctx, s.db, playerID); err != nil {
			return err
		}
		code, err := s.matchRoomCode(ctx, matchID)
		if err != nil {
			return err
		}
		if code != "" {
			return errMatchRoomTaken
		}
		other, err := openRoomOf(ctx, s.db, playerID)
		if err != nil {
			return err
		}
		if other != "" {
			return errors.InAnotherRoom(other)
		}

		placed := false
		for range codeAttempts {
			room.ID, room.Code = ids.New(ids.Room), s.newCode()
			if placed, err = insertRoom(ctx, s.db, room); err != nil {
				return err
			}
			if placed {
				break
			}
		}
		if !placed {
			return fmt.Errorf("rooms: no free code after %d draws", codeAttempts)
		}
		err = data.Conn(ctx, s.db).Create(&model.RoomMember{
			RoomID: room.ID, PlayerID: playerID, Role: enum.RoomHost, Seat: 0, JoinedAt: now,
		}).Error
		if err != nil {
			return err
		}
		return event(ctx, s.db, room, playerID, enum.EventCreated, map[string]any{"code": room.Code, "match": matchID})
	})
	if stderrors.Is(err, errMatchRoomTaken) {
		return nil, err
	}
	if err != nil {
		return nil, asError(err)
	}
	return s.Get(ctx, playerID, room.ID)
}

// inMatch is the match room's door: whether the player is one of the
// match's. Asked in the join's transaction. Without the tournaments there is
// no match, and nobody comes in.
func (s *Service) inMatch(ctx context.Context, matchID, playerID string) (bool, error) {
	if s.Matches == nil {
		return false, nil
	}
	return s.Matches.Admits(ctx, matchID, playerID)
}

// counted asks the tournament about a round of a match room closing with
// these results: whether it decided the match (the room then ends with the
// reason tournament). Only a round under way counts, and only in a match
// room; the caller holds the room's row and its transaction.
func (s *Service) counted(ctx context.Context, r *model.Room, results []model.Score) (bool, error) {
	if r.MatchID == nil || s.Matches == nil || r.Status != enum.RoomPlaying {
		return false, nil
	}
	return s.Matches.Count(ctx, r, r.Round, results)
}

// decidedEnd closes a match room whose round just decided its match: an end
// with the reason tournament in place of the finish, in the caller's
// transaction, the round's results as the room's copy. who is the host whose
// finish closed the round, or empty for the clock.
func (s *Service) decidedEnd(ctx context.Context, r *model.Room, who string, results []model.Score, now time.Time) error {
	if err := updateRoom(ctx, s.db, r.ID, s.endFields(ctx, r, now, enum.EndTournament, model.Scores(results))); err != nil {
		return err
	}
	return event(ctx, s.db, r, who, enum.EventEnded, map[string]any{
		"reason": enum.EndTournament, "round": r.Round, "scores": len(results), "match": *r.MatchID,
	})
}

// CloseMatchRoom ends the match's open room, if it has one, for a match
// decided away from its table (an award, a walkover, a league finished
// early) or a tournament called off: the reason tournament, nobody named. A
// round under way closes with it, carrying the game's results if its server
// wrote them, and is not counted: the match is already decided. Called after
// the tournament's own change is committed.
func (s *Service) CloseMatchRoom(ctx context.Context, matchID string) error {
	var roomIDs []string
	err := data.Conn(ctx, s.db).Raw(`SELECT id FROM rooms WHERE match_id = ? AND status <> ?`,
		matchID, enum.RoomEnded).Scan(&roomIDs).Error
	if err != nil {
		return errors.Internal(err)
	}
	for _, id := range roomIDs {
		if _, err := s.sweepEnd(ctx, id, enum.EndTournament, func(r *model.Room) bool { return r.Open() }); err != nil {
			return asError(err)
		}
	}
	return nil
}

// matchOf is the match a room plays as the Room carries it, or nil. A brief
// that cannot be read leaves the room without it rather than failing the
// room: the lobby still works, and the next read says it.
func (s *Service) matchOf(ctx context.Context, r *model.Room) *MatchBrief {
	if r.MatchID == nil || s.Matches == nil {
		return nil
	}
	brief, err := s.Matches.Brief(ctx, *r.MatchID)
	if err != nil {
		applog.From(ctx).Warn("match not read", "room", r.ID, "match", *r.MatchID, "err", err)
		return nil
	}
	return brief
}
