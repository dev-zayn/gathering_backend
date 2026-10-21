package admin

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

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

type playerResponse struct {
	Body playerRowJSON
}

func (h *Handler) registerPlayers(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: "admin"}

	huma.Register(api, huma.Operation{
		OperationID: "admin-players", Method: http.MethodGet, Path: "/v1/admin/players",
		Summary:     "Players",
		Description: "By the start of a handle, or the newest without one. Presence is read from Redis as the list is built.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.players)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player", Method: http.MethodGet, Path: "/v1/admin/players/{player}",
		Summary: "One player", Tags: []string{"admin"}, Metadata: admin,
	}, h.player)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player-disable", Method: http.MethodPost, Path: "/v1/admin/players/{player}/disable",
		Summary:     "Switch a player off",
		Description: "They cannot sign in again and are not found by anybody. Their session ends at its next refresh.",
		Tags:        []string{"admin"}, Metadata: write, DefaultStatus: http.StatusNoContent,
	}, h.disable)

	huma.Register(api, huma.Operation{
		OperationID: "admin-player-enable", Method: http.MethodPost, Path: "/v1/admin/players/{player}/enable",
		Summary: "Switch a player back on", Tags: []string{"admin"}, Metadata: write, DefaultStatus: http.StatusNoContent,
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
	rows, err := h.Players.Search(ctx, q, in.Limit)
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
	return &playerResponse{Body: h.row(ctx, p)}, nil
}

func (h *Handler) disable(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.setDisabled(ctx, in.Player, true)
}

func (h *Handler) enable(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.setDisabled(ctx, in.Player, false)
}

func (h *Handler) setDisabled(ctx context.Context, id string, disabled bool) error {
	p, err := h.Players.ByID(ctx, id)
	if err != nil {
		return errors.Internal(err)
	}
	if p == nil {
		return errors.NotFound("not_found")
	}
	if err := h.Players.SetDisabled(ctx, id, disabled); err != nil {
		return errors.Internal(err)
	}
	return nil
}
