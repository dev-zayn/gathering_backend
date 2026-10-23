package rooms

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// The refusals, one value each, named as docs/ROOMS.md names them.
var (
	errNotFound     = errors.NotFound("not_found")
	errRoomNotFound = errors.NotFound("room_not_found")
	errNotMember    = errors.Forbidden("not_member")
	errHostOnly     = errors.Forbidden("host_only")
	errKicked       = errors.Forbidden("kicked")
	errRoomClosed   = errors.Conflict("room_closed")
	errRoomStarted  = errors.Conflict("room_started")
	errRoomFull     = errors.Conflict("room_full")
	errTooFew       = errors.Conflict("too_few_players")
	errFriendsOnly  = errors.Forbidden("friends_only")

	// errNotThisGame is a join that named its game meeting a code another
	// game's room holds (a launch into such a room is errAnotherGame).
	errNotThisGame = errors.Conflict("another_game")
)

// codeAttempts is how many codes a create draws before giving up. Each
// collides with the open rooms' share of a billion codes, so the second draw
// is already a rarity.
const codeAttempts = 8

// asError keeps a refusal as it is and turns anything else into the one
// internal error: a transaction hands back whatever its body returned.
func asError(err error) error {
	var e *errors.Error
	if stderrors.As(err, &e) {
		return e
	}
	return errors.Internal(err)
}

// CreateRequest is a room as the app asks for one.
type CreateRequest struct {
	Game       string
	LAN        bool
	Visibility string
}

// Create makes a room for a published game, with the caller as its host in
// seat 0. The game's room block is copied in, capped at what the relay
// carries. A player still in another open room is told which.
func (s *Service) Create(ctx context.Context, playerID string, in CreateRequest) (*Room, error) {
	visibility := enum.Visibility(in.Visibility)
	switch visibility {
	case "":
		visibility = enum.VisibilityPrivate
	case enum.VisibilityPrivate, enum.VisibilityFriends:
	default:
		return nil, errors.Invalid(map[string]string{"visibility": "unknown"})
	}

	g, err := gameRow(ctx, s.db, in.Game)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil || g.Listing != enum.Published {
		return nil, errNotFound
	}
	if in.LAN && !g.Room.LAN {
		return nil, errors.Invalid(map[string]string{"lan": "not_allowed"})
	}

	config := g.Room
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
	room := &model.Room{
		GameID: g.ID, HostID: playerID, Status: enum.RoomLobby, Visibility: visibility,
		LAN: in.LAN, Config: config, CreatedAt: now, ActiveAt: now,
	}
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPlayer(ctx, s.db, playerID); err != nil {
			return err
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
		return event(ctx, s.db, room, playerID, enum.EventCreated, map[string]any{"code": room.Code, "lan": room.LAN})
	})
	if err != nil {
		return nil, asError(err)
	}
	return s.Get(ctx, playerID, room.ID)
}

// Join is by code, typed however a friend read it out. A current member gets
// the room back as it is; anybody else takes the lowest free seat, if the
// room will have them. A friends-only room has the host's friends alone,
// and nobody either side of a block with the host. game, when not empty, is
// the game the caller expects (a game's page outside a room): a code another
// game's room holds is refused right after it is found, before anything
// about the caller, so a member of that room is refused too. Empty, the code
// may be any game's.
func (s *Service) Join(ctx context.Context, playerID, typed, game string) (*Room, error) {
	code := normalizeCode(typed)
	if !s.validCode(code) {
		return nil, errors.Invalid(map[string]string{"code": "must_be_6_symbols"})
	}

	var (
		room    *model.Room
		already bool
		seat    int
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPlayer(ctx, s.db, playerID); err != nil {
			return err
		}
		r, err := lockOpenByCode(ctx, s.db, code)
		if err != nil {
			return err
		}
		if r == nil {
			ended, err := endedWithCode(ctx, s.db, code)
			if err != nil {
				return err
			}
			if ended {
				return errRoomClosed
			}
			return errRoomNotFound
		}
		if game != "" && r.GameID != game {
			return errNotThisGame
		}
		room = r

		m, err := membership(ctx, s.db, r.ID, playerID)
		if err != nil {
			return err
		}
		if m != nil && m.Current() {
			already = true
			return nil
		}
		if m != nil && m.Kicked {
			return errKicked
		}
		// A match room's door is the match (docs/TOURNAMENTS.md): only its
		// players, whatever the room's visibility says.
		if r.MatchID != nil {
			ok, err := s.inMatch(ctx, *r.MatchID, playerID)
			if err != nil {
				return err
			}
			if !ok {
				return errNotInMatch
			}
		} else if r.Visibility == enum.VisibilityFriends {
			ok, err := s.friendOf(ctx, r.HostID, playerID)
			if err != nil {
				return err
			}
			if !ok {
				return errFriendsOnly
			}
		}
		other, err := openRoomOf(ctx, s.db, playerID)
		if err != nil {
			return err
		}
		if other != "" {
			return errors.InAnotherRoom(other)
		}
		// A round under way takes back somebody who stepped out, when the game
		// says it can; nobody new. Between rounds the lobby takes anybody the
		// door lets in, whatever the game says about a rejoin.
		if r.Status == enum.RoomPlaying && (!r.Config.Rejoin || m == nil) {
			return errRoomStarted
		}
		rows, err := currentMembers(ctx, s.db, r.ID)
		if err != nil {
			return err
		}
		if len(rows) >= r.Config.Players.Max {
			return errRoomFull
		}

		// Their old seat may be taken; a rejoiner sits where a newcomer would.
		seat = lowestFreeSeat(rows)
		now := data.Now()
		if m != nil {
			err = updateMember(ctx, s.db, r.ID, playerID, map[string]any{
				"role": enum.RoomPlayer, "seat": seat, "joined_at": now, "left_at": nil,
			})
		} else {
			err = data.Conn(ctx, s.db).Create(&model.RoomMember{
				RoomID: r.ID, PlayerID: playerID, Role: enum.RoomPlayer, Seat: seat, JoinedAt: now,
			}).Error
		}
		if err != nil {
			return err
		}
		if err := updateRoom(ctx, s.db, r.ID, map[string]any{"active_at": now}); err != nil {
			return err
		}
		return event(ctx, s.db, r, playerID, enum.EventJoined, map[string]any{"seat": seat, "rejoin": m != nil})
	})
	if err != nil {
		return nil, asError(err)
	}

	if !already {
		list, err := s.members(ctx, room.ID)
		if err == nil {
			s.publish(ctx, room.ID,
				Joined{T: FrameJoined, Player: pick(list, playerID)},
				Members{T: FrameMembers, List: list})
		}
	}
	return s.Get(ctx, playerID, room.ID)
}

// pick is one member out of a list, or the bare id when the list has moved on.
func pick(list []Member, playerID string) Member {
	for _, m := range list {
		if m.ID == playerID {
			return m
		}
	}
	return Member{ID: playerID, Role: string(enum.RoomPlayer)}
}

// Leave frees the seat. A host leaving hands the room to the member in the
// lowest seat; the last one leaving ends it, finishing the round under way if
// there is one. Leaving a room that has already ended only takes the caller
// out of it: there is nobody to tell.
func (s *Service) Leave(ctx context.Context, playerID, roomID string) error {
	var (
		room    *model.Room
		wasOpen bool
		newHost string
		emptied bool
		round   int
		results []model.Score
		now     time.Time
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := lockRoom(ctx, s.db, roomID)
		if err != nil {
			return err
		}
		if r == nil {
			return errNotMember
		}
		room, wasOpen = r, r.Open()
		m, err := membership(ctx, s.db, r.ID, playerID)
		if err != nil {
			return err
		}
		if m == nil || !m.Current() {
			return errNotMember
		}

		now = data.Now()
		if err := updateMember(ctx, s.db, r.ID, playerID, map[string]any{"left_at": now}); err != nil {
			return err
		}
		if err := event(ctx, s.db, r, playerID, enum.EventLeft, map[string]any{"seat": m.Seat}); err != nil {
			return err
		}
		if !wasOpen || r.HostID != playerID {
			return nil
		}

		rows, err := currentMembers(ctx, s.db, r.ID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			emptied, round = true, r.Round
			if results, err = s.closing(ctx, r); err != nil {
				return err
			}
			// A match room's round under way closes with the room: counted.
			if _, err := s.counted(ctx, r, results); err != nil {
				return err
			}
			if err := updateRoom(ctx, s.db, r.ID, s.endFields(ctx, r, now, enum.EndEmpty, model.Scores(results))); err != nil {
				return err
			}
			return event(ctx, s.db, r, "", enum.EventEnded, map[string]any{"reason": enum.EndEmpty, "round": r.Round})
		}

		newHost = rows[0].PlayerID
		if err := updateMember(ctx, s.db, r.ID, playerID, map[string]any{"role": enum.RoomPlayer}); err != nil {
			return err
		}
		if err := updateMember(ctx, s.db, r.ID, newHost, map[string]any{"role": enum.RoomHost}); err != nil {
			return err
		}
		if err := updateRoom(ctx, s.db, r.ID, map[string]any{"host_id": newHost}); err != nil {
			return err
		}
		return event(ctx, s.db, r, newHost, enum.EventHost, map[string]any{"from": playerID, "by": nil})
	})
	if err != nil {
		return asError(err)
	}
	if !wasOpen {
		return nil
	}

	// The new host first, so the room is never without one; then the leave,
	// which closes the leaver's socket; then the list.
	var frames []any
	if newHost != "" {
		frames = append(frames, HostChanged{T: FrameHost, Player: newHost})
	}
	frames = append(frames, Left{T: FrameLeft, Player: playerID, Reason: ReasonLeft})
	if !emptied {
		if list, err := s.members(ctx, room.ID); err == nil {
			frames = append(frames, Members{T: FrameMembers, List: list})
		}
	}
	s.publish(ctx, room.ID, frames...)
	if emptied {
		s.end(ctx, room.ID, Ended{T: FrameEnded, At: stamp(now), Reason: string(enum.EndEmpty), Round: round, Results: results})
	}
	return nil
}

// Kick is the host's, never of themselves, and for good: the kicked player
// cannot come back to this room.
func (s *Service) Kick(ctx context.Context, hostID, roomID, targetID string) error {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := s.lockAsHost(ctx, roomID, hostID)
		if err != nil {
			return err
		}
		// A match is its two players' (or its league's), and a host does not
		// get to choose an opponent.
		if r.MatchID != nil {
			return errTournamentRoom
		}
		if targetID == hostID {
			return errors.Invalid(map[string]string{"player": "is_you"})
		}
		t, err := membership(ctx, s.db, r.ID, targetID)
		if err != nil {
			return err
		}
		if t == nil || !t.Current() {
			return errors.Invalid(map[string]string{"player": "not_a_member"})
		}
		if err := updateMember(ctx, s.db, r.ID, targetID, map[string]any{"left_at": data.Now(), "kicked": true}); err != nil {
			return err
		}
		return event(ctx, s.db, r, targetID, enum.EventKicked, map[string]any{"by": hostID, "seat": t.Seat})
	})
	if err != nil {
		return asError(err)
	}

	frames := []any{
		Kicked{T: FrameKicked, Player: targetID, By: hostID},
		Left{T: FrameLeft, Player: targetID, Reason: ReasonKicked},
	}
	if list, err := s.members(ctx, roomID); err == nil {
		frames = append(frames, Members{T: FrameMembers, List: list})
	}
	s.publish(ctx, roomID, frames...)
	return nil
}

// Start is the host's, from the lobby, with at least the game's minimum in
// the room. Every start begins a round, the first or the next: the round
// counts on, and the relay resets the shared state as it publishes the start.
func (s *Service) Start(ctx context.Context, playerID, roomID string) (*Room, error) {
	var (
		now   time.Time
		round int
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := s.lockAsHost(ctx, roomID, playerID)
		if err != nil {
			return err
		}
		if r.Status == enum.RoomPlaying {
			return errRoomStarted
		}
		rows, err := currentMembers(ctx, s.db, r.ID)
		if err != nil {
			return err
		}
		if len(rows) < r.Config.Players.Min {
			return errTooFew
		}
		now, round = data.Now(), r.Round+1
		err = updateRoom(ctx, s.db, r.ID, map[string]any{
			"status": enum.RoomPlaying, "round": round, "started_at": now, "active_at": now,
		})
		if err != nil {
			return err
		}
		return event(ctx, s.db, r, playerID, enum.EventStarted, map[string]any{"members": len(rows), "round": round})
	})
	if err != nil {
		return nil, asError(err)
	}
	s.start(ctx, roomID, Started{T: FrameStarted, At: stamp(now), By: playerID, Round: round})
	return s.Get(ctx, playerID, roomID)
}

// Finish is the host's, from a round under way: the round's results, by the
// rules of an end, and the room back in its lobby with the same code,
// members and sockets, for the host to start the next round. Each score names
// somebody who was in the room at some point; a missing rank is computed.
// When the game's own server has written this round's results already, its
// word stands, the scores are checked but not kept, and the finish carries
// the game's instead. In a match room the round is counted for the match, and
// the round that decides it closes the room instead (the reason tournament:
// docs/TOURNAMENTS.md, "Match rooms").
func (s *Service) Finish(ctx context.Context, playerID, roomID string, scores []ScoreIn) (*Room, error) {
	var (
		now     time.Time
		round   int
		results []model.Score
		decided bool
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := s.lockAsHost(ctx, roomID, playerID)
		if err != nil {
			return err
		}
		if r.Status != enum.RoomPlaying {
			return errRoomNotStarted
		}
		ever, err := everMembers(ctx, s.db, r.ID)
		if err != nil {
			return err
		}
		if err := checkScores(scores, ever); err != nil {
			return err
		}
		var write bool
		if results, write, err = roundResults(ctx, s.db, r, scores); err != nil {
			return err
		}
		if decided, err = s.counted(ctx, r, results); err != nil {
			return err
		}
		now, round = data.Now(), r.Round
		if decided {
			err = s.decidedEnd(ctx, r, playerID, results, now)
		} else {
			err = s.finishRound(ctx, r, playerID, enum.EndHost, results, now)
		}
		if err != nil || !write {
			return err
		}
		return s.writeResults(ctx, r, r.Round, ever, playerID, enum.SubmitterHost, results, now)
	})
	if err != nil {
		return nil, asError(err)
	}
	by := playerID
	if decided {
		s.end(ctx, roomID, Ended{
			T: FrameEnded, At: stamp(now), By: &by, Reason: string(enum.EndTournament), Round: round, Results: results,
		})
	} else {
		s.publish(ctx, roomID, Finished{
			T: FrameFinished, At: stamp(now), By: &by, Round: round, Reason: string(enum.EndHost), Results: results,
		})
	}
	return s.Get(ctx, playerID, roomID)
}

// finishRound puts a room whose round is over back in its lobby: the status,
// the copy of the round's results, the room in use as of now, and the event,
// in the caller's transaction. who is the host, or empty for the clock.
func (s *Service) finishRound(ctx context.Context, r *model.Room, who string, reason enum.EndReason,
	results []model.Score, now time.Time) error {
	err := updateRoom(ctx, s.db, r.ID, map[string]any{
		"status": enum.RoomLobby, "results": model.Scores(results), "active_at": now,
	})
	if err != nil {
		return err
	}
	return event(ctx, s.db, r, who, enum.EventFinished, map[string]any{
		"round": r.Round, "reason": reason, "scores": len(results),
	})
}

// End is the host's, from the lobby or during a round, and closes the room for
// good. During a round it finishes that round too: the scores are the round's
// results, unless the game's own server has written this round's already, in
// which case its word stands and this end carries it instead. In the lobby it
// takes no scores and carries the latest round's results. The live state is
// copied here, and the relay closes every socket once the ended frame is
// delivered.
func (s *Service) End(ctx context.Context, playerID, roomID string, scores []ScoreIn) (*Room, error) {
	var (
		now     time.Time
		round   int
		results []model.Score
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := s.lockAsHost(ctx, roomID, playerID)
		if err != nil {
			return err
		}
		round = r.Round
		var (
			ever  map[string]int
			write bool
		)
		if r.Status == enum.RoomPlaying {
			if ever, err = everMembers(ctx, s.db, r.ID); err != nil {
				return err
			}
			if err := checkScores(scores, ever); err != nil {
				return err
			}
			if results, write, err = roundResults(ctx, s.db, r, scores); err != nil {
				return err
			}
			// A match room's round closes with the room: counted, and the room
			// ends as its host said either way.
			if _, err := s.counted(ctx, r, results); err != nil {
				return err
			}
		} else {
			if len(scores) > 0 {
				return errors.Invalid(map[string]string{"scores": "not_playing"})
			}
			results = latestResults(r)
		}

		now = data.Now()
		if err := updateRoom(ctx, s.db, r.ID, s.endFields(ctx, r, now, enum.EndHost, model.Scores(results))); err != nil {
			return err
		}
		err = event(ctx, s.db, r, playerID, enum.EventEnded, map[string]any{
			"reason": enum.EndHost, "scores": len(results), "round": r.Round,
		})
		if err != nil || !write {
			return err
		}
		return s.writeResults(ctx, r, r.Round, ever, playerID, enum.SubmitterHost, results, now)
	})
	if err != nil {
		return nil, asError(err)
	}
	by := playerID
	s.end(ctx, roomID, Ended{
		T: FrameEnded, At: stamp(now), By: &by, Reason: string(enum.EndHost), Round: round, Results: results,
	})
	return s.Get(ctx, playerID, roomID)
}

// closing is what an end that brings no scores of its own carries: during a
// round, that round's results from the game's server, else none; between
// rounds, the latest round's.
func (s *Service) closing(ctx context.Context, r *model.Room) ([]model.Score, error) {
	if r.Status != enum.RoomPlaying {
		return latestResults(r), nil
	}
	results, _, err := roundResults(ctx, s.db, r, nil)
	return results, err
}

// Host hands the room to another current member. Handing it to yourself
// changes nothing and says so by answering the room as it is.
func (s *Service) Host(ctx context.Context, playerID, roomID, targetID string) (*Room, error) {
	changed := false
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := s.lockAsHost(ctx, roomID, playerID)
		if err != nil {
			return err
		}
		t, err := membership(ctx, s.db, r.ID, targetID)
		if err != nil {
			return err
		}
		if t == nil || !t.Current() {
			return errors.Invalid(map[string]string{"player": "not_a_member"})
		}
		if targetID == playerID {
			return nil
		}
		changed = true
		if err := updateMember(ctx, s.db, r.ID, playerID, map[string]any{"role": enum.RoomPlayer}); err != nil {
			return err
		}
		if err := updateMember(ctx, s.db, r.ID, targetID, map[string]any{"role": enum.RoomHost}); err != nil {
			return err
		}
		if err := updateRoom(ctx, s.db, r.ID, map[string]any{"host_id": targetID}); err != nil {
			return err
		}
		return event(ctx, s.db, r, targetID, enum.EventHost, map[string]any{"from": playerID, "by": playerID})
	})
	if err != nil {
		return nil, asError(err)
	}
	if changed {
		by := playerID
		frames := []any{HostChanged{T: FrameHost, Player: targetID, By: &by}}
		if list, err := s.members(ctx, roomID); err == nil {
			frames = append(frames, Members{T: FrameMembers, List: list})
		}
		s.publish(ctx, roomID, frames...)
	}
	return s.Get(ctx, playerID, roomID)
}

// lockAsHost is the check every host-only call opens with, in the order the
// refusals are named: a member at all, a room still open, the host.
func (s *Service) lockAsHost(ctx context.Context, roomID, playerID string) (*model.Room, error) {
	r, err := lockRoom(ctx, s.db, roomID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errNotMember
	}
	m, err := membership(ctx, s.db, r.ID, playerID)
	if err != nil {
		return nil, err
	}
	if m == nil || !m.Current() {
		return nil, errNotMember
	}
	if !r.Open() {
		return nil, errRoomClosed
	}
	if r.HostID != playerID {
		return nil, errHostOnly
	}
	return r, nil
}

// endFields is an end, whoever called it: the status, why, the results (the
// latest round's), and the live state copied out of the relay.
func (s *Service) endFields(ctx context.Context, r *model.Room, now time.Time, reason enum.EndReason, results model.Scores) map[string]any {
	fields := map[string]any{
		"status": enum.RoomEnded, "ended_at": now, "end_reason": reason,
		"results": results, "active_at": now,
	}
	if r.Config.State == enum.StateNone {
		return fields
	}
	if st, ok := s.liveState(ctx, r.ID); ok && st.Version > 0 {
		fields["state"] = model.NullRaw(rawOrNil(st.Data))
		fields["state_version"] = st.Version
	}
	return fields
}

// Ticket is a room ticket: who, which room, what they are there, where they
// sit, and which round it is, for the game's own server to verify through the
// JWKS.
type Ticket struct {
	Token     string
	ExpiresIn int
}

func (s *Service) Ticket(ctx context.Context, playerID, roomID string) (*Ticket, error) {
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if r == nil {
		return nil, errNotMember
	}
	m, err := membership(ctx, s.db, r.ID, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if m == nil || !m.Current() {
		return nil, errNotMember
	}
	if !r.Open() {
		return nil, errRoomClosed
	}
	token, _, err := s.Minter.MintRoom(identity.RoomClaims{
		GameID: r.GameID, PlayerID: playerID, RoomID: r.ID, Role: string(m.Role), Seat: m.Seat, Round: r.Round,
	}, s.cfg.TicketTTL)
	if err != nil {
		return nil, asError(err)
	}
	return &Ticket{Token: token, ExpiresIn: int(s.cfg.TicketTTL.Seconds())}, nil
}

// Get is one room, for anybody who was ever in it. A stranger is told there
// is no such room: which rooms exist is not their business.
func (s *Service) Get(ctx context.Context, playerID, roomID string) (*Room, error) {
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if r == nil {
		return nil, errNotFound
	}
	m, err := membership(ctx, s.db, r.ID, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if m == nil {
		return nil, errNotFound
	}
	return s.view(ctx, r, playerID)
}

// Mine is the open room the player is in: none, or one.
func (s *Service) Mine(ctx context.Context, playerID string) ([]Room, error) {
	id, err := openRoomOf(ctx, s.db, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if id == "" {
		return []Room{}, nil
	}
	r, err := s.Get(ctx, playerID, id)
	if err != nil {
		return nil, err
	}
	return []Room{*r}, nil
}
