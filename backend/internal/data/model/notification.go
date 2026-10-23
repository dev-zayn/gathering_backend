package model

// What a player's inbox holds.

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Notification is one line of a player's inbox. Key is the idempotency key,
// per recipient and game: a raise with a key already there updates that row
// in place, brings it back to the top and marks it unread.
type Notification struct {
	ID       string `gorm:"primaryKey"`
	PlayerID string
	Kind     enum.NoticeKind

	// ActorID is the player who caused it, when one did; GameID the game it
	// is from or about. Nil for none.
	ActorID *string
	GameID  *string

	Title    string
	Body     string
	DeepLink string

	// Data is what the app needs to act on it: {room, code, game} for an
	// invite, {player} for a request. Always an object.
	Data Raw
	Key  *string

	// CreatedAt is when it was raised, or raised again; ReadAt nil is unread.
	CreatedAt time.Time
	ReadAt    *time.Time
}

func (Notification) TableName() string { return "notifications" }

// Announcement is an admin's message to many players at once: one system
// notification per recipient, written in the same transaction as this row.
// Audience is the audience as the console sent it; Reach is how many
// notifications the send wrote.
type Announcement struct {
	ID       string `gorm:"primaryKey"`
	AdminID  string
	Audience Raw
	Title    string
	Body     string

	// GameID is the game the notification opens in the app, its store page;
	// nil for none.
	GameID *string

	Reach     int
	CreatedAt time.Time
}

func (Announcement) TableName() string { return "announcements" }
