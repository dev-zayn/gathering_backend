package rooms

import (
	"context"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Room is a room as a member reads it: over HTTP, in hello, and nowhere else
// in any other shape. docs/ROOMS.md shows it.
type Room struct {
	ID           string      `json:"id" example:"room_7f3k9d2m8q1x4b6n"`
	Code         string      `json:"code" example:"K7M2QX" doc:"Six symbols, upper case. What a friend types to join."`
	Game         Game        `json:"game"`
	Status       string      `json:"status" enum:"lobby,playing,ended" doc:"lobby, playing a round, back in the lobby after every round, ended when the room closes."`
	Round        int         `json:"round" doc:"How many rounds have started: 0 in a fresh lobby, the round being played, or the round just finished."`
	Visibility   string      `json:"visibility" enum:"private,friends"`
	LAN          bool        `json:"lan" doc:"The host asked for a Wi-Fi relay."`
	Host         string      `json:"host" example:"ply_7f3k9d2m8q1x4b6n"`
	Config       RoomConfig  `json:"config" doc:"The game's room block as it stood when the room was made."`
	Members      []Member    `json:"members" doc:"The current members, by seat."`
	You          *You        `json:"you" doc:"The caller's own role and seat; null for a caller who has left a room they can still read."`
	StateVersion int         `json:"state_version" doc:"The shared state's version: the live one while the room is open, the last one once it has ended."`
	Link         string      `json:"link" doc:"The invite link, LINK_PUBLIC_URL/r/CODE: the app opens it where it is installed, and a page with the game and the code answers everywhere else."`
	EndReason    *string     `json:"end_reason" nullable:"true" enum:"host,empty,idle,timeout,admin,tournament" doc:"Why the room ended: its host, the last member leaving, an idle lobby, a round past its limit, an admin from the console, or its tournament match decided."`
	Results      []Result    `json:"results" doc:"The results of round round, ranked, ties in seat order: the host's finish or end, or the game's own server. Empty until there are any."`
	Tournament   *MatchBrief `json:"tournament" doc:"The tournament match this room plays (docs/TOURNAMENTS.md), with its tally as it stands; null for every other room."`
	CreatedAt    string      `json:"created_at"`
	StartedAt    *string     `json:"started_at" nullable:"true" doc:"The current round's start, or the last one's."`
	EndedAt      *string     `json:"ended_at" nullable:"true" doc:"The room's own end."`
}

// Game is as much of the game as the lobby draws.
type Game struct {
	ID          string `json:"id" example:"fixture.xo"`
	Name        string `json:"name" example:"إكس أو"`
	Icon        string `json:"icon" doc:"A link that expires; empty when the game has no tile yet."`
	Orientation string `json:"orientation" enum:"portrait,landscape"`
}

// RoomConfig is the room block, flat.
type RoomConfig struct {
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Host       string `json:"host" enum:"authoritative,peer"`
	Pace       string `json:"pace" enum:"turn,realtime"`
	State      string `json:"state" enum:"blob,none"`
	Rejoin     bool   `json:"rejoin"`
	Spectators bool   `json:"spectators"`
	LAN        bool   `json:"lan"`
}

// You is the caller's place in the room.
type You struct {
	Role string `json:"role" enum:"host,player,spectator"`
	Seat int    `json:"seat"`
}

// Member is one current member, as every other member sees them.
type Member struct {
	ID        string  `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname  string  `json:"nickname" example:"زين"`
	Handle    string  `json:"handle" example:"zayn_4821"`
	Avatar    string  `json:"avatar" doc:"A bundled avatar's id, or empty."`
	AvatarURL *string `json:"avatar_url" nullable:"true" doc:"A link that expires, when the player uploaded a picture."`
	Role      string  `json:"role" enum:"host,player,spectator"`
	Seat      int     `json:"seat"`
	Connected bool    `json:"connected" doc:"The member has a live socket to this room."`
}

// configOf flattens the stored block.
func configOf(b model.RoomBlock) RoomConfig {
	return RoomConfig{
		Min: b.Players.Min, Max: b.Players.Max,
		Host: string(b.Host), Pace: string(b.Pace), State: string(b.State),
		Rejoin: b.Rejoin, Spectators: b.Spectators, LAN: b.LAN,
	}
}

// view is the room as viewer reads it. Everything a client needs to draw the
// lobby is here, so one read after any change is enough.
func (s *Service) view(ctx context.Context, r *model.Room, viewer string) (*Room, error) {
	g, err := gameRow(ctx, s.db, r.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	members, err := s.members(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	m, err := membership(ctx, s.db, r.ID, viewer)
	if err != nil {
		return nil, errors.Internal(err)
	}
	results, err := s.resultsOf(ctx, r)
	if err != nil {
		return nil, err
	}

	out := &Room{
		ID:           r.ID,
		Code:         r.Code,
		Game:         s.gameCard(ctx, g, r.GameID),
		Status:       string(r.Status),
		Round:        r.Round,
		Visibility:   string(r.Visibility),
		LAN:          r.LAN,
		Host:         r.HostID,
		Config:       configOf(r.Config),
		Members:      members,
		StateVersion: r.StateVersion,
		Link:         s.cfg.LinkURL + "/r/" + r.Code,
		Results:      results,
		Tournament:   s.matchOf(ctx, r),
		CreatedAt:    stamp(r.CreatedAt),
	}
	if m != nil && m.Current() {
		out.You = &You{Role: string(m.Role), Seat: m.Seat}
	}
	if r.Open() {
		// The live version is the relay's until the end copies it here.
		if st, ok := s.liveState(ctx, r.ID); ok {
			out.StateVersion = st.Version
		}
	}
	if r.EndReason != nil {
		reason := string(*r.EndReason)
		out.EndReason = &reason
	}
	if r.StartedAt != nil {
		at := stamp(*r.StartedAt)
		out.StartedAt = &at
	}
	if r.EndedAt != nil {
		at := stamp(*r.EndedAt)
		out.EndedAt = &at
	}
	return out, nil
}

// members is the current members, by seat, each with whether they have a
// socket open to this room right now.
func (s *Service) members(ctx context.Context, roomID string) ([]Member, error) {
	rows, err := currentMembers(ctx, s.db, roomID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	connected := s.connected(ctx, roomID, rows)

	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		m := Member{
			ID: r.PlayerID, Nickname: r.Nickname, Handle: r.Handle,
			Role: string(r.Role), Seat: r.Seat, Connected: connected[r.PlayerID],
		}
		m.Avatar, m.AvatarURL = s.picture(ctx, r.PlayerID, r.AvatarKey)
		out = append(out, m)
	}
	return out, nil
}

// gameCard is as much of a game as a lobby, a result sheet or a history line
// draws. A game whose row is gone is its id alone.
func (s *Service) gameCard(ctx context.Context, g *model.Game, id string) Game {
	if g == nil {
		return Game{ID: id}
	}
	return Game{ID: g.ID, Name: g.Name, Icon: games.IconURL(ctx, s.Files, g), Orientation: string(g.Orientation)}
}

// connected reads presence once for the whole list: a member is connected
// when their presence says in_room with this room. Presence that cannot be
// read is nobody connected, not a failed answer.
func (s *Service) connected(ctx context.Context, roomID string, rows []memberRow) map[string]bool {
	out := make(map[string]bool, len(rows))
	if s.Presence == nil || len(rows) == 0 {
		return out
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.PlayerID
	}
	states, err := s.Presence.Of(ctx, ids)
	if err != nil {
		applog.From(ctx).Warn("presence not read", "room", roomID, "err", err)
		return out
	}
	for id, st := range states {
		out[id] = st.State == players.InRoom && st.Room == roomID
	}
	return out
}

// picture is the one rule for a player's avatar (players.Picture): a
// bundled avatar's id, or a signed link to an uploaded picture, or neither.
func (s *Service) picture(ctx context.Context, playerID, key string) (avatar string, link *string) {
	return players.Picture(ctx, s.Files, s.cfg.AvatarTTL, playerID, key)
}
