package model

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"net/url"
	"strings"
)

// NewSecret is a game's half of its credentials: shown once at registration and
// stored only as a hash.
func NewSecret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("model: no randomness available: " + err.Error())
	}
	enc := base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
	return "sec_" + enc.EncodeToString(b)
}

func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SecretMatches compares in constant time. A timing difference here would leak
// the secret one byte at a time.
func SecretMatches(given, hash string) bool {
	if hash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashSecret(given)), []byte(hash)) == 1
}

// RedirectAllowed decides where a game may be sent back to: an explicit list,
// or the entry's own origin. An open redirect here gives away a player's code.
func (g *Game) RedirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}

	if len(g.RedirectURIs) > 0 {
		for _, allowed := range g.RedirectURIs {
			if allowed == raw {
				return true
			}
		}
		return false
	}

	entry, err := url.Parse(g.Entry)
	if err != nil || entry.Host == "" {
		return false
	}
	return strings.EqualFold(entry.Scheme, u.Scheme) && strings.EqualFold(entry.Host, u.Host)
}
