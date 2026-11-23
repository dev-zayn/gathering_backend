package model

// The studios that make games, the versions they send on their way, and the
// players who try them first (docs/PUBLISHERS.md).

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Publisher is one studio's sign-in to the console. The password is chosen
// through a one-time link an admin makes, so it never passes through anybody
// else's hands; only its bcrypt hash is kept, and only the link's token's
// sha256.
type Publisher struct {
	ID    string `gorm:"primaryKey"`
	Name  string
	Email string

	PasswordHash string

	SetupHash      string
	SetupExpiresAt *time.Time

	DisabledAt *time.Time
	CreatedBy  string
	CreatedAt  time.Time
	SeenAt     *time.Time
}

func (Publisher) TableName() string { return "publishers" }

// Status is how the console reads the row.
func (p *Publisher) Status() enum.PublisherStatus {
	switch {
	case p.DisabledAt != nil:
		return enum.PublisherDisabled
	case p.PasswordHash == "":
		return enum.PublisherInvited
	default:
		return enum.PublisherActive
	}
}

// The widths of a publisher's and a build's words, as the migration made them.
const (
	MaxBuildNotes   = 2000
	MaxReviewNote   = 2000
	MaxDescription  = 4000
	MaxInviteLabel  = 60
	MinPassword     = 10
	MaxPasswordByte = 72 // bcrypt reads no further
)

// GameBuild is one version of a game on its way to players: the manifest's
// half as the games row keeps it, and the store copy it will have once
// published. A game has at most one open.
type GameBuild struct {
	ID     string `gorm:"primaryKey"`
	GameID string
	Status enum.BuildStatus

	Name         string
	Version      string
	ManifestURL  string
	Entry        string
	Manifest     Raw
	FetchedAt    time.Time
	Orientation  enum.Orientation
	Splash       bool
	Scopes       Strings
	Permissions  Strings
	Room         RoomBlock
	RedirectURIs Strings `gorm:"column:redirect_uris"`

	Tagline     string
	Description string
	Category    enum.GameCategory
	IconKey     string
	Screenshots Strings

	Notes      string
	ReviewNote string
	ReviewedBy string
	ReviewedAt *time.Time

	SubmittedAt *time.Time
	PublishedAt *time.Time
	ClosedAt    *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (GameBuild) TableName() string { return "game_builds" }

// Over is the game as a tester gets it: the live row with this build's
// manifest and store copy laid over it. An empty icon or no screenshots fall
// back to the live game's, so a build that changed only its words still
// draws a tile.
func (b *GameBuild) Over(g Game) Game {
	g.Name = b.Name
	g.Version = b.Version
	g.Entry = b.Entry
	g.ManifestURL = b.ManifestURL
	g.Manifest = b.Manifest
	g.Orientation = b.Orientation
	g.Splash = b.Splash
	g.Scopes = b.Scopes
	g.Permissions = b.Permissions
	g.Room = b.Room
	g.RedirectURIs = b.RedirectURIs
	g.Tagline = b.Tagline
	g.Description = b.Description
	g.Category = b.Category
	if b.IconKey != "" {
		g.IconKey = b.IconKey
	}
	if len(b.Screenshots) > 0 {
		g.Screenshots = b.Screenshots
	}
	return g
}

// Tester is one player trying a game's builds, kept per game.
type Tester struct {
	GameID   string `gorm:"primaryKey"`
	PlayerID string `gorm:"primaryKey"`
	Code     string
	JoinedAt time.Time
}

func (Tester) TableName() string { return "game_testers" }

// TesterInvite is a code a publisher hands out. Typed in the app, it makes a
// tester of whoever typed it, while it lasts.
type TesterInvite struct {
	Code      string `gorm:"primaryKey"`
	GameID    string
	Label     string
	MaxUses   int
	Uses      int
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedBy string
	CreatedAt time.Time
}

func (TesterInvite) TableName() string { return "tester_invites" }

// Live says the code still makes testers at t.
func (i *TesterInvite) Live(t time.Time) bool {
	return i.RevokedAt == nil && t.Before(i.ExpiresAt) && (i.MaxUses == 0 || i.Uses < i.MaxUses)
}

// InviteCodeLength is how many symbols a tester's code has: two more than a
// room's, so the two are never read for each other.
const InviteCodeLength = 8
