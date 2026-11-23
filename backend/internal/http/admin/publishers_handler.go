package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// The studios' accounts and the review of what they send
// (docs/PUBLISHERS.md, "The admin's routes").

type publisherAccountJSON struct {
	ID             string  `json:"id" example:"pub_7f3k9d2m8q1x4b6n"`
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	Status         string  `json:"status" enum:"invited,active,disabled" doc:"invited: no password yet."`
	Games          int     `json:"games"`
	CreatedAt      string  `json:"created_at"`
	SeenAt         *string `json:"seen_at" nullable:"true" doc:"The last sign-in."`
	SetupExpiresAt *string `json:"setup_expires_at" nullable:"true" doc:"When the waiting one-time link stops working; null when none waits."`
}

type setupJSON struct {
	Token     string `json:"token" doc:"Shown once. The console makes its link out of it: /setup#<token>."`
	ExpiresAt string `json:"expires_at"`
}

type publisherPath struct {
	Publisher string `path:"publisher" example:"pub_7f3k9d2m8q1x4b6n"`
}

type publishersResponse struct {
	Body struct {
		Items []publisherAccountJSON `json:"items"`
	}
}

type publisherCreateRequest struct {
	Body struct {
		Name  string `json:"name" required:"true" maxLength:"200"`
		Email string `json:"email" required:"true" maxLength:"300"`
	}
}

type publisherCreatedResponse struct {
	Body struct {
		Publisher publisherAccountJSON `json:"publisher"`
		Setup     setupJSON            `json:"setup"`
	}
}

type publisherGameJSON struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Icon        string  `json:"icon"`
	Listing     string  `json:"listing" enum:"published,unpublished,suspended"`
	Version     string  `json:"version"`
	BuildStatus *string `json:"build_status" nullable:"true" enum:"testing,in_review,approved,rejected" doc:"The open build's, or null."`
}

type publisherResponse struct {
	Body struct {
		Publisher publisherAccountJSON `json:"publisher"`
		Games     []publisherGameJSON  `json:"games"`
	}
}

type publisherEditRequest struct {
	Publisher string `path:"publisher"`
	Body      struct {
		Name  *string `json:"name,omitempty" maxLength:"200"`
		Email *string `json:"email,omitempty" maxLength:"300"`
	}
}

type setupResponse struct{ Body setupJSON }

func (h *Handler) registerPublishers(api huma.API) {
	read := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)}

	huma.Register(api, huma.Operation{
		OperationID: "admin-publishers", Method: http.MethodGet, Path: "/v1/admin/publishers",
		Summary: "Every publisher account", Description: "The newest first.",
		Tags: []string{"admin"}, Metadata: read,
	}, h.publishers)
	huma.Register(api, huma.Operation{
		OperationID: "admin-publisher-create", Method: http.MethodPost, Path: "/v1/admin/publishers",
		Summary: "Make a publisher account",
		Description: "With no password: the answer carries a one-time link's token, shown once, that " +
			"the studio chooses its password through. 422 name: required | too_long, email: " +
			"required | invalid | too_long; 409 email_taken.",
		Tags: []string{"admin"}, Metadata: write, DefaultStatus: http.StatusCreated,
	}, h.createPublisher)
	huma.Register(api, huma.Operation{
		OperationID: "admin-publisher", Method: http.MethodGet, Path: "/v1/admin/publishers/{publisher}",
		Summary: "One publisher and its games", Tags: []string{"admin"}, Metadata: read,
	}, h.publisher)
	huma.Register(api, huma.Operation{
		OperationID: "admin-publisher-edit", Method: http.MethodPatch, Path: "/v1/admin/publishers/{publisher}",
		Summary:     "Rename an account, or move its sign-in",
		Description: "A new name reaches its games at their next publish. The same 422s and 409 as making one.",
		Tags:        []string{"admin"}, Metadata: write,
	}, h.editPublisher)
	huma.Register(api, huma.Operation{
		OperationID: "admin-publisher-setup", Method: http.MethodPost, Path: "/v1/admin/publishers/{publisher}/setup",
		Summary: "A new one-time link",
		Description: "For a first sign-in never made, or a forgotten password. It replaces any link " +
			"waiting; the password stays what it was until the link is used.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.newSetup)
	huma.Register(api, huma.Operation{
		OperationID: "admin-publisher-disable", Method: http.MethodPost, Path: "/v1/admin/publishers/{publisher}/disable",
		Summary: "Switch an account off", Description: "Every session it holds ends at once; its games stay as they are.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.disablePublisher)
	huma.Register(api, huma.Operation{
		OperationID: "admin-publisher-enable", Method: http.MethodPost, Path: "/v1/admin/publishers/{publisher}/enable",
		Summary: "Switch an account back on", Tags: []string{"admin"}, Metadata: write,
	}, h.enablePublisher)
}

func (h *Handler) publishers(ctx context.Context, _ *struct{}) (*publishersResponse, error) {
	rows, err := h.Publishers.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &publishersResponse{}
	out.Body.Items = make([]publisherAccountJSON, 0, len(rows))
	for i := range rows {
		out.Body.Items = append(out.Body.Items, accountOf(&rows[i].Publisher, rows[i].Games))
	}
	return out, nil
}

func (h *Handler) createPublisher(ctx context.Context, in *publisherCreateRequest) (*publisherCreatedResponse, error) {
	p, setup, err := h.Publishers.Create(ctx, actor(ctx), in.Body.Name, in.Body.Email)
	if err != nil {
		return nil, err
	}
	out := &publisherCreatedResponse{}
	out.Body.Publisher = accountOf(p, 0)
	out.Body.Setup = setupOf(setup)
	return out, nil
}

func (h *Handler) publisher(ctx context.Context, in *publisherPath) (*publisherResponse, error) {
	row, err := h.Publishers.Get(ctx, in.Publisher)
	if err != nil {
		return nil, err
	}
	owned, err := h.Review.PublisherGames(ctx, in.Publisher)
	if err != nil {
		return nil, err
	}
	out := &publisherResponse{}
	out.Body.Publisher = accountOf(&row.Publisher, row.Games)
	out.Body.Games = make([]publisherGameJSON, 0, len(owned))
	for i := range owned {
		g := &owned[i].Game
		j := publisherGameJSON{
			ID: g.ID, Name: g.Name, Icon: games.IconURL(ctx, h.Files, g), Listing: string(g.Listing), Version: g.Version,
		}
		if b := owned[i].Build; b != nil {
			status := string(b.Status)
			j.BuildStatus = &status
			if g.Listing == enum.Unpublished {
				j.Name, j.Version = b.Name, b.Version
			}
		}
		out.Body.Games = append(out.Body.Games, j)
	}
	return out, nil
}

func (h *Handler) editPublisher(ctx context.Context, in *publisherEditRequest) (*publisherResponse, error) {
	if _, err := h.Publishers.Edit(ctx, actor(ctx), in.Publisher, in.Body.Name, in.Body.Email); err != nil {
		return nil, err
	}
	return h.publisher(ctx, &publisherPath{Publisher: in.Publisher})
}

func (h *Handler) newSetup(ctx context.Context, in *publisherPath) (*setupResponse, error) {
	setup, err := h.Publishers.NewSetup(ctx, actor(ctx), in.Publisher)
	if err != nil {
		return nil, err
	}
	return &setupResponse{Body: setupOf(setup)}, nil
}

func (h *Handler) disablePublisher(ctx context.Context, in *publisherPath) (*publisherResponse, error) {
	if _, err := h.Publishers.Disable(ctx, actor(ctx), in.Publisher); err != nil {
		return nil, err
	}
	return h.publisher(ctx, in)
}

func (h *Handler) enablePublisher(ctx context.Context, in *publisherPath) (*publisherResponse, error) {
	if _, err := h.Publishers.Enable(ctx, actor(ctx), in.Publisher); err != nil {
		return nil, err
	}
	return h.publisher(ctx, in)
}

func accountOf(p *model.Publisher, gameCount int) publisherAccountJSON {
	out := publisherAccountJSON{
		ID: p.ID, Name: p.Name, Email: p.Email, Status: string(p.Status()), Games: gameCount,
		CreatedAt: *stamp(&p.CreatedAt), SeenAt: stamp(p.SeenAt),
	}
	if p.SetupHash != "" && p.SetupExpiresAt != nil && time.Now().Before(*p.SetupExpiresAt) {
		out.SetupExpiresAt = stamp(p.SetupExpiresAt)
	}
	return out
}

func setupOf(s *identity.Setup) setupJSON {
	return setupJSON{Token: s.Token, ExpiresAt: *stamp(&s.ExpiresAt)}
}

// ---- Review ----

type reviewRequest struct {
	Status string `query:"status" enum:"in_review,approved,rejected,all" doc:"in_review when empty."`
}

type reviewOwnerJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type reviewGameJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Icon    string `json:"icon"`
	Listing string `json:"listing" enum:"published,unpublished,suspended"`
	Version string `json:"version" doc:"The live game's; empty before the first publish."`
}

type reviewItemJSON struct {
	Build     games.BuildBrief `json:"build"`
	Game      reviewGameJSON   `json:"game"`
	Publisher *reviewOwnerJSON `json:"publisher"`
}

type reviewResponse struct {
	Body struct {
		Items   []reviewItemJSON `json:"items"`
		Waiting int              `json:"waiting" doc:"How many builds are in review, whatever was asked."`
	}
}

type buildPath struct {
	Build string `path:"build" example:"bld_7f3k9d2m8q1x4b6n"`
}

type reviewBuildResponse struct {
	Body struct {
		Build     games.BuildView    `json:"build"`
		Game      reviewGameJSON     `json:"game"`
		Publisher *reviewOwnerJSON   `json:"publisher"`
		Invites   []games.InviteView `json:"invites" doc:"The game's live codes, to test the build with."`
	}
}

type decisionRequest struct {
	Build string `path:"build"`
	Body  struct {
		Note string `json:"note,omitempty" maxLength:"3000" doc:"Required to reject: what to fix. Up to 2000 characters."`
	}
}

type reviewInviteResponse struct{ Body games.InviteView }

type gameBuildsResponse struct {
	Body struct {
		Builds []games.BuildBrief `json:"builds"`
	}
}

func (h *Handler) registerReview(api huma.API) {
	read := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)}

	huma.Register(api, huma.Operation{
		OperationID: "admin-review", Method: http.MethodGet, Path: "/v1/admin/review",
		Summary: "Builds sent to review",
		Description: "The oldest submission first for those waiting; the newest decision first " +
			"for the rest. At most 100. status: unknown.",
		Tags: []string{"admin"}, Metadata: read,
	}, h.review)
	huma.Register(api, huma.Operation{
		OperationID: "admin-build", Method: http.MethodGet, Path: "/v1/admin/builds/{build}",
		Summary: "One build in full", Description: "With its game, its publisher and the game's live codes.",
		Tags: []string{"admin"}, Metadata: read,
	}, h.reviewBuild)
	huma.Register(api, huma.Operation{
		OperationID: "admin-build-approve", Method: http.MethodPost, Path: "/v1/admin/builds/{build}/approve",
		Summary: "Approve a build", Description: "The publisher publishes it when ready. 409 not_in_review; note: too_long.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.approve)
	huma.Register(api, huma.Operation{
		OperationID: "admin-build-reject", Method: http.MethodPost, Path: "/v1/admin/builds/{build}/reject",
		Summary: "Send a build back", Description: "With a note saying what to fix. 409 not_in_review; note: required | too_long.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.reject)
	huma.Register(api, huma.Operation{
		OperationID: "admin-build-invite", Method: http.MethodPost, Path: "/v1/admin/builds/{build}/invite",
		Summary:     "A code to test the build with",
		Description: "Labelled Review, one use, 7 days; it counts towards the game's 20. invites: too_many.",
		Tags:        []string{"admin"}, Metadata: write, DefaultStatus: http.StatusCreated,
	}, h.reviewInvite)
	huma.Register(api, huma.Operation{
		OperationID: "admin-game-builds", Method: http.MethodGet, Path: "/v1/admin/games/{game}/builds",
		Summary: "Every build of a game", Description: "The newest first.",
		Tags: []string{"admin"}, Metadata: read,
	}, h.gameBuilds)
}

func (h *Handler) review(ctx context.Context, in *reviewRequest) (*reviewResponse, error) {
	rows, waiting, err := h.Review.Queue(ctx, in.Status)
	if err != nil {
		return nil, err
	}
	out := &reviewResponse{}
	out.Body.Waiting = waiting
	out.Body.Items = make([]reviewItemJSON, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out.Body.Items = append(out.Body.Items, reviewItemJSON{
			Build: games.BriefOf(&r.Build), Game: h.reviewGame(ctx, &r.Build, &r.Game), Publisher: ownerOf(r.Publisher),
		})
	}
	return out, nil
}

func (h *Handler) reviewBuild(ctx context.Context, in *buildPath) (*reviewBuildResponse, error) {
	return h.reviewed(ctx)(h.Review.Build(ctx, in.Build))
}

func (h *Handler) approve(ctx context.Context, in *decisionRequest) (*reviewBuildResponse, error) {
	return h.reviewed(ctx)(h.Review.Approve(ctx, actor(ctx), in.Build, in.Body.Note))
}

func (h *Handler) reject(ctx context.Context, in *decisionRequest) (*reviewBuildResponse, error) {
	return h.reviewed(ctx)(h.Review.Reject(ctx, actor(ctx), in.Build, in.Body.Note))
}

func (h *Handler) reviewInvite(ctx context.Context, in *buildPath) (*reviewInviteResponse, error) {
	inv, err := h.Review.Invite(ctx, actor(ctx), in.Build)
	if err != nil {
		return nil, err
	}
	return &reviewInviteResponse{Body: games.ViewInvite(inv)}, nil
}

func (h *Handler) gameBuilds(ctx context.Context, in *gamePath) (*gameBuildsResponse, error) {
	rows, err := h.Review.GameBuilds(ctx, in.Game)
	if err != nil {
		return nil, err
	}
	out := &gameBuildsResponse{}
	out.Body.Builds = make([]games.BuildBrief, 0, len(rows))
	for i := range rows {
		out.Body.Builds = append(out.Body.Builds, games.BriefOf(&rows[i]))
	}
	return out, nil
}

func (h *Handler) reviewed(ctx context.Context) func(*games.ReviewBuild, error) (*reviewBuildResponse, error) {
	return func(r *games.ReviewBuild, err error) (*reviewBuildResponse, error) {
		if err != nil {
			return nil, err
		}
		out := &reviewBuildResponse{}
		out.Body.Build = games.ViewBuild(ctx, h.Files, &r.Build, &r.Game, true)
		out.Body.Game = h.reviewGame(ctx, &r.Build, &r.Game)
		out.Body.Publisher = ownerOf(r.Publisher)
		out.Body.Invites = make([]games.InviteView, 0, len(r.Invites))
		for i := range r.Invites {
			out.Body.Invites = append(out.Body.Invites, games.ViewInvite(&r.Invites[i]))
		}
		return out, nil
	}
}

// reviewGame is the game a build belongs to: its live self, or before the
// first publish the build's name and nothing live.
func (h *Handler) reviewGame(ctx context.Context, b *model.GameBuild, g *model.Game) reviewGameJSON {
	out := reviewGameJSON{ID: g.ID, Name: g.Name, Listing: string(g.Listing)}
	if g.Listing == enum.Unpublished {
		out.Name = b.Name
		over := b.Over(*g)
		out.Icon = games.IconURL(ctx, h.Files, &over)
		return out
	}
	out.Version = g.Version
	out.Icon = games.IconURL(ctx, h.Files, g)
	return out
}

func ownerOf(p *model.Publisher) *reviewOwnerJSON {
	if p == nil {
		return nil
	}
	return &reviewOwnerJSON{ID: p.ID, Name: p.Name}
}
