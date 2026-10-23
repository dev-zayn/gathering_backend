package rooms

import (
	"crypto/rand"
	"strings"
	"unicode"

	"github.com/jamaa/gathering/backend/internal/data/model"
)

// newCode draws six symbols from the alphabet, each uniformly: a byte that
// would favour the first symbols is drawn again rather than folded over.
func (s *Service) newCode() string {
	n := len(s.alphabet)
	limit := 256 - 256%n
	out := make([]byte, 0, model.RoomCodeLength)
	buf := make([]byte, 16)
	for len(out) < model.RoomCodeLength {
		if _, err := rand.Read(buf); err != nil {
			panic("rooms: no randomness available: " + err.Error())
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, s.alphabet[int(b)%n])
			if len(out) == model.RoomCodeLength {
				break
			}
		}
	}
	return string(out)
}

// maxTyped is the longest thing worth reading as a code. Beyond it there is
// no code in there, however many spaces and dashes.
const maxTyped = 64

// normalizeCode is what a player meant: case does not count, and neither do
// the spaces and dashes a code is read out with ("k7m-2qx").
func normalizeCode(typed string) string {
	if len(typed) > maxTyped {
		return ""
	}
	var b strings.Builder
	for _, r := range typed {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// validCode is six symbols of the alphabet. Anything else cannot be a room.
func (s *Service) validCode(code string) bool {
	if len(code) != model.RoomCodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(s.alphabet, rune(code[i])) {
			return false
		}
	}
	return true
}
