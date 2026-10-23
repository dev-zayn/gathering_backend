package rooms

// What the relay asks of the rules: who may open a socket, what hello says,
// who may write the state, and that a room is still in use. The relay holds
// no rule of its own about membership; it asks here.

import (
	"context"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Admit is the socket's door, asked before the upgrade so a refusal is a
// plain HTTP answer: no such room or not a current member (404), kicked from
// it (403), or the room is over (409).
func (s *Service) Admit(ctx context.Context, roomID, playerID string) error {
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return errors.Internal(err)
	}
	if r == nil {
		return errNotFound
	}
	m, err := membership(ctx, s.db, r.ID, playerID)
	if err != nil {
		return errors.Internal(err)
	}
	switch {
	case m == nil:
		return errNotFound
	case m.Kicked:
		return errKicked
	case !r.Open():
		return errRoomClosed
	case !m.Current():
		return errNotFound
	}
	return nil
}

// View is the room as one member sees it, for hello: You is nil once they
// are no longer in it.
func (s *Service) View(ctx context.Context, roomID, playerID string) (*Room, error) {
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if r == nil {
		return nil, errNotFound
	}
	return s.view(ctx, r, playerID)
}

// MemberList is the members frame's list: the current members, by seat,
// each with whether they are connected.
func (s *Service) MemberList(ctx context.Context, roomID string) ([]Member, error) {
	return s.members(ctx, roomID)
}

// Standing is one member's place in a room at the moment a frame arrives:
// whether the room is open, whether they are still in it (and if not, how
// they went), whether they are its host, and the room's shape.
type Standing struct {
	Open    bool
	Current bool
	Kicked  bool
	Host    bool
	Config  model.RoomBlock
}

// Standing answers for one player. A room that does not exist is closed,
// with nobody in it.
func (s *Service) Standing(ctx context.Context, roomID, playerID string) (Standing, error) {
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return Standing{}, errors.Internal(err)
	}
	if r == nil {
		return Standing{}, nil
	}
	out := Standing{Open: r.Open(), Host: r.HostID == playerID, Config: r.Config}
	m, err := membership(ctx, s.db, r.ID, playerID)
	if err != nil {
		return Standing{}, errors.Internal(err)
	}
	if m != nil {
		out.Current, out.Kicked = m.Current(), m.Kicked
	}
	if !out.Current {
		out.Host = false
	}
	return out, nil
}

// Touch moves active_at: somebody is using the room. The relay calls it on
// connect and, at most once a minute, on pings. An ended room stays as it
// ended.
func (s *Service) Touch(ctx context.Context, roomID string) error {
	err := data.Conn(ctx, s.db).Model(&model.Room{}).
		Where("id = ? AND status <> ?", roomID, enum.RoomEnded).
		Update("active_at", data.Now()).Error
	if err != nil {
		return errors.Internal(err)
	}
	return nil
}

// Brief is a room as a game's server is told about it through the bridge.
type Brief struct {
	ID      string
	Code    string
	Status  string
	Round   int
	Role    string
	Seat    int
	Members []BriefMember

	// Tournament is the match the room plays, nil for any other room.
	Tournament *MatchBrief
}

// BriefMember is one member as the bridge lists them. The nickname is the
// bridge's to hand over or withhold, by scope.
type BriefMember struct {
	ID       string
	Nickname string
	Role     string
	Seat     int
}

// Brief is the room a game token names, when its player is still a current
// member of it; nil otherwise.
func (s *Service) Brief(ctx context.Context, roomID, playerID string) (*Brief, error) {
	if roomID == "" {
		return nil, nil
	}
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if r == nil {
		return nil, nil
	}
	m, err := membership(ctx, s.db, r.ID, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if m == nil || !m.Current() {
		return nil, nil
	}
	rows, err := currentMembers(ctx, s.db, r.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := &Brief{
		ID: r.ID, Code: r.Code, Status: string(r.Status), Round: r.Round, Role: string(m.Role), Seat: m.Seat,
		Tournament: s.matchOf(ctx, r),
	}
	out.Members = make([]BriefMember, 0, len(rows))
	for _, row := range rows {
		out.Members = append(out.Members, BriefMember{ID: row.PlayerID, Nickname: row.Nickname, Role: string(row.Role), Seat: row.Seat})
	}
	return out, nil
}

// errAnotherGame is a launch into a room of another game.
var errAnotherGame = errors.Invalid(map[string]string{"room": "another_game"})

// CheckLaunch is the room rule on opening a game into a room: the room is
// open, the caller is in it, and it is this game's.
func (s *Service) CheckLaunch(ctx context.Context, roomID, playerID, gameID string) error {
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return errors.Internal(err)
	}
	if r == nil {
		return errNotMember
	}
	m, err := membership(ctx, s.db, r.ID, playerID)
	if err != nil {
		return errors.Internal(err)
	}
	switch {
	case m == nil:
		return errNotMember
	case !r.Open():
		return errRoomClosed
	case !m.Current():
		return errNotMember
	case r.GameID != gameID:
		return errAnotherGame
	}
	return nil
}
