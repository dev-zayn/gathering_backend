package admin

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/reports"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Reports, as the console sees them. docs/REPORTS.md is the contract.

type adminReportsRequest struct {
	Status string `query:"status" example:"open" doc:"open (the default), resolved, or all (status: unknown)."`
	Cursor string `query:"cursor" doc:"next_cursor from the page before; leave it out for the first page (cursor: invalid)."`
	Limit  int    `query:"limit" default:"50" doc:"1 to 100 (limit: 1_to_100)."`
}

type adminReportsResponse struct {
	Body *reports.ConsoleReports
}

type adminReportPath struct {
	Report string `path:"report" maxLength:"24" example:"rpt_7f3k9d2m8q1x4b6n"`
}

func (h *Handler) registerReports(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-reports", Method: http.MethodGet, Path: "/v1/admin/reports",
		Summary: "What players reported",
		Description: "Newest first, a page at a time. against is how many open reports name that " +
			"player; open is how many are open across the platform, whatever the filters.",
		Tags: []string{"admin"}, Metadata: map[string]any{authn.ScopeKey: caller.ScopeAdmin},
	}, h.reports)

	huma.Register(api, huma.Operation{
		OperationID: "admin-report-resolve", Method: http.MethodPost, Path: "/v1/admin/reports/{report}/resolve",
		Summary: "Mark a report dealt with",
		Description: "Records that an admin dealt with it; switching the player off is on the " +
			"player's page. not_found, report_resolved.",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusNoContent,
		Metadata:      map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)},
	}, h.resolveReport)
}

func (h *Handler) reports(ctx context.Context, in *adminReportsRequest) (*adminReportsResponse, error) {
	out, err := h.Reports.List(ctx, in.Status, in.Cursor, in.Limit)
	if err != nil {
		return nil, err
	}
	return &adminReportsResponse{Body: out}, nil
}

func (h *Handler) resolveReport(ctx context.Context, in *adminReportPath) (*noContent, error) {
	return nil, h.Reports.Resolve(ctx, caller.FromContext(ctx).Subject, in.Report)
}
