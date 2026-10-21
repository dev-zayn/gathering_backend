package model

import "time"

// Library is one game a player has: opened at least once. Nothing was agreed
// to; the row is what puts the tile on their shelf and lets a token be minted
// for it. Removing it is the revocation.
type Library struct {
	PlayerID string `gorm:"primaryKey"`
	GameID   string `gorm:"primaryKey"`

	// AddedAt is the first open. LastPlayedAt and Plays move on every open
	// after it, so the shelf can be sorted by what was played last.
	AddedAt      time.Time
	LastPlayedAt *time.Time
	Plays        int
}

func (Library) TableName() string { return "library" }
