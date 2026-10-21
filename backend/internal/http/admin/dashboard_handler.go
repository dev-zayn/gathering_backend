package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

type dashboardResponse struct {
	Body struct {
		Players   int64 `json:"players" doc:"Everybody who may sign in."`
		NewToday  int64 `json:"new_today" doc:"Signed up since midnight UTC."`
		Active    int64 `json:"active" doc:"Used the app in the last 7 days."`
		Online    int   `json:"online" doc:"Here this minute."`
		Games     int64 `json:"games" doc:"Published games. Arrives with the registry."`
		RoomsLive int64 `json:"rooms_live" doc:"Rooms being played right now. Arrives with the rooms."`
	}
}

func (h *Handler) registerDashboard(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-dashboard", Method: http.MethodGet,
		Path: "/v1/admin/dashboard", Summary: "The first page's numbers",
		Tags:     []string{"admin"},
		Metadata: map[string]any{authn.ScopeKey: caller.ScopeAdmin},
	}, h.dashboard)
}

func (h *Handler) dashboard(ctx context.Context, _ *struct{}) (*dashboardResponse, error) {
	o, err := h.Dashboard.Overview(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	out := &dashboardResponse{}
	out.Body.Players, out.Body.NewToday, out.Body.Active, out.Body.Online = o.Players, o.NewToday, o.Active, o.Online
	out.Body.Games, out.Body.RoomsLive = o.Games, o.RoomsLive
	return out, nil
}
