package admin

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/friends"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Players, as the console sees them: the list, one record, and the switch.

type playersRequest struct {
	Q     string `query:"q" maxLength:"24" doc:"The start of a handle."`
	Limit int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type playerRowJSON struct {
	ID        string  `json:"id"`
	Nickname  string  `json:"nickname"`
	Handle    string  `json:"handle"`
	Phone     string  `json:"phone,omitempty"`
	Avatar    string  `json:"avatar"`
	Language  string  `json:"language"`
	Disabled  bool    `json:"disabled"`
	Presence  string  `json:"presence" enum:"online,in_room,offline"`
	CreatedAt *string `json:"created_at"`
	LastSeen  *string `json:"last_seen_at"`
}

type playersResponse struct {
	Body struct {
		Players []playerRowJSON `json:"players"`
	}
}

// playerDetailJSON is one player's record: the row the list draws, and how
// far they have got (docs/CONSOLE.md, "The dashboard and the players").
type playerDetailJSON struct {
	ID        string          `json:"id"`
	Nickname  string          `json:"nickname"`
	Handle    string          `json:"handle"`
	Phone     string          `json:"phone,omitempty"`
	Avatar    string          `json:"avatar"`
	Bio       string          `json:"bio" doc:"What they wrote about themselves: other players read it on their card, so a report may be about it."`
	Language  string          `json:"language"`
	Disabled  bool            `json:"disabled"`
	Presence  string          `json:"presence" enum:"online,in_room,offline"`
	CreatedAt *string         `json:"created_at"`
	LastSeen  *string         `json:"last_seen_at"`
	Friends   int64           `json:"friends" doc:"Accepted friendships."`
	Rounds    int64           `json:"rounds" doc:"Rounds played: their rows in results."`
	Room      *playerRoomJSON `json:"room" doc:"The open room they are in; null when none."`
}

type playerRoomJSON struct {
	ID   string `json:"id" example:"room_7f3k9d2m8q1x4b6n"`
	Code string `json:"code" example:"K7M2QX"`
}

type playerResponse struct {
	Body playerDetailJSON
}

type playerBlockPath struct {
	Player  string `path:"player" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n"`
	Blocked string `path:"blocked" maxLength:"24" example:"ply_2k8d1m4q7x9b3n6f"`
}

type playerBlocksResponse struct {
	Body struct {
		Blocked   []friends.BlockRow `json:"blocked" doc:"Who this player blocked, newest first."`
		BlockedBy []friends.BlockRow `json:"blocked_by" doc:"Who blocked this player, newest first."`
	}
}

func (h *Handler) registerPlayers(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: "admin"}

	huma.Register(api, huma.Operation{
		OperationID: "admin-players", Method: http.MethodGet, Path: "/v1/admin/players",
		Summary:     "Players",
		Description: "By the start of a handle, or the newest without one; switched-off players included. Presence is read from Redis as the list is built.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.players)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player", Method: http.MethodGet, Path: "/v1/admin/players/{player}",
		Summary:     "One player",
		Description: "The list's row, with their friends, the rounds they have played and the open room they are in.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.player)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player-blocks", Method: http.MethodGet, Path: "/v1/admin/players/{player}/blocks",
		Summary:     "A player's blocks",
		Description: "Who they blocked and who blocked them, newest first; switched-off players in both, marked. 404 not_found for nobody or a deleted account.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.playerBlocks)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player-unblock", Method: http.MethodDelete, Path: "/v1/admin/players/{player}/blocks/{blocked}",
		Summary:     "Lift a player's block",
		Description: "Lifts {player}'s block of {blocked}, as their own unblock does: nothing restored, nobody told. 204 also when there was no such block; the audit line block.lifted only when one went. 404 not_found when {player} is nobody or a deleted account.",
		Tags:        []string{"admin"}, Metadata: write, DefaultStatus: http.StatusNoContent,
	}, h.liftBlock)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player-disable", Method: http.MethodPost, Path: "/v1/admin/players/{player}/disable",
		Summary:     "Switch a player off",
		Description: "They cannot sign in again and are not found by anybody. Every session they hold ends now, and so do their games' tokens; they are taken out of an open room, and their socket closes. 404 not_found for nobody or a deleted account.",
		Tags:        []string{"admin"}, Metadata: write, DefaultStatus: http.StatusNoContent,
	}, h.disable)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player-enable", Method: http.MethodPost, Path: "/v1/admin/players/{player}/enable",
		Summary:     "Switch a player back on",
		Description: "They may sign in again; nothing they held comes back. 404 not_found for nobody or a deleted account.",
		Tags:        []string{"admin"}, Metadata: write, DefaultStatus: http.StatusNoContent,
	}, h.enable)
}

func (h *Handler) row(ctx context.Context, p *model.Player) playerRowJSON {
	out := playerRowJSON{
		ID: p.ID, Nickname: p.Nickname, Handle: p.Handle, Avatar: p.AvatarKey,
		Language: string(p.Language), Disabled: p.Disabled, Presence: "offline",
		LastSeen: stamp(p.LastSeenAt),
	}
	created := p.CreatedAt
	out.CreatedAt = stamp(&created)
	if p.Phone != nil {
		out.Phone = *p.Phone
	}
	if h.Presence != nil {
		if st, err := h.Presence.Of(ctx, []string{p.ID}); err == nil {
			out.Presence = st[p.ID].State
		}
	}
	return out
}

func (h *Handler) players(ctx context.Context, in *playersRequest) (*playersResponse, error) {
	q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(in.Q, "@")))
	rows, err := h.Players.List(ctx, q, in.Limit)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := &playersResponse{}
	out.Body.Players = make([]playerRowJSON, 0, len(rows))
	for i := range rows {
		out.Body.Players = append(out.Body.Players, h.row(ctx, &rows[i]))
	}
	return out, nil
}

func (h *Handler) player(ctx context.Context, in *playerPath) (*playerResponse, error) {
	p, err := h.Players.ByID(ctx, in.Player)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if p == nil {
		return nil, errors.NotFound("not_found")
	}
	tally, err := h.Dashboard.Tally(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	row := h.row(ctx, p)
	out := &playerResponse{Body: playerDetailJSON{
		ID: row.ID, Nickname: row.Nickname, Handle: row.Handle, Phone: row.Phone, Avatar: row.Avatar, Bio: p.Bio,
		Language: row.Language, Disabled: row.Disabled, Presence: row.Presence, CreatedAt: row.CreatedAt,
		LastSeen: row.LastSeen, Friends: tally.Friends, Rounds: tally.Rounds,
	}}
	if tally.Room != nil {
		out.Body.Room = &playerRoomJSON{ID: tally.Room.ID, Code: tally.Room.Code}
	}
	return out, nil
}

func (h *Handler) playerBlocks(ctx context.Context, in *playerPath) (*playerBlocksResponse, error) {
	blocked, by, err := h.Friends.Blocks(ctx, in.Player)
	if err != nil {
		return nil, err
	}
	out := &playerBlocksResponse{}
	out.Body.Blocked, out.Body.BlockedBy = blocked, by
	return out, nil
}

func (h *Handler) liftBlock(ctx context.Context, in *playerBlockPath) (*noContent, error) {
	return nil, h.Friends.Lift(ctx, actor(ctx), in.Player, in.Blocked)
}

func (h *Handler) disable(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.Switch.Off(ctx, actor(ctx), in.Player)
}

func (h *Handler) enable(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.Switch.On(ctx, actor(ctx), in.Player)
}
