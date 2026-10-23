package admin

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// The audit log, read back: every area in one table, a page at a time. Which
// area is a filter here because the console is the one reader of them all.
type auditRequest struct {
	Area    string `query:"area" enum:"auth,players,games,rooms,tournaments,admin" doc:"One area of the log. Empty is all of them."`
	Actor   string `query:"actor" doc:"Only this admin's lines."`
	Action  string `query:"action" example:"profile.edited"`
	Subject string `query:"subject" example:"ply_5pt1kx8vqzr3nayd" doc:"Only what was done to this player, game or room."`
	Search  string `query:"q" doc:"A fragment of anything on the line — an id, a name, a word from a reason."`
	Page    int    `query:"page" default:"1" minimum:"1"`
	Limit   int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type auditJSON struct {
	ID      string         `json:"id"`
	At      string         `json:"at"`
	Area    string         `json:"area" enum:"auth,players,games,rooms,tournaments,admin"`
	Actor   string         `json:"actor" doc:"The admin's id. /v1/admin/admins names them."`
	Action  string         `json:"action"`
	Subject string         `json:"subject" doc:"What it was done to."`
	Detail  map[string]any `json:"detail"`
}

type auditResponse struct {
	Body struct {
		Entries []auditJSON `json:"entries"`

		// Total is how many lines match in the area asked for, so a page number
		// means something. Counts is the same per area, whatever was asked for.
		Total  int            `json:"total"`
		Counts map[string]int `json:"counts"`
		Page   int            `json:"page"`
		Limit  int            `json:"limit"`
	}
}

func (h *Handler) registerAudit(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-audit",
		Method:      http.MethodGet,
		Path:        "/v1/admin/audit",
		Summary:     "Every decision, on the record",
		Description: "Newest first. Every sign-in, edit, publish and switch, with who did " +
			"it. Append-only: there is nothing here to correct. One area or all of " +
			"them, a page at a time, with a count per area.",
		Tags:     []string{"admin"},
		Metadata: map[string]any{authn.ScopeKey: caller.ScopeAdmin},
	}, h.readAudit)
}

func (h *Handler) readAudit(ctx context.Context, in *auditRequest) (*auditResponse, error) {
	q := model.Query{
		Area: in.Area, Actor: in.Actor, Action: in.Action, Subject: in.Subject, Search: in.Search,
		Offset: (in.Page - 1) * in.Limit, Limit: in.Limit,
	}

	entries, err := h.Audit.List(ctx, q)
	if err != nil {
		return nil, err
	}
	counts, err := h.Audit.Counts(ctx, q)
	if err != nil {
		return nil, err
	}

	out := &auditResponse{}
	out.Body.Entries = make([]auditJSON, 0, len(entries))
	for _, e := range entries {
		detail := e.Detail
		if detail == nil {
			detail = map[string]any{}
		}
		out.Body.Entries = append(out.Body.Entries, auditJSON{
			ID:      e.ID,
			At:      e.At.UTC().Format("2006-01-02T15:04:05Z"),
			Area:    e.Area,
			Actor:   e.Actor,
			Action:  e.Action,
			Subject: e.Subject,
			Detail:  detail,
		})
	}

	// Every area is named even at zero, so the console never reads a missing
	// key as a missing tab.
	out.Body.Counts = map[string]int{}
	for _, a := range audit.Areas {
		out.Body.Counts[a] = counts[a]
	}
	if in.Area != "" {
		out.Body.Total = counts[in.Area]
	} else {
		for _, n := range counts {
			out.Body.Total += n
		}
	}
	out.Body.Page = in.Page
	out.Body.Limit = in.Limit
	return out, nil
}
