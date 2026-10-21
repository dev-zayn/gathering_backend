package model

import (
	"strings"
	"time"
)

// Admin is somebody who has opened the console: the name behind an id that
// shows up in the logs. The identity provider owns who they are and what they
// may hold; this is only a name list, written from what it says about them.
type Admin struct {
	// ID is ours, adm_…: what every audit line names.
	ID string `gorm:"primaryKey"`
	// Subject is the provider's user id, the sub of their token; dev:<name> for
	// a laptop's dev admin. Only the console reads it, to find the row a
	// sign-in belongs to.
	Subject string `gorm:"column:sso_subject"`
	Name    string
	Email   string

	CreatedAt time.Time
	// SeenAt is the last sign-in. Nil until the first one has been stamped,
	// which is how the console tells a new name from a returning one.
	SeenAt *time.Time
}

func (Admin) TableName() string { return "admins" }

// NormalizeEmail is the one spelling the column holds. Trimmed and lower-cased
// rather than validated: a colleague's address is not ours to reject.
func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
