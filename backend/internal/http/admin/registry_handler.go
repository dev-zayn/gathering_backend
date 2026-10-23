package admin

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// The registry is the admin's surface. Adding a game is registering a URL;
// everything else is read from the manifest the studio publishes about it.
type gamePath struct {
	Game string `path:"game" example:"fixture.xo"`
}

type registryRoomJSON struct {
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Host       string `json:"host" enum:"authoritative,peer"`
	Pace       string `json:"pace" enum:"turn,realtime"`
	LAN        bool   `json:"lan"`
	State      string `json:"state" enum:"blob,none"`
	Rejoin     bool   `json:"rejoin"`
	Spectators bool   `json:"spectators"`
}

type registryGameJSON struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Publisher    string           `json:"publisher"`
	Tier         string           `json:"tier" enum:"first-party,partner"`
	Version      string           `json:"version"`
	Entry        string           `json:"entry"`
	Listing      string           `json:"listing" enum:"published,unpublished,suspended"`
	Featured     bool             `json:"featured"`
	Category     string           `json:"category"`
	Tagline      string           `json:"tagline"`
	Icon         string           `json:"icon"`
	Orientation  string           `json:"orientation" enum:"portrait,landscape"`
	Splash       bool             `json:"splash"`
	Scopes       []string         `json:"scopes"`
	Permissions  []string         `json:"permissions"`
	RedirectURIs []string         `json:"redirect_uris"`
	Room         registryRoomJSON `json:"room"`
	ManifestURL  string           `json:"manifest_url"`
	FetchedAt    *string          `json:"fetched_at,omitempty"`

	// A re-read waiting for somebody to read it: what it newly asks for.
	Held *heldJSON `json:"held,omitempty"`

	// The store copy in full: the table shows the tagline, the form edits it
	// with the description and the screenshots.
	Store storeJSON `json:"store"`
}

type heldJSON struct {
	Version     string           `json:"version"`
	Scopes      []string         `json:"scopes" doc:"Every scope the held document asks for."`
	New         []string         `json:"new" doc:"The scopes the current manifest does not have."`
	Permissions []string         `json:"permissions"`
	Room        registryRoomJSON `json:"room"`
	Changed     []string         `json:"changed" doc:"Which blocks differ from the current manifest: scopes, permissions, room."`
	URL         string           `json:"url"`
	At          string           `json:"at"`
}

type storeJSON struct {
	Tagline     string           `json:"tagline"`
	Description string           `json:"description"`
	Screenshots []screenshotJSON `json:"screenshots"`
}

// A picture on the page: the id a remove names, and a link that expires.
type screenshotJSON struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type registryGameResponse struct{ Body registryGameJSON }

// gameRegisteredResponse is the one response that carries the client secret.
// It is shown here and nowhere else, ever: only its hash is stored.
type gameRegisteredResponse struct {
	Body struct {
		registryGameJSON
		Secret string `json:"client_secret" doc:"Shown once. Store it — there is no way to read it back, only to roll it."`
	}
}

type secretResponse struct {
	Body struct {
		Secret string `json:"client_secret"`
	}
}

type registryListRequest struct {
	Listing string `query:"listing" enum:"published,unpublished,suspended"`
}

type registryListResponse struct {
	Body struct {
		Games []registryGameJSON `json:"games"`
	}
}

type registerGameRequest struct {
	Body struct {
		URL string `json:"url" required:"true" example:"https://games.yourstudio.example/xo/" doc:"The manifest's address, or the game's origin. An origin, or anything ending in a slash, gets /.well-known/gathering-game.json appended."`
	}
}

type suspendRequest struct {
	Game string `path:"game"`
	Body struct {
		Reason string `json:"reason,omitempty" maxLength:"500" doc:"For the log. The player is not shown it."`
	}
}

type screenshotRequest struct {
	Game string `path:"game"`
	Shot string `path:"shot" doc:"The id from store.screenshots."`
}

type pictureRequest struct {
	Game    string `path:"game"`
	RawBody multipart.Form
}

type moveRequest struct {
	Game string `path:"game"`
	Body struct {
		URL string `json:"url" required:"true" example:"https://games.yourstudio.example/xo/.well-known/gathering-game.json" doc:"Where the manifest lives now. It must still name this game."`
	}
}

type featureRequest struct {
	Game string `path:"game"`
	Body struct {
		Featured *bool `json:"featured,omitempty" doc:"Defaults to true. Send false to take a game back down."`
	}
}

type listingRequest struct {
	Game string `path:"game"`
	Body struct {
		Tagline     *string `json:"tagline,omitempty" maxLength:"160" example:"إكس أو، لكن مع صديقك"`
		Description *string `json:"description,omitempty" maxLength:"4000"`
		Category    *string `json:"category,omitempty" example:"brain"`
		Featured    *bool   `json:"featured,omitempty"`
	}
}

type reachJSON struct {
	Game    string  `json:"game"`
	Players int     `json:"players" doc:"How many shelves it is on."`
	LastAt  *string `json:"last_at,omitempty" doc:"When the last player added it."`
}

type reachResponse struct {
	Body struct {
		Games []reachJSON `json:"games"`

		// Players is how many distinct players hold anything — not the number
		// of shelves, and not the number of players.
		Players int `json:"players"`
		Total   int `json:"total" doc:"Every shelf, summed."`
	}
}

type categoriesResponse struct {
	Body struct {
		Categories []string `json:"categories"`
	}
}

// maxPictureBody is what the whole multipart body may be: the picture at its
// largest, and the framing around it. Without it huma stops at a megabyte.
const maxPictureBody = games.MaxPicture + 64<<10

func (h *Handler) registerRegistry(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)}

	huma.Register(api, huma.Operation{
		OperationID: "admin-games-list",
		Method:      http.MethodGet,
		Path:        "/v1/admin/games",
		Summary:     "Every game, in any listing",
		Tags:        []string{"admin"},
		Metadata:    admin,
	}, h.listRegistry)

	huma.Register(api, huma.Operation{
		OperationID: "admin-games-register",
		Method:      http.MethodPost,
		Path:        "/v1/admin/games",
		Summary:     "Register by manifest URL",
		Description: "Fetches, validates and creates the game unpublished. Nothing is " +
			"hand-entered and nothing is deployed: a game is a document we read. The " +
			"answer carries the client secret, once.",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusCreated,
		Metadata:      write,
	}, h.registerGame)

	huma.Register(api, huma.Operation{
		OperationID: "admin-games-reach",
		Method:      http.MethodGet,
		Path:        "/v1/admin/games/reach",
		Summary:     "How far each game has got",
		Description: "Shelves per game. Suspending a game is a different decision on three " +
			"shelves than on three thousand, and until now nothing said which.",
		Tags:     []string{"admin"},
		Metadata: admin,
	}, h.reach)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game",
		Method:      http.MethodGet,
		Path:        "/v1/admin/games/{game}",
		Summary:     "One game, as the registry holds it",
		Tags:        []string{"admin"},
		Metadata:    admin,
	}, h.getGame)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-listing",
		Method:      http.MethodPatch,
		Path:        "/v1/admin/games/{game}",
		Summary:     "The store copy",
		Description: "The tagline, description, category and the featured switch — the half " +
			"no manifest carries. Deliberately ours to write: a game allowed to describe " +
			"its own permissions would call an alarming one a speed improvement.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.listing)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-publish",
		Method:      http.MethodPost,
		Path:        "/v1/admin/games/{game}/publish",
		Summary:     "Put it in the store",
		Description: "The human gate. Refused until the store copy exists — a row with no " +
			"tagline and no tile is a broken tile in every store.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.publish)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-suspend",
		Method:      http.MethodPost,
		Path:        "/v1/admin/games/{game}/suspend",
		Summary:     "The kill switch",
		Description: "Leaves every store and shelf at once, and its live tokens stop with it. " +
			"Shelves are not emptied: reinstating it is another publish.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.suspend)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-refresh",
		Method:      http.MethodPost,
		Path:        "/v1/admin/games/{game}/refresh",
		Summary:     "Re-fetch the manifest",
		Description: "A document asking for more scopes, other permissions or another room is " +
			"not saved for a published game: it is held until an admin accepts it, and " +
			"players carry on with the manifest they had.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.rereadOne)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-accept",
		Method:      http.MethodPost,
		Path:        "/v1/admin/games/{game}/accept",
		Summary:     "Accept a held manifest",
		Description: "A re-read of a published game that asks for more waits here. Accepting " +
			"it makes it the game's manifest; every player's next token carries the new " +
			"scopes, and every new room the new shape. Refused when nothing is held.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.accept)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-secret",
		Method:      http.MethodPost,
		Path:        "/v1/admin/games/{game}/secret",
		Summary:     "Roll the client secret",
		Description: "Returns a new secret once. Tokens the game already holds keep working: " +
			"the secret proves who is asking for a new token, not who already has one.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.roll)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-feature",
		Method:      http.MethodPost,
		Path:        "/v1/admin/games/{game}/feature",
		Summary:     "Put it at the top of the store",
		Description: "Featured games lead the store, whatever their tier. Send featured false " +
			"to take one back down.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.feature)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-move",
		Method:      http.MethodPut,
		Path:        "/v1/admin/games/{game}/manifest",
		Summary:     "Move the manifest to a new address",
		Description: "For a studio that moved its document. The manifest at the new " +
			"address must name this game; the address changes, the game does not.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.move)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-icon",
		Method:      http.MethodPut,
		Path:        "/v1/admin/games/{game}/icon",
		Summary:     "Upload the tile picture",
		Description: "A PNG or JPEG up to 2 MB, as the one file in a multipart body. " +
			"Kept in our bucket, so a studio's CDN going away blanks no store.",
		Tags:         []string{"admin"},
		Metadata:     write,
		MaxBodyBytes: maxPictureBody,
	}, h.putIcon)

	huma.Register(api, huma.Operation{
		OperationID:  "admin-game-screenshot-add",
		Method:       http.MethodPost,
		Path:         "/v1/admin/games/{game}/screenshots",
		Summary:      "Add a screenshot to the page in the store",
		Description:  "A PNG or JPEG up to 2 MB, as the one file in a multipart body. At most six.",
		Tags:         []string{"admin"},
		Metadata:     write,
		MaxBodyBytes: maxPictureBody,
	}, h.addScreenshot)

	huma.Register(api, huma.Operation{
		OperationID: "admin-game-screenshot-remove",
		Method:      http.MethodDelete,
		Path:        "/v1/admin/games/{game}/screenshots/{shot}",
		Summary:     "Remove a screenshot",
		Tags:        []string{"admin"},
		Metadata:    write,
	}, h.removeScreenshot)

	huma.Register(api, huma.Operation{
		OperationID: "admin-categories",
		Method:      http.MethodGet,
		Path:        "/v1/admin/categories",
		Summary:     "How the store groups games",
		Description: "The closed list a category must come from, in the order the store " +
			"shows it. The console's picker and filter read it here rather than guessing.",
		Tags:     []string{"admin"},
		Metadata: admin,
	}, h.categories)
}

func (h *Handler) categories(_ context.Context, _ *struct{}) (*categoriesResponse, error) {
	out := &categoriesResponse{}
	out.Body.Categories = make([]string, 0, len(enum.Categories))
	for _, c := range enum.Categories {
		out.Body.Categories = append(out.Body.Categories, string(c))
	}
	return out, nil
}

func (h *Handler) reach(ctx context.Context, _ *struct{}) (*reachResponse, error) {
	rows, players, err := h.Registry.Reach(ctx)
	if err != nil {
		return nil, err
	}

	out := &reachResponse{}
	out.Body.Games = make([]reachJSON, 0, len(rows))
	out.Body.Players = players
	for _, r := range rows {
		out.Body.Games = append(out.Body.Games, reachJSON{Game: r.GameID, Players: r.Players, LastAt: stamp(r.LastAt)})
		out.Body.Total += r.Players
	}
	return out, nil
}

func (h *Handler) listRegistry(ctx context.Context, in *registryListRequest) (*registryListResponse, error) {
	rows, err := h.Registry.List(ctx, in.Listing)
	if err != nil {
		return nil, err
	}

	out := &registryListResponse{}
	out.Body.Games = make([]registryGameJSON, 0, len(rows))
	for i := range rows {
		out.Body.Games = append(out.Body.Games, h.registryGameOf(ctx, &rows[i]))
	}
	return out, nil
}

func (h *Handler) getGame(ctx context.Context, in *gamePath) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.Get(ctx, in.Game))
}

func (h *Handler) registerGame(ctx context.Context, in *registerGameRequest) (*gameRegisteredResponse, error) {
	g, secret, err := h.Registry.Register(ctx, actor(ctx), in.Body.URL)
	if err != nil {
		return nil, err
	}

	out := &gameRegisteredResponse{}
	out.Body.registryGameJSON = h.registryGameOf(ctx, g)
	out.Body.Secret = secret
	return out, nil
}

func (h *Handler) roll(ctx context.Context, in *gamePath) (*secretResponse, error) {
	secret, err := h.Registry.RollSecret(ctx, actor(ctx), in.Game)
	if err != nil {
		return nil, err
	}

	out := &secretResponse{}
	out.Body.Secret = secret
	return out, nil
}

func (h *Handler) rereadOne(ctx context.Context, in *gamePath) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.Refresh(ctx, actor(ctx), in.Game))
}

func (h *Handler) accept(ctx context.Context, in *gamePath) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.Accept(ctx, actor(ctx), in.Game))
}

func (h *Handler) putIcon(ctx context.Context, in *pictureRequest) (*registryGameResponse, error) {
	up, err := readPicture(in.RawBody)
	if err != nil {
		return nil, err
	}
	return h.registryGame(ctx)(h.Registry.SetIcon(ctx, actor(ctx), in.Game, up))
}

func (h *Handler) addScreenshot(ctx context.Context, in *pictureRequest) (*registryGameResponse, error) {
	up, err := readPicture(in.RawBody)
	if err != nil {
		return nil, err
	}
	return h.registryGame(ctx)(h.Registry.AddScreenshot(ctx, actor(ctx), in.Game, up))
}

func (h *Handler) removeScreenshot(ctx context.Context, in *screenshotRequest) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.RemoveScreenshot(ctx, actor(ctx), in.Game, in.Shot))
}

// readPicture is the one file in a multipart body, as the registry takes it.
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

func (h *Handler) move(ctx context.Context, in *moveRequest) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.Move(ctx, actor(ctx), in.Game, in.Body.URL))
}

func (h *Handler) publish(ctx context.Context, in *gamePath) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.Publish(ctx, actor(ctx), in.Game))
}

func (h *Handler) suspend(ctx context.Context, in *suspendRequest) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.Suspend(ctx, actor(ctx), in.Game, in.Body.Reason))
}

func (h *Handler) feature(ctx context.Context, in *featureRequest) (*registryGameResponse, error) {
	on := true
	if in.Body.Featured != nil {
		on = *in.Body.Featured
	}
	return h.registryGame(ctx)(h.Registry.Feature(ctx, actor(ctx), in.Game, on))
}

func (h *Handler) listing(ctx context.Context, in *listingRequest) (*registryGameResponse, error) {
	return h.registryGame(ctx)(h.Registry.Listing(ctx, actor(ctx), in.Game, model.Listing{
		Tagline:     in.Body.Tagline,
		Description: in.Body.Description,
		Category:    in.Body.Category,
		Featured:    in.Body.Featured,
	}))
}

// registryGame wraps a registry call's result, signing the picture links for
// the request it answers.
func (h *Handler) registryGame(ctx context.Context) func(*model.Game, error) (*registryGameResponse, error) {
	return func(g *model.Game, err error) (*registryGameResponse, error) {
		if err != nil {
			return nil, err
		}
		return &registryGameResponse{Body: h.registryGameOf(ctx, g)}, nil
	}
}

func actor(ctx context.Context) string { return caller.FromContext(ctx).Subject }

func registryRoomOf(r model.RoomBlock) registryRoomJSON {
	return registryRoomJSON{
		Min: r.Players.Min, Max: r.Players.Max, Host: string(r.Host), Pace: string(r.Pace),
		LAN: r.LAN, State: string(r.State), Rejoin: r.Rejoin, Spectators: r.Spectators,
	}
}

func (h *Handler) registryGameOf(ctx context.Context, g *model.Game) registryGameJSON {
	shots := []screenshotJSON{}
	for _, s := range games.ScreenshotURLs(ctx, h.Files, g) {
		shots = append(shots, screenshotJSON{ID: s.ID, URL: s.URL})
	}
	j := registryGameJSON{
		ID:           g.ID,
		Name:         g.Name,
		Publisher:    g.Publisher,
		Tier:         string(g.Tier),
		Version:      g.Version,
		Entry:        g.Entry,
		Listing:      string(g.Listing),
		Featured:     g.Featured,
		Category:     string(g.Category),
		Tagline:      g.Tagline,
		Icon:         games.IconURL(ctx, h.Files, g),
		Orientation:  string(g.Orientation),
		Splash:       g.Splash,
		Scopes:       nonNil(g.Scopes),
		Permissions:  nonNil(g.Permissions),
		RedirectURIs: nonNil(g.RedirectURIs),
		Room:         registryRoomOf(g.Room),
		ManifestURL:  g.ManifestURL,
		FetchedAt:    stamp(g.FetchedAt),
		Store: storeJSON{
			Tagline:     g.Tagline,
			Description: g.Description,
			Screenshots: shots,
		},
	}

	if g.HeldAt != nil {
		var held model.Manifest
		_ = json.Unmarshal(g.Held, &held)
		have := map[string]bool{}
		for _, s := range g.Scopes {
			have[s] = true
		}
		fresh := []string{}
		for _, s := range held.Scopes {
			if !have[s] {
				fresh = append(fresh, s)
			}
		}
		changed := []string{}
		if len(fresh) > 0 {
			changed = append(changed, "scopes")
		}
		if !sameSet(g.Permissions, held.Permissions) {
			changed = append(changed, "permissions")
		}
		if held.Room.State == "" {
			held.Room.State = enum.StateBlob
		}
		if held.Room != g.Room {
			changed = append(changed, "room")
		}
		j.Held = &heldJSON{
			Version:     held.Version,
			Scopes:      nonNil(held.Scopes),
			New:         fresh,
			Permissions: nonNil(held.Permissions),
			Room:        registryRoomOf(held.Room),
			Changed:     changed,
			URL:         g.HeldURL,
			At:          *stamp(g.HeldAt),
		}
	}
	return j
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	in := make(map[string]bool, len(a))
	for _, s := range a {
		in[s] = true
	}
	for _, s := range b {
		if !in[s] {
			return false
		}
	}
	return true
}
