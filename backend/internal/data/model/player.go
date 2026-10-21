package model

// What a person is: a phone, a name to show, a handle to be found by, and the
// language they read in.

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Player is a person. A phone belongs to exactly one record, which is why a
// number is enough to sign in. It is a pointer because a player who arrives
// through a provider need not have one.
type Player struct {
	ID       string  `gorm:"primaryKey"`
	Phone    *string // canonical +9647XXXXXXXXX
	Nickname string

	// Handle is the @name friends search by: unique, lowercase, letters digits
	// and _. HandleChangedAt is when it last moved; changing it has a cooldown.
	Handle          string
	HandleChangedAt *time.Time

	// AvatarKey is avatars/<id>.<ext> in object storage, or a bundled avatar's
	// id. Empty is the default picture.
	AvatarKey string
	Bio       string

	Disabled   bool
	Language   enum.Language
	LastSeenAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (Player) TableName() string { return "players" }

// CanSignIn is false for someone an admin has switched off. They keep their
// record, their history and their phone; they just cannot get a session.
func (p *Player) CanSignIn() bool { return !p.Disabled }

// Identity is a sign-in through a provider: the seam SSO lands in. Empty until
// a provider ships.
type Identity struct {
	Provider  string `gorm:"primaryKey"`
	Subject   string `gorm:"primaryKey"`
	PlayerID  string
	CreatedAt time.Time
}

func (Identity) TableName() string { return "identities" }
