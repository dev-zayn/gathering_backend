package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// The identity provider's token is the credential here, once: it is checked
// and traded for a session of ours, and never sent again.
type openSessionRequest struct {
	Authorization string `header:"Authorization" doc:"Bearer, then the access token the provider gave the console."`
	Origin        string `header:"Origin"`
}

// The session goes in a cookie and nowhere else: not in the body, where the
// page's scripts could read it.
type openSessionResponse struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		ExpiresAt time.Time `json:"expires_at" doc:"The latest it lasts. Unused for the idle time, it ends sooner."`
		Admin     adminJSON `json:"admin"`
	}
}

type devSessionRequest struct {
	Origin string `header:"Origin"`
	Body   struct {
		Name string `json:"name" required:"true" minLength:"1" maxLength:"60"`
	}
}

type passwordSessionRequest struct {
	Origin string `header:"Origin"`
	Body   struct {
		Name     string `json:"name,omitempty" doc:"Who is signing in: the name the activity log gives them, up to 120 characters (name: required / too_long)."`
		Password string `json:"password,omitempty" doc:"The console's password (password: required; 401 wrong_password; 429 after 20 tries in 15 minutes, everybody's together)."`
	}
}

type closeSessionResponse struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type endSessionsRequest struct {
	Admin string `path:"admin" example:"adm_7f3k9d2m8q1x4b6n"`
}

func (h *Handler) registerSession(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-session-open",
		Method:      http.MethodPost,
		Path:        "/v1/admin/session",
		Summary:     "Start a console session from a provider sign-in",
		Description: "Takes the access token the identity provider issued and answers with a " +
			"session of ours, in an HttpOnly cookie. Only roles prefixed with this service's " +
			"name count: a token with none of them is a 403, however genuine. Only the " +
			"console's own origin may ask. The session ends on sign-out, after the idle " +
			"time unused, when the browser closes, or at expires_at.",
		Tags: []string{"admin"},
	}, h.openSession)

	huma.Register(api, huma.Operation{
		OperationID: "admin-session-dev",
		Method:      http.MethodPost,
		Path:        "/v1/admin/session/dev",
		Summary:     "A laptop's sign-in",
		Description: "Mints an admin session with no provider at all. Exists only where the api " +
			"runs with DEBUG and DEV_ADMIN; anywhere else it is not found.",
		Tags: []string{"admin"},
	}, h.openDev)

	huma.Register(api, huma.Operation{
		OperationID: "admin-session-password",
		Method:      http.MethodPost,
		Path:        "/v1/admin/session/password",
		Summary:     "Sign in with the console's password",
		Description: "For a console with no identity provider: the one password set in " +
			"CONSOLE_PASSWORD_HASH, and a name to be known by, give an admin session in the " +
			"same cookie a provider sign-in does. Refused, in this order: another origin 403 " +
			"bad_origin; no password set 404 not_found; 422 invalid_fields (name, password); " +
			"429 too_many_requests after 20 tries in 15 minutes; 401 wrong_password.",
		Tags: []string{"admin"},
	}, h.openPassword)

	huma.Register(api, huma.Operation{
		OperationID:   "admin-session-close",
		Method:        http.MethodDelete,
		Path:          "/v1/admin/session",
		Summary:       "Sign out",
		Description:   "Ends this session at once, for every tab holding it.",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusNoContent,
		Metadata:      map[string]any{authn.ScopeKey: caller.ScopeAdmin},
	}, h.closeSession)

	huma.Register(api, huma.Operation{
		OperationID: "admin-sessions-end",
		Method:      http.MethodDelete,
		Path:        "/v1/admin/admins/{admin}/sessions",
		Summary:     "Sign somebody out everywhere",
		Description: "Ends every console session one admin holds, at once. Roles are read at " +
			"sign-in, so this is how taking a role away at the provider reaches a session " +
			"already open. Admins only; written to the log.",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusNoContent,
		Metadata:      map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: "admin"},
	}, h.endSessions)
}

func (h *Handler) openSession(ctx context.Context, in *openSessionRequest) (*openSessionResponse, error) {
	// A page on another site must not be able to sign this browser in, as
	// itself or as anybody: the cookie it would leave is one we then trust.
	if in.Origin == "" || !h.Cookie.From(in.Origin, http.MethodPost) {
		return nil, errors.Forbidden("bad_origin")
	}
	raw, ok := token.Bearer(in.Authorization)
	if !ok {
		return nil, errors.Unauthorized("missing_token")
	}
	opened, err := h.Console.Open(ctx, raw)
	if err != nil {
		return nil, err
	}
	return h.opened(opened), nil
}

func (h *Handler) openDev(ctx context.Context, in *devSessionRequest) (*openSessionResponse, error) {
	if in.Origin == "" || !h.Cookie.From(in.Origin, http.MethodPost) {
		return nil, errors.Forbidden("bad_origin")
	}
	opened, err := h.Console.OpenDev(ctx, in.Body.Name)
	if err != nil {
		return nil, err
	}
	return h.opened(opened), nil
}

func (h *Handler) openPassword(ctx context.Context, in *passwordSessionRequest) (*openSessionResponse, error) {
	if in.Origin == "" || !h.Cookie.From(in.Origin, http.MethodPost) {
		return nil, errors.Forbidden("bad_origin")
	}
	opened, err := h.Console.OpenPassword(ctx, in.Body.Name, in.Body.Password)
	if err != nil {
		return nil, err
	}
	return h.opened(opened), nil
}

func (h *Handler) opened(o *identityOpened) *openSessionResponse {
	out := &openSessionResponse{SetCookie: h.Cookie.Issue(o.Token)}
	out.Body.ExpiresAt = o.ExpiresAt.UTC()
	out.Body.Admin = adminJSON{
		ID: o.Admin.ID, Name: o.Admin.Name, Email: o.Admin.Email, Roles: nonNil(o.Roles),
	}
	return out
}

func (h *Handler) closeSession(ctx context.Context, _ *struct{}) (*closeSessionResponse, error) {
	if err := h.Console.Close(ctx, caller.FromContext(ctx).ID); err != nil {
		return nil, err
	}
	return &closeSessionResponse{SetCookie: h.Cookie.Clear()}, nil
}

func (h *Handler) endSessions(ctx context.Context, in *endSessionsRequest) (*noContent, error) {
	return nil, h.Console.EndAll(ctx, caller.FromContext(ctx).Subject, in.Admin)
}
