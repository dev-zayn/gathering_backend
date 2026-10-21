// Package ids makes the prefixed identifiers the API hands out, ply_7f3k9d2m8q1x4b6n.
// The prefix is part of the value, so a player id is never a room id.
package ids

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

const (
	Player       = "ply"
	Admin        = "adm"
	Game         = "gam"
	Room         = "room"
	Notification = "ntf"
	Session      = "ses"
	Device       = "dev"
	Code         = "cod"
	Refresh      = "rft"
	Token        = "tok"
	Audit        = "aud"
	RoomEvent    = "rev"
)

var enc = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// New returns prefix + "_" + 16 random lowercase base32 characters, 80 bits.
func New(prefix string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic("ids: no randomness available: " + err.Error())
	}
	return prefix + "_" + enc.EncodeToString(b)
}

// Is reports whether id carries the given prefix. Handlers use it to reject a
// well-formed id of the wrong kind before it reaches the database.
func Is(prefix, id string) bool {
	return strings.HasPrefix(id, prefix+"_") && len(id) == len(prefix)+1+16
}
