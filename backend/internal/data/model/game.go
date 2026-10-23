package model

// What a game is, with no idea of where it is kept.

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Listing is the store copy: the words a manifest does not carry, and the
// one switch that puts a game at the top of the store. Every field is
// optional — only what is set is written. One language: the console
// translates its own chrome, the data says one thing.
type Listing struct {
	Tagline     *string
	Description *string
	Category    *string
	Featured    *bool
}

// Reach is how far a game has got: how many players hold it in their
// library, and when the last one added it.
type Reach struct {
	GameID  string
	Players int
	LastAt  *time.Time
}

type Game struct {
	ID          string `gorm:"primaryKey"`
	Name        string
	Tagline     string
	Description string
	Publisher   string

	// IconKey is the tile picture's place in our bucket; the link a store
	// draws it from is signed on the way out, never stored. Screenshots are
	// keys in the same bucket.
	IconKey     string
	Screenshots Strings

	// Category and Featured are the store's, written by an admin. Featured
	// games lead the store whatever their tier.
	Category enum.GameCategory
	Featured bool

	// Orientation and Splash are the manifest's: how the frame opens.
	Orientation enum.Orientation
	Splash      bool

	Version string
	Listing enum.Listing

	// FirstParty is the tier, decided at registration and never by a re-read.
	FirstParty bool
	Tier       enum.Tier

	// Entry is where the game opens, in a frame. Manifest is the document as
	// fetched, kept whole; ManifestURL is where it was read from.
	ManifestURL string
	Entry       string
	Manifest    Raw
	FetchedAt   *time.Time

	// Held is a re-read that asked for more than the manifest above — new
	// scopes, other permissions, another room shape — kept whole until an
	// admin accepts it. HeldAt set is what says one is waiting; Held itself
	// reads as {} when there is none.
	Held    Raw
	HeldURL string
	HeldAt  *time.Time

	// Scopes is what the game's server may read, by key. Permissions is what
	// the app asks the phone for on the game's behalf. An admin reads both
	// before publishing; a player is never asked.
	Scopes      Strings
	Permissions Strings

	// Room is the manifest's room block, as one column: the bounds a room
	// for this game is made under.
	Room RoomBlock

	// SecretHash is the game's client secret, hashed. Never the secret itself.
	SecretHash string

	// RedirectURIs is where the authorization code may be delivered. Empty
	// means any path on the entry's origin.
	RedirectURIs Strings `gorm:"column:redirect_uris"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Game) TableName() string { return "games" }

// Scope is one key of the vocabulary a game may ask for.
type Scope struct {
	Key string `gorm:"primaryKey"`

	// Elevated scopes need a person present and never survive a renewal.
	// Nothing is yet; the rule exists so the first does not arrive as a
	// surprise.
	Elevated bool
}

func (Scope) TableName() string { return "scopes" }

// ModeOf is how a game opens. There is one mode and the row does not store
// it; the launch answers it so the app reads one field, not a rule.
func ModeOf(Game) enum.Mode { return enum.ModeEmbedded }
