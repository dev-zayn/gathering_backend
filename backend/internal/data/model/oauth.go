package model

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"time"
)

// Code is the one-time authorization code: sixty seconds, because it only has
// to survive a redirect, and single use, because a reusable one is worth
// stealing.
type Code struct {
	Hash     string `gorm:"primaryKey"`
	GameID   string
	PlayerID string

	// RoomID is the room the game was opened into, or empty. It rides on the
	// code so the token minted from it knows which room the player is in.
	RoomID string

	// Scopes is what was decided at mint time: the game's grant, cut to what
	// a person present may hold. No game is trusted to enforce it.
	Scopes Strings
	Live   bool

	// Challenge and RedirectURI are fixed when the code is minted and must
	// match when it is spent. Both are empty for the in-app launch.
	Challenge   string
	RedirectURI string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	UsedAt      *time.Time
}

func (Code) TableName() string { return "oauth_codes" }

func (c *Code) Spendable(now time.Time) bool {
	return c.UsedAt == nil && now.Before(c.ExpiresAt)
}

// GameToken is the refresh token a game's backend keeps. No expiry and no
// revoked flag: the game's place in the player's library is the lifetime.
type GameToken struct {
	Hash      string `gorm:"primaryKey"`
	GameID    string
	PlayerID  string
	Scopes    Strings
	CreatedAt time.Time
	UsedAt    *time.Time
}

func (GameToken) TableName() string { return "game_tokens" }

// HashCredential is what the database holds for anything that is a password
// by another name — a code, a refresh token. The plaintext is shown once.
func HashCredential(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// VerifyPKCE checks the verifier the game kept against the challenge the
// browser sent. S256 only: "plain" is a challenge that is its own answer.
func VerifyPKCE(challenge, verifier string) bool {
	if challenge == "" {
		// No challenge was set, so there is nothing to prove. This only happens
		// for the in-app launch, where the code never leaves the app.
		return verifier == ""
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(challenge)) == 1
}

// Grant is everything that has to be true before a credential is minted, in
// one answer, so the check and the mint cannot drift apart.
type Grant struct {
	Game      Game
	InLibrary bool
	Scopes    []Scope
}

// Effective is the cut, once, here: an elevated scope needs a person present,
// and a renewal has nobody there.
func (g *Grant) Effective(live bool) []string {
	out := make([]string, 0, len(g.Scopes))
	for _, s := range g.Scopes {
		if s.Elevated && !live {
			continue
		}
		out = append(out, s.Key)
	}
	return out
}
