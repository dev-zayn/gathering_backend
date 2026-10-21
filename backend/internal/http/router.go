package http

import (
	"context"
	stdhttp "net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/jamaa/gathering/backend/internal/biz"
	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/http/admin"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/http/guest"
	"github.com/jamaa/gathering/backend/internal/http/player"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// Deps is everything the routes need, in one struct because there is one binary.
type Deps struct {
	// Resolve turns an Authorization header into who is calling. The authn
	// middleware asks it on every guarded request.
	Resolve authn.Resolver

	// AdminCookie is where the console's session travels, and which origins
	// may send it.
	AdminCookie authn.Cookie

	// Signing in, and everything that follows from holding a token.
	SignIn  *identity.SignIn
	Verify  *identity.Verify
	Refresh *identity.Refresh
	Signup  *biz.Signup
	Devices *identity.Devices

	// The player's own record, and who is here.
	Profiles *players.Profiles
	Presence *players.Presence
	Players  *identity.PlayerStore

	// The games: the store and the shelf for a player, the registry for an
	// admin, the launch and the bridge for a game's own server.
	Games    *games.Catalogue
	Registry *games.Registry
	Launch   *guests.Launch
	Bridge   *guests.Bridge

	// Files signs the links to every picture. LocalFiles is a laptop's
	// stand-in for the bucket, served by this process under /files/; nil
	// anywhere a real bucket signs its own links.
	Files      files.Files
	LocalFiles stdhttp.Handler

	// The console.
	Console   *identity.Console
	Admins    *identity.AdminStore
	Dashboard *biz.Dashboard
	Audit     audit.Reader

	// Signer publishes the JWKS a game's server verifies with.
	Signer *token.Signer

	// PublicURL is the address the outside reaches us on, which is not the one
	// this process listens on.
	PublicURL string

	// DocsURL is where the developer documentation is served from.
	DocsURL string

	// Ready is what /ready asks after, by name.
	Ready map[string]Check
}

// Mount puts every route on one API in three groups. Which group a route is in
// is who may call it: player, admin or guest.
func Mount(api huma.API, d Deps) {
	resolve := d.Resolve
	if resolve == nil {
		resolve = func(context.Context, string) (*caller.Session, string, error) {
			return nil, "missing_token", nil
		}
	}
	api.UseMiddleware(authn.Auth(api, d.PublicURL, resolve, d.AdminCookie))
	ready(api, d.Ready)

	(&player.Handler{
		SignIn:   d.SignIn,
		Verify:   d.Verify,
		Refresh:  d.Refresh,
		Signup:   d.Signup,
		Devices:  d.Devices,
		Profiles: d.Profiles,
		Presence: d.Presence,
		Games:    d.Games,
		Files:    d.Files,
	}).Register(api)

	(&admin.Handler{
		Console:   d.Console,
		Cookie:    d.AdminCookie,
		Admins:    d.Admins,
		Players:   d.Players,
		Presence:  d.Presence,
		Dashboard: d.Dashboard,
		Audit:     d.Audit,
		Registry:  d.Registry,
		Files:     d.Files,
	}).Register(api)

	(&guest.Handler{
		Launch:    d.Launch,
		Bridge:    d.Bridge,
		Signer:    d.Signer,
		PublicURL: d.PublicURL,
		DocsURL:   d.DocsURL,
	}).Register(api)

	// A laptop serves its own pictures. Straight onto the router, outside the
	// API: a picture is not an operation, carries no token, and is not in the
	// OpenAPI document.
	if d.LocalFiles != nil {
		api.Adapter().Handle(&huma.Operation{Method: stdhttp.MethodGet, Path: "/files/*"}, func(ctx huma.Context) {
			r, w := humachi.Unwrap(ctx)
			d.LocalFiles.ServeHTTP(w, r)
		})
	}
}
