package model

import (
	"database/sql/driver"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// WellKnown is where a game publishes what it is. Registering a game is
// giving us this URL; nothing is hand-entered.
const WellKnown = "/.well-known/gathering-game.json"

// ManifestURL turns whatever an admin pasted into the document's address. A
// bare origin is the common case, so it is the one that is made to work.
func ManifestURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", false
	}
	// An origin, or a folder ending in a slash, is a place: the document is at
	// the well-known path under it. Anything else names the document itself,
	// wherever it is hosted and whatever the address looks like.
	if u.Path == "" || strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimSuffix(u.Path, "/") + WellKnown
	}
	return u.String(), true
}

// Manifest is the document itself. Only the fields the platform acts on are
// named; the rest is kept whole in the row for whatever reads it later.
type Manifest struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Version string    `json:"version"`
	Mode    enum.Mode `json:"mode"`
	Entry   string    `json:"entry"`

	// Orientation is how the frame is turned; Splash says the page needs a
	// moment before it can paint, so the app covers it until it says ready.
	Orientation enum.Orientation `json:"orientation"`
	Splash      bool             `json:"splash"`

	Publisher struct {
		Tier    enum.Tier `json:"tier"`
		Name    string    `json:"name"`
		Company string    `json:"company"`
	} `json:"publisher"`

	// Scopes is what the game's server may read of ours; Permissions is what
	// the app asks the player's phone for on the game's behalf.
	Scopes      []string `json:"scopes"`
	Permissions []string `json:"permissions"`

	// RedirectURIs is optional. Without it, anything on the entry's origin is
	// where the game may be sent back to.
	RedirectURIs []string `json:"redirect_uris"`

	// Room is how a room for this game is shaped: the platform admits players
	// by it and the relay enforces it. The game page never configures a room
	// itself.
	Room RoomBlock `json:"room"`
}

// RoomBlock is the manifest's room block. It is also one jsonb column of the
// game's row, whole, so a game's bounds are read without parsing the whole
// document, and it is copied into every room made for the game (rooms.config),
// so a manifest that changes later does not reshape a room already open.
type RoomBlock struct {
	Players struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"players"`
	Host       enum.HostMode  `json:"host"`
	Pace       enum.Pace      `json:"pace"`
	LAN        bool           `json:"lan"`
	Rejoin     bool           `json:"rejoin"`
	Spectators bool           `json:"spectators"`
	State      enum.StateMode `json:"state"`
}

func (r *RoomBlock) Scan(src any) error { return scanJSON(src, r) }

func (r RoomBlock) Value() (driver.Value, error) { return valueJSON(r) }

// FirstParty is the tier, not anybody's word for it.
func (m *Manifest) FirstParty() bool { return m.Publisher.Tier == enum.TierFirstParty }

var gameID = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9-]+)+$`)

// MaxPlayers is the most a room may hold, which is also the relay's ceiling.
const MaxPlayers = 64

// Check gathers everything wrong with a manifest at once. Registering is a
// thing an admin does with somebody else's file: they should see the whole
// list. The two fields a document may leave out — the orientation and the
// state mode — are filled in with their defaults here, so the row and the
// document agree on what was meant.
func (m *Manifest) Check(knownScopes map[string]bool) map[string]string {
	bad := map[string]string{}

	if !gameID.MatchString(m.ID) || TooLong(m.ID, MaxGameID) {
		bad["id"] = "must look like yourstudio.game"
	}
	if strings.TrimSpace(m.Name) == "" {
		bad["name"] = "required"
	} else if TooLong(m.Name, MaxName) {
		bad["name"] = "too_long"
	}
	if TooLong(m.Publisher.Name, MaxName) || TooLong(m.Publisher.Company, MaxName) {
		bad["publisher.name"] = "too_long"
	}
	if strings.TrimSpace(m.Version) == "" {
		bad["version"] = "required"
	} else if TooLong(m.Version, MaxVersion) {
		bad["version"] = "too_long"
	}
	if !m.Mode.Valid() {
		bad["mode"] = "must be embedded"
	}
	if !isURL(m.Entry) {
		bad["entry"] = "must be a URL"
	}
	if m.Orientation == "" {
		m.Orientation = enum.Portrait
	} else if !m.Orientation.Valid() {
		bad["orientation"] = "must be portrait or landscape"
	}
	if !m.Publisher.Tier.Valid() {
		bad["publisher.tier"] = "must be first-party or partner"
	}

	for _, s := range m.Scopes {
		if !knownScopes[s] {
			// We cannot grant access to data we do not hold, so a scope that is
			// not ours is not a scope.
			bad["scopes"] = "unknown scope: " + s
			break
		}
	}
	for _, p := range m.Permissions {
		if !enum.Permission(p).Valid() {
			bad["permissions"] = "unknown permission: " + p
			break
		}
	}
	for _, r := range m.RedirectURIs {
		if !isURL(r) {
			bad["redirect_uris"] = "must be URLs"
			break
		}
	}

	// The room block is what the platform runs the game under, so every
	// bound is checked here and not when the first room is made.
	players := m.Room.Players
	if players.Min < 1 || players.Min > MaxPlayers {
		bad["room.players.min"] = fmt.Sprintf("must be 1 to %d", MaxPlayers)
	}
	if players.Max < 1 || players.Max < players.Min || players.Max > MaxPlayers {
		bad["room.players.max"] = fmt.Sprintf("must be at least min and at most %d", MaxPlayers)
	}
	if !m.Room.Host.Valid() {
		bad["room.host"] = "must be authoritative or peer"
	}
	if !m.Room.Pace.Valid() {
		bad["room.pace"] = "must be turn or realtime"
	}
	if m.Room.State == "" {
		m.Room.State = enum.StateBlob
	} else if !m.Room.State.Valid() {
		bad["room.state"] = "must be blob or none"
	}
	return bad
}

// isURL is an absolute http or https address with a host. Whether the host
// is one we may reach is the fetcher's rule, not the document's.
func isURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http")
}
