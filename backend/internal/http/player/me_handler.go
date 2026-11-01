package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// The player's own record.

type meResponse struct {
	Body meJSON
}

type meUpdateRequest struct {
	Body struct {
		Nickname *string `json:"nickname,omitempty" minLength:"2" maxLength:"20"`
		Handle   *string `json:"handle,omitempty" minLength:"3" maxLength:"20" doc:"Lowercase letters, digits and _. Once changed, held for a week."`
		Bio      *string `json:"bio,omitempty" maxLength:"120"`
		Avatar   *string `json:"avatar,omitempty" maxLength:"32" doc:"A bundled avatar id."`
		Language *string `json:"language,omitempty" enum:"ar,en"`
	}
}

func (h *Handler) registerMe(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "me", Method: http.MethodGet, Path: "/v1/me",
		Summary:     "Who is signed in",
		Description: "The player's own record. Read it after sign-in and on every return to the foreground.",
		Tags:        []string{"me"}, Metadata: session,
	}, h.me)

	huma.Register(api, huma.Operation{
		OperationID: "me-update", Method: http.MethodPatch, Path: "/v1/me",
		Summary: "Edit the record",
		Description: "Any of the fields, alone or together. Every problem comes back at once as " +
			"invalid_fields; a taken @name is handle_taken, one changed too recently is handle_cooldown.",
		Tags: []string{"me"}, Metadata: session,
	}, h.update)

	huma.Register(api, huma.Operation{
		OperationID: "me-presence", Method: http.MethodPut, Path: "/v1/me/presence",
		Summary: "I am here",
		Description: "Sent while the app is in the foreground, about every 45 seconds. Friends " +
			"see the player as online for a minute after the last one; a room's own " +
			"connection outranks it.",
		Tags: []string{"me"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.presence)

	huma.Register(api, huma.Operation{
		OperationID: "me-delete", Method: http.MethodDelete, Path: "/v1/me",
		Summary: "Delete my account",
		Description: "Everything that is the player's goes: how they sign in, their friends, " +
			"their inbox, their phones and their library; every session ends. The games " +
			"already played keep a seat for them, emptied (docs/ACCOUNTS.md). Refuses " +
			"409 in_room (leave the room first) and 409 in_tournament (an open or running " +
			"one they organize or are still in).",
		Tags: []string{"me"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.deleteMe)
}

func (h *Handler) deleteMe(ctx context.Context, _ *struct{}) (*noContent, error) {
	return nil, h.Deletion.Do(ctx, caller.FromContext(ctx).Subject)
}

func (h *Handler) me(ctx context.Context, _ *struct{}) (*meResponse, error) {
	claims := caller.FromContext(ctx)
	p, err := h.Profiles.Me(ctx, claims.Subject)
	if err != nil {
		return nil, err
	}
	return &meResponse{Body: me(p)}, nil
}

func (h *Handler) update(ctx context.Context, in *meUpdateRequest) (*meResponse, error) {
	claims := caller.FromContext(ctx)
	p, err := h.Profiles.Edit(ctx, claims.Subject, players.Update{
		Nickname: in.Body.Nickname,
		Handle:   in.Body.Handle,
		Bio:      in.Body.Bio,
		Avatar:   in.Body.Avatar,
		Language: in.Body.Language,
	})
	if err != nil {
		return nil, err
	}
	return &meResponse{Body: me(p)}, nil
}

func (h *Handler) presence(ctx context.Context, _ *struct{}) (*noContent, error) {
	claims := caller.FromContext(ctx)
	return nil, h.Presence.Heartbeat(ctx, claims.Subject)
}
