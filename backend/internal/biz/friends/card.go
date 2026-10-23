package friends

import (
	"context"
	"slices"
	"strings"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// PlayerCard is another player as the caller sees them: the public card,
// where the caller stands with them, the friends they share, and — between
// friends only — where they are.
type PlayerCard struct {
	players.Card
	Friendship    string    `json:"friendship" enum:"none,requested,incoming,friends,blocked" doc:"Where the caller stands with them: requested (the caller asked), incoming (they asked), blocked (the caller blocked them)."`
	MutualFriends int       `json:"mutual_friends" doc:"Accepted friends the two share."`
	Presence      *Presence `json:"presence" doc:"Between friends only; null for anybody else."`
}

// Presence is where a friend is: in a room (which, so the app can say so;
// joining it still takes its code or an invite), online, or offline.
type Presence struct {
	State string        `json:"state" enum:"online,in_room,offline"`
	Room  *PresenceRoom `json:"room" doc:"The room they are in; null unless in_room."`
}

// PresenceRoom is the room a friend is in.
type PresenceRoom struct {
	ID   string     `json:"id" example:"room_7f3k9d2m8q1x4b6n"`
	Code string     `json:"code" example:"K7M2QX"`
	Game games.Tile `json:"game"`
}

// Friend is one friend in the list: who, since when, and where they are.
type Friend struct {
	Player   players.Card `json:"player"`
	Since    string       `json:"since" doc:"When the request was accepted."`
	Presence Presence     `json:"presence"`
}

// Card is the player ref names — a handle, or an id, which is read as one
// first — as the viewer sees them. A player who blocked the viewer is not
// found, unless the viewer blocked them too: the viewer's own block is
// theirs to see, and to lift.
func (s *Service) Card(ctx context.Context, viewerID, ref string) (*PlayerCard, error) {
	p, err := s.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := &PlayerCard{
		Card:       players.NewCard(ctx, s.Files, s.AvatarTTL, p.ID, p.Nickname, p.Handle, p.AvatarKey),
		Friendship: None,
	}
	if p.ID == viewerID {
		return out, nil
	}

	mine, theirs, err := blocksBetween(ctx, s.db, viewerID, p.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if theirs && !mine {
		return nil, errNotFound
	}
	row, err := friendship(ctx, s.db, viewerID, p.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	switch {
	case mine:
		out.Friendship = Blocked
	case row == nil:
	case row.Status == enum.FriendshipAccepted:
		out.Friendship = Friends
	case row.RequestedBy == viewerID:
		out.Friendship = Requested
	default:
		out.Friendship = Incoming
	}

	if out.MutualFriends, err = mutual(ctx, s.db, viewerID, p.ID); err != nil {
		return nil, errors.Internal(err)
	}
	if out.Friendship == Friends {
		st := s.presence(ctx, []string{p.ID})[p.ID]
		out.Presence = &st
	}
	return out, nil
}

// resolve is the player a ref names: an id when it is shaped like one and
// somebody holds it, else a handle, with or without its @. A player an admin
// switched off is nobody.
func (s *Service) resolve(ctx context.Context, ref string) (*model.Player, error) {
	ref = strings.TrimSpace(ref)
	var p *model.Player
	var err error
	if ids.Is(ids.Player, ref) {
		if p, err = s.Players.ByID(ctx, ref); err != nil {
			return nil, errors.Internal(err)
		}
	}
	if p == nil {
		if p, err = s.Players.ByHandle(ctx, strings.TrimPrefix(ref, "@")); err != nil {
			return nil, errors.Internal(err)
		}
	}
	if p == nil || p.Disabled {
		return nil, errNotFound
	}
	return p, nil
}

// Friends is the player's friends with their presence: in a room first,
// then online, then offline, each by nickname.
func (s *Service) Friends(ctx context.Context, playerID string) ([]Friend, error) {
	rows, err := accepted(ctx, s.db, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	who := make([]string, len(rows))
	for i, r := range rows {
		who[i] = r.ID
	}
	where := s.presence(ctx, who)

	out := make([]Friend, 0, len(rows))
	for _, r := range rows {
		f := Friend{Player: s.card(ctx, r), Presence: where[r.ID]}
		if r.At != nil {
			f.Since = stamp(*r.At)
		}
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(a, b Friend) int {
		if d := order(a.Presence.State) - order(b.Presence.State); d != 0 {
			return d
		}
		if c := byName(a.Player.Nickname, b.Player.Nickname); c != 0 {
			return c
		}
		return strings.Compare(a.Player.ID, b.Player.ID)
	})
	return out, nil
}

// order is where a state sorts: in a room first, offline last.
func order(state string) int {
	switch state {
	case players.InRoom:
		return 0
	case players.Online:
		return 1
	default:
		return 2
	}
}

// presence is where each of the players is. Presence says the state; a
// room it names counts only while the room is open and the player still a
// current member of it, and is online otherwise. Presence that cannot be
// read is everybody offline, not a failed answer.
func (s *Service) presence(ctx context.Context, who []string) map[string]Presence {
	out := make(map[string]Presence, len(who))
	for _, id := range who {
		out[id] = Presence{State: players.Offline}
	}
	if s.Presence == nil || len(who) == 0 {
		return out
	}
	states, err := s.Presence.Of(ctx, who)
	if err != nil {
		applog.From(ctx).Warn("presence not read", "err", err)
		return out
	}

	var inRoom, roomIDs []string
	for _, id := range who {
		st := states[id]
		switch st.State {
		case players.InRoom:
			out[id] = Presence{State: players.Online}
			if st.Room != "" {
				inRoom, roomIDs = append(inRoom, id), append(roomIDs, st.Room)
			}
		case players.Online:
			out[id] = Presence{State: players.Online}
		}
	}
	if len(inRoom) == 0 {
		return out
	}
	rows, err := openRooms(ctx, s.db, inRoom, roomIDs)
	if err != nil {
		applog.From(ctx).Warn("friends' rooms not read", "err", err)
		return out
	}
	for _, r := range rows {
		if states[r.PlayerID].Room != r.RoomID {
			continue
		}
		g := &model.Game{ID: r.GameID, Name: r.GameName, IconKey: r.GameIconKey}
		out[r.PlayerID] = Presence{State: players.InRoom, Room: &PresenceRoom{
			ID: r.RoomID, Code: r.Code, Game: games.TileOf(ctx, s.Files, r.GameID, g),
		}}
	}
	return out
}
