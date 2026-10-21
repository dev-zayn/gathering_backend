package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// The phone this session runs on, so an invite can ring it.

type deviceRequest struct {
	Body struct {
		Token    string `json:"token" required:"true" maxLength:"4096" doc:"The Firebase registration token, as the Firebase SDK gives it."`
		Platform string `json:"platform" required:"true" enum:"ios,android"`
	}
}

type deviceResponse struct {
	Body struct {
		ID       string `json:"id" example:"dev_4kx8dmzq7v2urybe"`
		Platform string `json:"platform"`
	}
}

func (h *Handler) registerDevice(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "device-set", Method: http.MethodPut,
		Path: "/v1/me/device", Summary: "Push to this phone",
		Description: "Send it after signing in, and again whenever Firebase hands the app a new " +
			"token. One phone per session; the same token on another session moves here. " +
			"Signing out stops the pushes by itself.",
		Tags: []string{"me"}, Metadata: session,
	}, h.setDevice)

	huma.Register(api, huma.Operation{
		OperationID: "device-forget", Method: http.MethodDelete,
		Path: "/v1/me/device", Summary: "Stop pushing to this phone",
		Description: "For a player who turned notifications off in the app. The inbox still fills.",
		Tags:        []string{"me"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.forgetDevice)
}

func (h *Handler) setDevice(ctx context.Context, in *deviceRequest) (*deviceResponse, error) {
	claims := caller.FromContext(ctx)
	d, err := h.Devices.Register(ctx, claims.Session, claims.Subject, enum.Platform(in.Body.Platform), in.Body.Token)
	if err != nil {
		return nil, err
	}
	out := &deviceResponse{}
	out.Body.ID, out.Body.Platform = d.ID, string(d.Platform)
	return out, nil
}

func (h *Handler) forgetDevice(ctx context.Context, _ *struct{}) (*noContent, error) {
	return nil, h.Devices.Forget(ctx, caller.FromContext(ctx).Session)
}
