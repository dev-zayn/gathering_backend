package admin

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// identityOpened is what a sign-in answers with, named here so the two ways
// in share one response.
type identityOpened = identity.Opened

type meResponse struct {
	Body adminJSON
}

type adminRowJSON struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	CreatedAt *string `json:"created_at"`
	SeenAt    *string `json:"seen_at" doc:"Their last sign-in."`
}

type adminsResponse struct {
	Body struct {
		Admins []adminRowJSON `json:"admins"`
	}
}

func (h *Handler) registerAdmin(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}

	huma.Register(api, huma.Operation{
		OperationID: "admin-me", Method: http.MethodGet, Path: "/v1/admin/me",
		Summary:     "Who is signed in to the console",
		Description: "The admin and the roles their session was opened with.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.me)

	huma.Register(api, huma.Operation{
		OperationID: "admin-list", Method: http.MethodGet, Path: "/v1/admin/admins",
		Summary:     "Everybody who has signed in to the console",
		Description: "Roles are the provider's and are not kept here; the list says who and when.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.list)
}

func (h *Handler) me(ctx context.Context, _ *struct{}) (*meResponse, error) {
	c := caller.FromContext(ctx)
	a, err := h.Admins.ByID(ctx, c.Subject)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if a == nil {
		return nil, errors.Unauthorized("invalid_token")
	}
	return &meResponse{Body: adminJSON{ID: a.ID, Name: a.Name, Email: a.Email, Roles: nonNil(c.Roles)}}, nil
}

func (h *Handler) list(ctx context.Context, _ *struct{}) (*adminsResponse, error) {
	rows, err := h.Admins.List(ctx)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := &adminsResponse{}
	out.Body.Admins = make([]adminRowJSON, 0, len(rows))
	for _, a := range rows {
		created := a.CreatedAt
		out.Body.Admins = append(out.Body.Admins, adminRowJSON{
			ID: a.ID, Name: a.Name, Email: a.Email, CreatedAt: stamp(&created), SeenAt: stamp(a.SeenAt),
		})
	}
	return out, nil
}
