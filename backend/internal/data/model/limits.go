package model

import "unicode/utf8"

// The widths the columns were made with. Checked here, on the way in, so a
// value that would not fit is refused as a field error rather than failing
// as a database one. The migrations name the same numbers.
const (
	MaxName     = 120 // a person, an admin, a publisher
	MaxNickname = 20
	MaxHandle   = 24
	MaxBio      = 120
	MaxAvatar   = 255 // an avatar key
	MaxTagline  = 160
	MaxLink     = 2048
	// MaxNotice is a console message's text: what a phone shows in full.
	MaxNotice  = 1000
	MaxGameID  = 64
	MaxVersion = 32
	MaxEmail   = 254
)

// What a game's server may put in a notification (docs/FRIENDS.md). The
// columns are text; these are the contract's, refused on the way in as
// field errors.
const (
	MaxTitle = 60
	MaxBody  = 160
	MaxKey   = 64 // the idempotency key
)

// TooLong counts runes, not bytes: an Arabic name is two bytes a letter and
// still one name.
func TooLong(s string, max int) bool { return utf8.RuneCountInString(s) > max }
