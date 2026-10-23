package tournaments

import (
	"crypto/rand"
	"math/big"
	"strings"
	"unicode"

	"github.com/jamaa/gathering/backend/internal/data/model"
)

// A tournament's code is a room's: six symbols of the same alphabet, read
// the same way, so a player who has typed one has typed the other.
const (
	codeLength   = model.RoomCodeLength
	codeAttempts = 8
	maxTyped     = 64
	maxEntrants  = model.MaxEntrants
)

// newCode draws six symbols from the alphabet, each uniformly.
func (s *Service) newCode() string {
	n := len(s.alphabet)
	limit := 256 - 256%n
	out := make([]byte, 0, codeLength)
	buf := make([]byte, 16)
	for len(out) < codeLength {
		if _, err := rand.Read(buf); err != nil {
			panic("tournaments: no randomness available: " + err.Error())
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, s.alphabet[int(b)%n])
			if len(out) == codeLength {
				break
			}
		}
	}
	return string(out)
}

// normalizeCode is what a player meant: case does not count, and neither do
// the spaces and dashes a code is read out with.
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

// validCode is six symbols of the alphabet.
func (s *Service) validCode(code string) bool {
	if len(code) != codeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(s.alphabet, rune(code[i])) {
			return false
		}
	}
	return true
}

// shuffle is the draw: the list in a uniformly random order, from crypto/rand
// (Fisher–Yates). Nobody can lean on a seed they can predict.
func shuffle[T any](list []T) {
	for i := len(list) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic("tournaments: no randomness available: " + err.Error())
		}
		k := int(j.Int64())
		list[i], list[k] = list[k], list[i]
	}
}

// bracketOrder is the standard seeding of a bracket of p slots (a power of
// two): the seed in each slot, in order, so that seed 1 and seed 2 can only
// meet in the final, 1–4 and 2–3 in the semi-finals, and so on. Pairs of
// slots are the first stage's matches: p = 8 is 1–8, 4–5, 2–7, 3–6.
func bracketOrder(p int) []int {
	order := []int{1}
	for n := 1; n < p; n *= 2 {
		next := make([]int, 0, 2*len(order))
		for _, seed := range order {
			next = append(next, seed, 2*n+1-seed)
		}
		order = next
	}
	return order
}

// bracketSize is the smallest power of two at or above n, and its log: the
// bracket's slots and its stages.
func bracketSize(n int) (slots, stages int) {
	slots, stages = 1, 0
	for slots < n {
		slots *= 2
		stages++
	}
	return slots, stages
}
