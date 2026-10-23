// Package guest is what a game's backend and a standard client reach: the
// launch that mints a one-time code, the OAuth exchange that turns it into
// the game's own credential, the bridge that answers who arrived, and the
// discovery documents. A guest holds a credential of its own, minted for one
// game and worth nothing anywhere else. Here too is what anybody reaches with
// nothing at all: a room's invite link, a tournament's link, and the
// documents a phone reads to let the app catch them.
package guest

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/biz/tournaments"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

type Handler struct {
	Launch *guests.Launch
	Bridge *guests.Bridge
	Signer *token.Signer

	// Rooms finds the open room behind an invite link, Tournaments the live
	// tournament behind its link. AppLinks is how the phones claim the link
	// host as the app's, and where a phone without the app gets it.
	Rooms       *rooms.Service
	Tournaments *tournaments.Service
	AppLinks    AppLinks

	// PublicURL is the address the outside reaches us on. Every refusal names
	// the protected-resource document under it.
	PublicURL string

	// DocsURL is where a developer reads about us: its own origin, not a path
	// under ours.
	DocsURL string
}

func (h *Handler) Register(api huma.API) {
	h.registerLaunch(api)
	h.registerOAuth(api)
	h.registerBridge(api)
	h.registerWellKnown(api)
	h.registerLinks(api)
}
