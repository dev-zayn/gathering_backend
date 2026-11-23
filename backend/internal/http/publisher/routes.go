// Package publisher is the studios' routes (docs/PUBLISHERS.md): a third
// audience in the console, with a token of its own kind, so nothing here is
// reachable with an admin's session or a player's, and nothing of theirs with
// this one.
package publisher

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
)

type Handler struct {
	// Publishers is the accounts and their sessions; Cookie is the console's,
	// which a publisher's session travels in as an admin's does.
	Publishers *identity.Publishers
	Cookie     authn.Cookie

	// Studio is the publisher's games, builds and testers; Files signs the
	// links to their pictures, and AvatarTTL a tester's.
	Studio    *games.Studio
	Files     files.Files
	AvatarTTL time.Duration
}

type noContent struct{}

func (h *Handler) Register(api huma.API) {
	h.registerSession(api)
	h.registerGames(api)
	h.registerBuilds(api)
	h.registerTesters(api)
}

// ---- Signing in ----

type publisherJSON struct {
	ID    string `json:"id" example:"pub_7f3k9d2m8q1x4b6n"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type signInRequest struct {
	Origin string `header:"Origin"`
	Body   struct {
		Email    string `json:"email,omitempty" maxLength:"254"`
		Password string `json:"password,omitempty" maxLength:"200"`
	}
}

type setupRequest struct {
	Origin string `header:"Origin"`
	Body   struct {
		Token    string `json:"token" required:"true" maxLength:"200" doc:"The one-time link's token, from /setup#<token>."`
		Password string `json:"password" required:"true" maxLength:"200" doc:"At least 10 characters, at most 72 bytes."`
	}
}

type passwordRequest struct {
	Body struct {
		Current  string `json:"current" required:"true" maxLength:"200"`
		Password string `json:"password" required:"true" maxLength:"200"`
	}
}

type sessionResponse struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		ExpiresAt time.Time     `json:"expires_at"`
		Publisher publisherJSON `json:"publisher"`
	}
}

type closeResponse struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type meResponse struct{ Body publisherJSON }

func (h *Handler) registerSession(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopePublisher}

	huma.Register(api, huma.Operation{
		OperationID: "publisher-session-open", Method: http.MethodPost, Path: "/v1/publisher/session",
		Summary: "A publisher signs in",
		Description: "An email and a password give a publisher's session in the console's cookie. " +
			"Refused, in order: 403 bad_origin; 422 invalid_fields (email, password: required); " +
			"429 after 10 tries in 15 minutes for one email; 401 wrong_password (an unknown " +
			"email, a wrong password, or no password yet); 403 publisher_disabled.",
		Tags: []string{"publisher"},
	}, h.signIn)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-setup", Method: http.MethodPost, Path: "/v1/publisher/setup",
		Summary: "Choose the password with a one-time link",
		Description: "The link an admin made: the password chosen, the link spent, and signed in. " +
			"403 bad_origin; 422 password: too_short | too_long; 401 invalid_setup; " +
			"403 publisher_disabled.",
		Tags: []string{"publisher"},
	}, h.setup)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-password", Method: http.MethodPost, Path: "/v1/publisher/password",
		Summary:     "Change the password",
		Description: "401 wrong_password for the current one; the same 422s as the setup.",
		Tags:        []string{"publisher"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.password)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-session-close", Method: http.MethodDelete, Path: "/v1/publisher/session",
		Summary: "Sign out", Tags: []string{"publisher"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.closeSession)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-me", Method: http.MethodGet, Path: "/v1/publisher/me",
		Summary: "Who is signed in", Tags: []string{"publisher"}, Metadata: session,
	}, h.me)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-categories", Method: http.MethodGet, Path: "/v1/publisher/categories",
		Summary: "How the store groups games", Tags: []string{"publisher"}, Metadata: session,
	}, h.categories)
}

func (h *Handler) signIn(ctx context.Context, in *signInRequest) (*sessionResponse, error) {
	if in.Origin == "" || !h.Cookie.From(in.Origin, http.MethodPost) {
		return nil, errors.Forbidden("bad_origin")
	}
	opened, err := h.Publishers.SignIn(ctx, in.Body.Email, in.Body.Password)
	if err != nil {
		return nil, err
	}
	return h.opened(opened), nil
}

func (h *Handler) setup(ctx context.Context, in *setupRequest) (*sessionResponse, error) {
	if in.Origin == "" || !h.Cookie.From(in.Origin, http.MethodPost) {
		return nil, errors.Forbidden("bad_origin")
	}
	opened, err := h.Publishers.UseSetup(ctx, in.Body.Token, in.Body.Password)
	if err != nil {
		return nil, err
	}
	return h.opened(opened), nil
}

func (h *Handler) opened(o *identity.PublisherOpened) *sessionResponse {
	out := &sessionResponse{SetCookie: h.Cookie.Issue(o.Token)}
	out.Body.ExpiresAt = o.ExpiresAt.UTC()
	out.Body.Publisher = publisherJSON{ID: o.Publisher.ID, Name: o.Publisher.Name, Email: o.Publisher.Email}
	return out
}

func (h *Handler) password(ctx context.Context, in *passwordRequest) (*noContent, error) {
	return nil, h.Publishers.ChangePassword(ctx, caller.FromContext(ctx).Subject, in.Body.Current, in.Body.Password)
}

func (h *Handler) closeSession(ctx context.Context, _ *struct{}) (*closeResponse, error) {
	if err := h.Publishers.Close(ctx, caller.FromContext(ctx).ID); err != nil {
		return nil, err
	}
	return &closeResponse{SetCookie: h.Cookie.Clear()}, nil
}

func (h *Handler) me(ctx context.Context, _ *struct{}) (*meResponse, error) {
	p, err := h.Publishers.Me(ctx, caller.FromContext(ctx).Subject)
	if err != nil {
		return nil, err
	}
	return &meResponse{Body: publisherJSON{ID: p.ID, Name: p.Name, Email: p.Email}}, nil
}

type categoriesResponse struct {
	Body struct {
		Categories []string `json:"categories"`
	}
}

func (h *Handler) categories(_ context.Context, _ *struct{}) (*categoriesResponse, error) {
	out := &categoriesResponse{}
	out.Body.Categories = make([]string, 0, len(enum.Categories))
	for _, c := range enum.Categories {
		out.Body.Categories = append(out.Body.Categories, string(c))
	}
	return out, nil
}

// account is who is asking, as the studio's rules want it: the id from the
// token, the name from the row, so a rename reaches the next publish.
func (h *Handler) account(ctx context.Context) (games.Account, error) {
	p, err := h.Publishers.Me(ctx, caller.FromContext(ctx).Subject)
	if err != nil {
		return games.Account{}, err
	}
	return games.Account{ID: p.ID, Name: p.Name}, nil
}

// ---- Games ----

type gamePath struct {
	Game string `path:"game" example:"yourstudio.game"`
}

type createGameRequest struct {
	Body struct {
		URL string `json:"url" required:"true" maxLength:"2048" example:"https://games.yourstudio.example/mygame/" doc:"The manifest's address, or the game's origin: /.well-known/gathering-game.json is appended to an origin or anything ending in a slash."`
	}
}

type gameResponse struct{ Body games.StudioGameView }

type createdResponse struct {
	Body struct {
		Game   games.StudioGameView `json:"game"`
		Secret string               `json:"client_secret" doc:"Shown once. Store it: there is no way to read it back, only to roll it."`
	}
}

type gamesResponse struct {
	Body struct {
		Items []games.StudioGameView `json:"items"`
	}
}

type secretResponse struct {
	Body struct {
		Secret string `json:"client_secret"`
	}
}

func (h *Handler) registerGames(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopePublisher}

	huma.Register(api, huma.Operation{
		OperationID: "publisher-games", Method: http.MethodGet, Path: "/v1/publisher/games",
		Summary: "The publisher's games", Description: "The newest first, each with its open build.",
		Tags: []string{"publisher"}, Metadata: session,
	}, h.listGames)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-game-create", Method: http.MethodPost, Path: "/v1/publisher/games",
		Summary: "Register a game by its manifest",
		Description: "Reads and checks the manifest (every problem at once, 422), refuses an id " +
			"already taken (409 already_registered), and makes the game unpublished with its " +
			"first build in testing. The tier is partner whatever the document says. The " +
			"answer carries the client secret, once.",
		Tags: []string{"publisher"}, Metadata: session, DefaultStatus: http.StatusCreated,
	}, h.createGame)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-game", Method: http.MethodGet, Path: "/v1/publisher/games/{game}",
		Summary: "One of the publisher's games", Description: "With its open build and every build before it.",
		Tags: []string{"publisher"}, Metadata: session,
	}, h.getGame)

	huma.Register(api, huma.Operation{
		OperationID: "publisher-game-secret", Method: http.MethodPost, Path: "/v1/publisher/games/{game}/secret",
		Summary:     "Roll the client secret",
		Description: "A new secret, once. Tokens the game already holds keep working.",
		Tags:        []string{"publisher"}, Metadata: session,
	}, h.rollSecret)
}

func (h *Handler) listGames(ctx context.Context, _ *struct{}) (*gamesResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := h.Studio.Games(ctx, who.ID)
	if err != nil {
		return nil, err
	}
	out := &gamesResponse{}
	out.Body.Items = make([]games.StudioGameView, 0, len(rows))
	for i := range rows {
		out.Body.Items = append(out.Body.Items, games.ViewStudioGame(ctx, h.Files, &rows[i], false))
	}
	return out, nil
}

func (h *Handler) createGame(ctx context.Context, in *createGameRequest) (*createdResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	g, secret, err := h.Studio.Create(ctx, who, in.Body.URL)
	if err != nil {
		return nil, err
	}
	out := &createdResponse{}
	out.Body.Game = games.ViewStudioGame(ctx, h.Files, g, false)
	out.Body.Secret = secret
	return out, nil
}

func (h *Handler) getGame(ctx context.Context, in *gamePath) (*gameResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	return h.game(ctx)(h.Studio.Game(ctx, who.ID, in.Game))
}

func (h *Handler) rollSecret(ctx context.Context, in *gamePath) (*secretResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	secret, err := h.Studio.RollSecret(ctx, who, in.Game)
	if err != nil {
		return nil, err
	}
	out := &secretResponse{}
	out.Body.Secret = secret
	return out, nil
}

func (h *Handler) game(ctx context.Context) func(*games.StudioGame, error) (*gameResponse, error) {
	return func(g *games.StudioGame, err error) (*gameResponse, error) {
		if err != nil {
			return nil, err
		}
		return &gameResponse{Body: games.ViewStudioGame(ctx, h.Files, g, false)}, nil
	}
}

// ---- Builds ----

type buildPath struct {
	Build string `path:"build" example:"bld_7f3k9d2m8q1x4b6n"`
}

type newBuildRequest struct {
	Game string `path:"game"`
	Body struct {
		URL string `json:"url,omitempty" maxLength:"2048" doc:"Where this version's manifest is; the live one's address when empty. A test build is best read from an address of its own, so players keep loading the live entry."`
	}
}

type refreshRequest struct {
	Build string `path:"build"`
	Body  struct {
		URL string `json:"url,omitempty" maxLength:"2048" doc:"A new address for the build's manifest; empty reads the one it has."`
	}
}

type editRequest struct {
	Build string `path:"build"`
	Body  struct {
		Tagline     *string `json:"tagline,omitempty" maxLength:"200"`
		Description *string `json:"description,omitempty" maxLength:"5000"`
		Category    *string `json:"category,omitempty"`
		Notes       *string `json:"notes,omitempty" maxLength:"3000" doc:"For testers and the reviewer: what is new, what to try."`
	}
}

type pictureRequest struct {
	Build   string `path:"build"`
	RawBody multipart.Form
}

type screenshotRequest struct {
	Build string `path:"build"`
	Shot  string `path:"shot"`
}

type buildResponse struct{ Body games.BuildView }

const maxPictureBody = games.MaxPicture + 64<<10

func (h *Handler) registerBuilds(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopePublisher}
	op := func(id, method, path, summary, description string, status int) huma.Operation {
		return huma.Operation{
			OperationID: id, Method: method, Path: path, Summary: summary, Description: description,
			Tags: []string{"publisher"}, Metadata: session, DefaultStatus: status,
		}
	}

	huma.Register(api, op("publisher-build-create", http.MethodPost, "/v1/publisher/games/{game}/builds",
		"Start the next version",
		"Reads the manifest at url (the live one's address when empty); its store copy starts as "+
			"the live game's. 409 build_open, 409 suspended; 422 id: another_game, version: same_as_live.",
		http.StatusCreated), h.newBuild)
	huma.Register(api, op("publisher-build-refresh", http.MethodPost, "/v1/publisher/builds/{build}/refresh",
		"Read the build's manifest again",
		"From url when given: the build moves there. A document that changed takes an approved "+
			"build back to testing. 409 in_review, 409 not_open.", 0), h.refresh)
	huma.Register(api, op("publisher-build-edit", http.MethodPatch, "/v1/publisher/builds/{build}",
		"The build's store copy and notes",
		"tagline: too_long (160), description: too_long (4000), category: unknown, notes: too_long "+
			"(2000). A change to the copy takes an approved build back to testing.", 0), h.edit)
	huma.Register(api, op("publisher-build-submit", http.MethodPost, "/v1/publisher/builds/{build}/submit",
		"Send it to review", "422 incomplete (tagline, icon); 409 not_submittable, not_open.", 0), h.submit)
	huma.Register(api, op("publisher-build-cancel", http.MethodPost, "/v1/publisher/builds/{build}/cancel",
		"Take it back from review", "Back to testing. 409 not_in_review, not_open.", 0), h.cancel)
	huma.Register(api, op("publisher-build-publish", http.MethodPost, "/v1/publisher/builds/{build}/publish",
		"Publish an approved build",
		"It becomes the live game: its manifest, its store copy, published if it was not. "+
			"409 not_approved, suspended, not_open.", 0), h.publish)
	huma.Register(api, op("publisher-build-withdraw", http.MethodPost, "/v1/publisher/builds/{build}/withdraw",
		"Discard an open build", "Testers get the live game again. 409 not_open.", 0), h.withdraw)

	icon := op("publisher-build-icon", http.MethodPut, "/v1/publisher/builds/{build}/icon",
		"Upload the build's tile picture", "A PNG or JPEG up to 2 MB, the one file in a multipart body.", 0)
	icon.MaxBodyBytes = maxPictureBody
	huma.Register(api, icon, h.putIcon)
	shot := op("publisher-build-screenshot-add", http.MethodPost, "/v1/publisher/builds/{build}/screenshots",
		"Add a screenshot", "A PNG or JPEG up to 2 MB. At most six.", 0)
	shot.MaxBodyBytes = maxPictureBody
	huma.Register(api, shot, h.addScreenshot)
	huma.Register(api, op("publisher-build-screenshot-remove", http.MethodDelete,
		"/v1/publisher/builds/{build}/screenshots/{shot}", "Remove a screenshot",
		"A screenshot the build inherited from the live game leaves the live game's file alone.", 0),
		h.removeScreenshot)
}

func (h *Handler) newBuild(ctx context.Context, in *newBuildRequest) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.NewBuild(ctx, who, in.Game, in.Body.URL)
	return h.build(ctx, who, b, err)
}

func (h *Handler) refresh(ctx context.Context, in *refreshRequest) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.Refresh(ctx, who, in.Build, in.Body.URL)
	return h.build(ctx, who, b, err)
}

func (h *Handler) edit(ctx context.Context, in *editRequest) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.Edit(ctx, who, in.Build, games.BuildCopy{
		Tagline: in.Body.Tagline, Description: in.Body.Description, Category: in.Body.Category, Notes: in.Body.Notes,
	})
	return h.build(ctx, who, b, err)
}

func (h *Handler) submit(ctx context.Context, in *buildPath) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.Submit(ctx, who, in.Build)
	return h.build(ctx, who, b, err)
}

func (h *Handler) cancel(ctx context.Context, in *buildPath) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.Cancel(ctx, who, in.Build)
	return h.build(ctx, who, b, err)
}

func (h *Handler) publish(ctx context.Context, in *buildPath) (*gameResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	return h.game(ctx)(h.Studio.Publish(ctx, who, in.Build))
}

func (h *Handler) withdraw(ctx context.Context, in *buildPath) (*gameResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	return h.game(ctx)(h.Studio.Withdraw(ctx, who, in.Build))
}

func (h *Handler) putIcon(ctx context.Context, in *pictureRequest) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	up, err := readPicture(in.RawBody)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.SetIcon(ctx, who, in.Build, up)
	return h.build(ctx, who, b, err)
}

func (h *Handler) addScreenshot(ctx context.Context, in *pictureRequest) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	up, err := readPicture(in.RawBody)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.AddScreenshot(ctx, who, in.Build, up)
	return h.build(ctx, who, b, err)
}

func (h *Handler) removeScreenshot(ctx context.Context, in *screenshotRequest) (*buildResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.Studio.RemoveScreenshot(ctx, who, in.Build, in.Shot)
	return h.build(ctx, who, b, err)
}

// build draws a build of the publisher's, beside its game.
func (h *Handler) build(ctx context.Context, who games.Account, b *model.GameBuild, err error) (*buildResponse, error) {
	if err != nil {
		return nil, err
	}
	sg, err := h.Studio.Game(ctx, who.ID, b.GameID)
	if err != nil {
		return nil, err
	}
	return &buildResponse{Body: games.ViewBuild(ctx, h.Files, b, &sg.Game, false)}, nil
}

// readPicture is the one file in a multipart body.
func readPicture(form multipart.Form) (games.Picture, error) {
	parts := form.File["file"]
	if len(parts) == 0 {
		return games.Picture{}, errors.Invalid(map[string]string{"file": "required"})
	}
	f, err := parts[0].Open()
	if err != nil {
		return games.Picture{}, errors.Internal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, games.MaxPicture+1))
	if err != nil {
		return games.Picture{}, errors.Internal(err)
	}
	return games.Picture{ContentType: parts[0].Header.Get("Content-Type"), Data: data}, nil
}

// ---- Testers ----

type testerJSON struct {
	players.Card
	JoinedAt string `json:"joined_at"`
	Code     string `json:"code" doc:"The code they joined with."`
}

type testersResponse struct {
	Body struct {
		Invites []games.InviteView `json:"invites"`
		Testers []testerJSON       `json:"testers"`
	}
}

type inviteRequest struct {
	Game string `path:"game"`
	Body struct {
		Label   string `json:"label,omitempty" maxLength:"100"`
		MaxUses *int   `json:"max_uses,omitempty" doc:"1 to 1000; no limit when absent."`
		Days    *int   `json:"days,omitempty" doc:"1 to 90; 30 when absent."`
	}
}

type inviteResponse struct{ Body games.InviteView }

type invitePath struct {
	Game string `path:"game"`
	Code string `path:"code"`
}

type testerPath struct {
	Game   string `path:"game"`
	Player string `path:"player"`
}

func (h *Handler) registerTesters(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopePublisher}

	huma.Register(api, huma.Operation{
		OperationID: "publisher-testers", Method: http.MethodGet, Path: "/v1/publisher/games/{game}/testers",
		Summary: "A game's codes and testers", Tags: []string{"publisher"}, Metadata: session,
	}, h.testers)
	huma.Register(api, huma.Operation{
		OperationID: "publisher-invite-create", Method: http.MethodPost, Path: "/v1/publisher/games/{game}/invites",
		Summary:     "Make a tester's code",
		Description: "422 label: too_long, max_uses: 1_to_1000, days: 1_to_90, invites: too_many (20 live).",
		Tags:        []string{"publisher"}, Metadata: session, DefaultStatus: http.StatusCreated,
	}, h.invite)
	huma.Register(api, huma.Operation{
		OperationID: "publisher-invite-revoke", Method: http.MethodDelete, Path: "/v1/publisher/games/{game}/invites/{code}",
		Summary: "Stop a code", Description: "At once; who joined with it stays.",
		Tags: []string{"publisher"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.revoke)
	huma.Register(api, huma.Operation{
		OperationID: "publisher-tester-remove", Method: http.MethodDelete, Path: "/v1/publisher/games/{game}/testers/{player}",
		Summary: "Take a tester off", Description: "404 not_found when they were not one.",
		Tags: []string{"publisher"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.removeTester)
}

func (h *Handler) testers(ctx context.Context, in *gamePath) (*testersResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	invites, testers, err := h.Studio.Testers(ctx, who.ID, in.Game)
	if err != nil {
		return nil, err
	}
	out := &testersResponse{}
	out.Body.Invites = make([]games.InviteView, 0, len(invites))
	for i := range invites {
		out.Body.Invites = append(out.Body.Invites, games.ViewInvite(&invites[i]))
	}
	out.Body.Testers = make([]testerJSON, 0, len(testers))
	for _, t := range testers {
		out.Body.Testers = append(out.Body.Testers, testerJSON{
			Card:     players.NewCard(ctx, h.Files, h.AvatarTTL, t.Tester.PlayerID, t.Nickname, t.Handle, t.Avatar),
			JoinedAt: stamp(t.Tester.JoinedAt),
			Code:     t.Tester.Code,
		})
	}
	return out, nil
}

func (h *Handler) invite(ctx context.Context, in *inviteRequest) (*inviteResponse, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := h.Studio.Invite(ctx, who, in.Game, games.InviteRequest{
		Label: in.Body.Label, MaxUses: in.Body.MaxUses, Days: in.Body.Days,
	})
	if err != nil {
		return nil, err
	}
	return &inviteResponse{Body: games.ViewInvite(inv)}, nil
}

func (h *Handler) revoke(ctx context.Context, in *invitePath) (*noContent, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	return nil, h.Studio.Revoke(ctx, who, in.Game, in.Code)
}

func (h *Handler) removeTester(ctx context.Context, in *testerPath) (*noContent, error) {
	who, err := h.account(ctx)
	if err != nil {
		return nil, err
	}
	return nil, h.Studio.RemoveTester(ctx, who, in.Game, in.Player)
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
