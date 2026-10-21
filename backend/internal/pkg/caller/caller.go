// Package caller is who is making the request, as the authn middleware decided
// and the handler reads. It knows nothing about how a token was minted.
package caller

import (
	"context"
	"strings"
)

// Session is the caller: the token's claims, under the token's own names, so
// that what a handler reads is exactly what was signed.
type Session struct {
	// ID is the token's jti: what the whitelist keys on.
	ID string `json:"jti"`

	// Subject is the person: a player, an admin, or empty while signing in.
	Subject string `json:"sub"`

	// Scope is the kind for our own tokens, and the space-separated permission
	// list for one handed to a game: "identity:read friends:read".
	Scope string `json:"scope"`

	// Kind names the sort of token when Scope is a permission list: game or room.
	Kind string `json:"kind,omitempty"`

	// Audience is the one game a token was minted for, or empty.
	Audience Audience `json:"aud,omitempty"`

	// Phone is set only while signing in, before there is anyone to point at.
	Phone string `json:"phone,omitempty"`

	// Room, Role and Seat are a room ticket's: which room, what the player is
	// there (host, player, spectator) and where they sit.
	Room string `json:"room,omitempty"`
	Role string `json:"role,omitempty"`
	Seat int    `json:"seat,omitempty"`

	// Roles are an admin's, from the identity provider: this service's only,
	// prefix already stripped. Never on the wire from here.
	Roles []string `json:"-"`

	// Session is the device. Signing out revokes the session, not the token.
	Session string `json:"sid,omitempty"`

	// Live says a person was present when this was minted. A refresh cannot
	// set it.
	Live bool `json:"live,omitempty"`
}

// Is reports whether the token is of the given kind.
func (s *Session) Is(kind string) bool {
	if s.Kind != "" {
		return s.Kind == kind
	}
	return s.Scope == kind
}

// HasRole reports whether the caller holds a role: a room ticket's single role,
// or one of an admin's.
func (s *Session) HasRole(role string) bool {
	if s.Role == role {
		return true
	}
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Has reports whether a space-separated permission list contains one permission.
func (s *Session) Has(permission string) bool {
	for _, p := range strings.Fields(s.Scope) {
		if p == permission {
			return true
		}
	}
	return false
}

// Game is the one game a token was minted for, or empty.
func (s *Session) Game() string {
	if len(s.Audience) == 1 {
		return s.Audience[0]
	}
	return ""
}

type ctxKey struct{}

// NewContext is for the middleware alone: a handler reads, it never writes.
func NewContext(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext is the caller, or nil on a public operation.
func FromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKey{}).(*Session)
	return s
}
