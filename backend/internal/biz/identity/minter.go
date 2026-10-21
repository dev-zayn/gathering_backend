package identity

import (
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// GameClaims is what goes into a game's access token. Short on purpose: no
// session, no phone, no other game's scope, no profile — the bridge serves
// those. RoomID is set when the game was opened into a room.
type GameClaims struct {
	GameID   string
	PlayerID string
	RoomID   string
	Scopes   []string
	Live     bool
}

// RoomClaims is a room ticket: one player, in one room, for one game's own
// server to verify through the JWKS. It opens nothing on this API.
type RoomClaims struct {
	GameID   string
	PlayerID string
	RoomID   string
	Role     string
	Seat     int
}

// Minter signs the tokens sign-in hands out. Nothing above this cares that they
// are JWTs.
type Minter interface {
	MintOTP(phone string, ttl time.Duration) (string, error)
	MintSignup(phone string, ttl time.Duration) (string, error)
	MintSession(playerID, sessionID string, live bool, ttl time.Duration) (token, jti string, err error)

	// MintGame is the credential a game's backend holds: one game's audience,
	// and the intersection of scopes the caller already decided.
	MintGame(in GameClaims, ttl time.Duration) (token, jti string, err error)

	// MintRoom is a room ticket, addressed to the game whose room it is.
	MintRoom(in RoomClaims, ttl time.Duration) (token, jti string, err error)

	// MintAdmin is the console's session: ours, made once the identity provider
	// has said who the person is and which of our roles they hold.
	MintAdmin(adminID string, roles []string, ttl time.Duration) (token, jti string, err error)
}

type jwtMinter struct{ s *token.Signer }

// NewMinter wraps the one signer. It holds the private half of the key, so this
// is the only place a token can be produced at all; everywhere else verifies.
func NewMinter(s *token.Signer) Minter { return &jwtMinter{s: s} }

// MintOTP carries the phone. There is no one to point at yet.
func (m *jwtMinter) MintOTP(phone string, ttl time.Duration) (string, error) {
	t, _, err := m.s.Mint(token.Claims{Scope: caller.ScopeOTP, Phone: phone}, ttl)
	return t, err
}

// MintSignup says the number is proven but has no record behind it yet. It is
// the only thing that lets a stranger create a player.
func (m *jwtMinter) MintSignup(phone string, ttl time.Duration) (string, error) {
	t, _, err := m.s.Mint(token.Claims{Scope: caller.ScopeSignup, Phone: phone}, ttl)
	return t, err
}

// MintSession returns the token and its id. The id is what the whitelist keys
// on, which is how a session is revoked without waiting for it to expire.
func (m *jwtMinter) MintSession(playerID, sessionID string, live bool, ttl time.Duration) (string, string, error) {
	c := token.Claims{Scope: caller.ScopeSession, Session: sessionID, Live: live}
	c.Subject = playerID
	return m.s.Mint(c, ttl)
}

// MintGame signs the credential a game's backend keeps. The audience is the one
// game, so a token leaked to another partner is not a token there.
func (m *jwtMinter) MintGame(in GameClaims, ttl time.Duration) (string, string, error) {
	c := token.Claims{
		Scope: strings.Join(in.Scopes, " "),
		Kind:  caller.ScopeGame,
		Room:  in.RoomID,
		Live:  in.Live,
	}
	c.Subject = in.PlayerID
	c.Audience = []string{in.GameID}
	return m.s.Mint(c, ttl)
}

// MintRoom signs a ticket the game's server reads: who, which room, what they
// are there and where they sit. Scope and kind both say room, so a reader of
// either sees the same thing.
func (m *jwtMinter) MintRoom(in RoomClaims, ttl time.Duration) (string, string, error) {
	c := token.Claims{
		Scope: caller.ScopeRoom,
		Kind:  caller.ScopeRoom,
		Room:  in.RoomID,
		Role:  in.Role,
		Seat:  in.Seat,
	}
	c.Subject = in.PlayerID
	c.Audience = []string{in.GameID}
	return m.s.Mint(c, ttl)
}

// MintAdmin returns the token and its id, which the whitelist keys on: signing
// out and sitting idle both end it there, before it expires.
func (m *jwtMinter) MintAdmin(adminID string, roles []string, ttl time.Duration) (string, string, error) {
	c := token.Claims{Scope: caller.ScopeAdmin, Roles: roles}
	c.Subject = adminID
	return m.s.Mint(c, ttl)
}
