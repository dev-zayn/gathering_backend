// Package admin is the console's routes: a different audience and a different
// scope of token, so nothing here is reachable with a player's session.
package admin

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz"
	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/biz/tournaments"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
)

type Handler struct {
	Console   *identity.Console
	Cookie    authn.Cookie
	Admins    *identity.AdminStore
	Players   *identity.PlayerStore
	Presence  *players.Presence
	Dashboard *biz.Dashboard
	Audit     audit.Reader

	// Registry is the games as an admin sees them; Files signs the links to
	// their pictures.
	Registry *games.Registry
	Files    files.Files

	// Rooms is every room, and the close; Inbox sends the announcements.
	Rooms *rooms.Service
	Inbox *inbox.Inbox

	// Tournaments is every tournament, the official ones made here, and the
	// console's hand in any of them.
	Tournaments *tournaments.Service
}

func (h *Handler) Register(api huma.API) {
	h.registerSession(api)
	h.registerAdmin(api)
	h.registerPlayers(api)
	h.registerRegistry(api)
	h.registerDashboard(api)
	h.registerAudit(api)
	h.registerRooms(api)
	h.registerAnnouncements(api)
	h.registerTournaments(api)
}
