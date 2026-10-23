package rooms

// Friends in rooms: the door of a friends-only room, and a member asking
// friends to the room they are in. docs/FRIENDS.md, "Rooms (extended)", is
// the contract.

import (
	"context"
	"fmt"

	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Friends is who may meet whom. *friends.Service is one; nil is a platform
// where nobody is anybody's friend.
type Friends interface {
	// Among is which of others are the player's friends, with no block
	// between the two either way.
	Among(ctx context.Context, playerID string, others []string) (map[string]bool, error)
}

// Notifier raises a notification in a player's inbox. *inbox.Inbox is one.
type Notifier interface {
	Raise(ctx context.Context, n inbox.Notice) (*inbox.Raised, error)
}

// maxInvites is the most players one invite names.
const maxInvites = 20

// The invite's words: the title, and the body after the inviter's name.
const (
	inviteTitle = "دعوة للعب"
	inviteBody  = "%s يدعوك تلعب %s"
)

// friendOf says whether the player may come into the host's friends-only
// room: the host's friend, with no block either way. Asked in the join's
// transaction, so it reads what the join holds.
func (s *Service) friendOf(ctx context.Context, hostID, playerID string) (bool, error) {
	if s.Friends == nil {
		return false, nil
	}
	found, err := s.Friends.Among(ctx, hostID, []string{playerID})
	if err != nil {
		return false, err
	}
	return found[playerID], nil
}

// Invite is a current member of an open room asking friends to it. Each
// invitee must be the caller's friend, with no block either way, and not
// already in the room; every problem comes back at once and nobody is asked
// unless all pass. Each gets a room_invite notification whose link joins by
// the room's code; inviting the same player to the same room again brings
// that one back to the top, unread. An invite holds no seat: joining from it
// is the join by code, with every refusal the join has. It answers the
// players invited, each once.
func (s *Service) Invite(ctx context.Context, playerID, roomID string, invitees []string) ([]string, error) {
	if len(invitees) < 1 || len(invitees) > maxInvites {
		return nil, errors.Invalid(map[string]string{"players": "1_to_20"})
	}
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
	// A match room is its match's players' alone: the tournament asks them.
	if r.MatchID != nil {
		return nil, errTournamentRoom
	}

	rows, err := currentMembers(ctx, s.db, r.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	members := make(map[string]string, len(rows))
	for _, row := range rows {
		members[row.PlayerID] = row.Nickname
	}

	// Each player once, named by where they first appear.
	first := make(map[string]int, len(invitees))
	unique := make([]string, 0, len(invitees))
	for i, id := range invitees {
		if _, seen := first[id]; seen {
			continue
		}
		first[id] = i
		unique = append(unique, id)
	}
	friends := map[string]bool{}
	if s.Friends != nil {
		if friends, err = s.Friends.Among(ctx, playerID, unique); err != nil {
			return nil, asError(err)
		}
	}
	bad := map[string]string{}
	for _, id := range unique {
		field := fmt.Sprintf("players.%d", first[id])
		if _, in := members[id]; in {
			bad[field] = "already_member"
		} else if !friends[id] {
			bad[field] = "not_friends"
		}
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	if s.Inbox == nil {
		return unique, nil
	}

	g, err := gameRow(ctx, s.db, r.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	name := r.GameID
	if g != nil {
		name = g.Name
	}
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		for _, id := range unique {
			_, err := s.Inbox.Raise(ctx, inbox.Notice{
				PlayerID: id, Kind: enum.NoticeRoomInvite, ActorID: playerID, GameID: r.GameID,
				Title: inviteTitle, Body: fmt.Sprintf(inviteBody, members[playerID], name),
				DeepLink: "/r/" + r.Code,
				Data:     map[string]any{"room": r.ID, "code": r.Code, "game": r.GameID},
				Key:      "room_invite:" + r.ID,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, asError(err)
	}
	return unique, nil
}
