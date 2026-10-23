package admin

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Announcements: one system notification to many players, sent from the
// console, and the list of what was sent. docs/CONSOLE.md, "Announcements",
// is the contract. Every field is optional on the way in so that what is
// missing comes back in the API's own words with everything else wrong.

type audienceJSON struct {
	Kind    string   `json:"kind,omitempty" example:"players" doc:"all, game or players (audience.kind: unknown)."`
	Game    string   `json:"game,omitempty" example:"fixture.xo" doc:"For kind game: the game whose players hear it (audience.game: not_found)."`
	Players []string `json:"players,omitempty" doc:"For kind players: up to 500 player ids or @handles (audience.players: required / up_to_500 / nobody)."`
}

type announceRequest struct {
	Body struct {
		Audience *audienceJSON `json:"audience,omitempty" doc:"Who hears it (audience: required)."`
		Title    string        `json:"title,omitempty" example:"صيانة الليلة" doc:"1 to 60 characters, trimmed (title: required / too_long)."`
		Body     string        `json:"body,omitempty" doc:"Up to 240 characters, trimmed (body: too_long)."`
		Game     string        `json:"game,omitempty" example:"fixture.xo" doc:"A game the notification opens: its store page in the app (game: not_found)."`
		DryRun   bool          `json:"dry_run,omitempty" doc:"Count who it would reach and write nothing: 200 {reach}."`
	}
}

type announceResponse struct {
	// Status is 200 for a dry run, 201 for a send.
	Status int
	Body   struct {
		ID    string `json:"id,omitempty" example:"ann_7f3k9d2m8q1x4b6n" doc:"The announcement; absent on a dry run."`
		Reach int    `json:"reach" doc:"How many players it reached, or would reach."`
	}
}

type announcementsRequest struct {
	Cursor string `query:"cursor" doc:"next_cursor from the page before; leave it out for the first page (cursor: invalid)."`
	Limit  int    `query:"limit" default:"50" doc:"1 to 100 (limit: 1_to_100)."`
}

type announcementsResponse struct {
	Body *inbox.Announcements
}

func (h *Handler) registerAnnouncements(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)}

	huma.Register(api, huma.Operation{
		OperationID: "admin-notify", Method: http.MethodPost, Path: "/v1/admin/notifications",
		Summary: "Send an announcement",
		Description: "One system notification for every player the audience reaches: everybody, a " +
			"game's players, or up to 500 named by id or @handle (unknown and switched-off players " +
			"skipped). dry_run counts them and writes nothing. A send writes the announcement and " +
			"every notification at once, and the log. Nothing is pushed. Admins only.",
		Tags: []string{"admin"}, Metadata: write, DefaultStatus: http.StatusCreated,
	}, h.announce)

	huma.Register(api, huma.Operation{
		OperationID: "admin-announcements", Method: http.MethodGet, Path: "/v1/admin/notifications",
		Summary:     "The announcements sent",
		Description: "Newest first, a page at a time: hand next_cursor back as cursor until it is null.",
		Tags:        []string{"admin"}, Metadata: admin,
	}, h.announcements)
}

func (h *Handler) announce(ctx context.Context, in *announceRequest) (*announceResponse, error) {
	ask := inbox.Announce{Title: in.Body.Title, Body: in.Body.Body, Game: in.Body.Game, DryRun: in.Body.DryRun}
	if a := in.Body.Audience; a != nil {
		ask.Audience = &inbox.AnnouncementAudience{Kind: a.Kind, Game: a.Game, Players: a.Players}
	}
	sent, err := h.Inbox.Announce(ctx, caller.FromContext(ctx).Subject, ask)
	if err != nil {
		return nil, err
	}
	out := &announceResponse{Status: http.StatusCreated}
	if in.Body.DryRun {
		out.Status = http.StatusOK
	}
	out.Body.ID, out.Body.Reach = sent.ID, sent.Reach
	return out, nil
}

func (h *Handler) announcements(ctx context.Context, in *announcementsRequest) (*announcementsResponse, error) {
	page, err := h.Inbox.Announcements(ctx, in.Cursor, in.Limit)
	if err != nil {
		return nil, err
	}
	return &announcementsResponse{Body: page}, nil
}
