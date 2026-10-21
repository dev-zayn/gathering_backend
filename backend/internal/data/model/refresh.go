package model

import "time"

// Refresh is one issued refresh token, by its hash. Rotating leaves the old row
// behind on purpose: that is what makes a replay visible.
type Refresh struct {
	Hash      string `gorm:"primaryKey"`
	SessionID string
	CreatedAt time.Time
	UsedAt    *time.Time
}

func (Refresh) TableName() string { return "refresh_tokens" }

// Spent is true once the token has been exchanged. A spent token arriving again
// is the signal that a copy is loose.
func (r *Refresh) Spent() bool { return r.UsedAt != nil }
