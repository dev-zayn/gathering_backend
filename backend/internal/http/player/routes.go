// Package player is everything a signed-in player reaches, from the first OTP
// to their own record and their shelf of games. It holds no rules of its own.
package player

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz"
	"github.com/jamaa/gathering/backend/internal/biz/friends"
	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/biz/tournaments"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
)

// Handler is every route in this group. What it holds is what this group may
// reach.
type Handler struct {
	SignIn   *identity.SignIn
	Verify   *identity.Verify
	Refresh  *identity.Refresh
	Signup   *biz.Signup
	Devices  *identity.Devices
	Profiles *players.Profiles
	Presence *players.Presence

	// Games is the store and the shelf; Files signs the links to the pictures
	// in it.
	Games *games.Catalogue
	Files files.Files

	// Rooms is every rule about rooms; the relay hears each change.
	Rooms *rooms.Service

	// Tournaments is every rule about tournaments; a match is played in a
	// room, made or joined through it.
	Tournaments *tournaments.Service

	// Friends is who is whose friend, and the cards that say so; Inbox is
	// the player's notifications.
	Friends *friends.Service
	Inbox   *inbox.Inbox
}

func (h *Handler) Register(api huma.API) {
	h.registerSignin(api)
	h.registerVerify(api)
	h.registerSignup(api)
	h.registerSessions(api)
	h.registerMe(api)
	h.registerDevice(api)
	h.registerPlayers(api)
	h.registerGames(api)
	h.registerRooms(api)
	h.registerResults(api)
	h.registerFriends(api)
	h.registerNotifications(api)
	h.registerTournaments(api)
}
