package model

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Session is one signed-in device. It lives as long as the player keeps using
// it: the refresh token is long, and every refresh moves LastSeenAt.
type Session struct {
	ID         string `gorm:"primaryKey"`
	PlayerID   string
	Device     string // what the app said about itself, for the sessions list
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

func (Session) TableName() string { return "sessions" }

func (s *Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// HashRefresh is what the database holds. A refresh token is a password by
// another name, so the plaintext is shown once and stored nowhere.
func HashRefresh(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
