package token

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// A throwaway pair, generated for this test and nowhere else.
const (
	testPriv = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg8f6vP1v0k9tGqUuY
2i7CmvIsG5MpiA0h6bVdPHmYdw2hRANCAAQ0xJ8mQ9uKHNoZKmeC5uJHb+2QSHMe
xKCTM4nSU3xMOMkYnQrPCH+g0lY1jJH3OaQXfLpJKvvGiGuh2mVrFT5r
-----END PRIVATE KEY-----`
)

func signer(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(testPriv, "https://api.gathering.test")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return s
}

func TestMintAndParseRoundTrip(t *testing.T) {
	s := signer(t)

	raw, jti, err := s.Mint(Claims{
		Scope: caller.ScopeSession, Room: "room_1", Role: "host", Live: true,
	}, time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if jti == "" {
		t.Fatal("mint returned no jti; nothing could ever be revoked")
	}

	got, err := s.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Room != "room_1" || got.Role != "host" || !got.Live {
		t.Fatalf("claims did not survive the round trip: %+v", got)
	}
	if got.ID != jti {
		t.Fatalf("jti %q is not the one Mint reported (%q)", got.ID, jti)
	}
	if got.Issuer != "https://api.gathering.test" {
		t.Fatalf("issuer = %q", got.Issuer)
	}
}

func TestParseRefusesExpired(t *testing.T) {
	s := signer(t)

	raw, _, err := s.Mint(Claims{Scope: caller.ScopeOTP, Phone: "+9647700000001"}, -time.Second)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := s.Parse(raw); err != ErrExpiredToken {
		t.Fatalf("an expired token parsed as %v, want %v", err, ErrExpiredToken)
	}
}

// A token signed by somebody else is the failure that matters most: it is what
// an attacker would forge if the signature were not checked.
func TestParseRefusesAnotherKey(t *testing.T) {
	other, err := NewSigner(generateKey(t), "https://api.gathering.test")
	if err != nil {
		t.Fatalf("second signer: %v", err)
	}

	raw, _, err := other.Mint(Claims{Scope: caller.ScopeSession, Room: "room_evil"}, time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := signer(t).Parse(raw); err == nil {
		t.Fatal("a token signed by another key was accepted")
	}
}

// generateKey is a fresh P-256 key in the PEM form the config carries, so the
// test exercises the same parsing path a deployment does.
func generateKey(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestParseRefusesRubbish(t *testing.T) {
	s := signer(t)
	for _, raw := range []string{"", "not.a.token", "a.b.c", strings.Repeat("x", 200)} {
		if _, err := s.Parse(raw); err == nil {
			t.Fatalf("%q was accepted as a token", raw)
		}
	}
}

// Is is what keeps an otp token out of a session endpoint, and a room ticket
// out of a game's. Kind wins because an app's scope is a permission list.
func TestClaimsIs(t *testing.T) {
	cases := []struct {
		name string
		c    Claims
		want string
		is   bool
	}{
		{"plain session", Claims{Scope: caller.ScopeSession}, caller.ScopeSession, true},
		{"otp is not a session", Claims{Scope: caller.ScopeOTP}, caller.ScopeSession, false},
		{"game named by kind", Claims{Kind: caller.ScopeGame, Scope: "identity:read friends:read"}, caller.ScopeGame, true},
		{"room is not a game", Claims{Kind: caller.ScopeRoom, Scope: "identity:read"}, caller.ScopeGame, false},
		{"permission list alone is nothing", Claims{Scope: "identity:read"}, caller.ScopeGame, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Is(tc.want); got != tc.is {
				t.Fatalf("Is(%q) = %v, want %v", tc.want, got, tc.is)
			}
		})
	}
}

func TestClaimsHas(t *testing.T) {
	c := Claims{Kind: caller.ScopeGame, Scope: "identity:read friends:read"}

	if !c.Has("friends:read") {
		t.Fatal("a granted scope read as missing")
	}
	if c.Has("notify:send") {
		t.Fatal("a scope nobody granted read as present")
	}
	// The one that would matter: a prefix is not a scope.
	if c.Has("friends") || c.Has("read") {
		t.Fatal("a substring of a scope was accepted as one")
	}
	if (&Claims{Scope: caller.ScopeSession}).Has("friends:read") {
		t.Fatal("a session token, whose scope is a kind, carried a permission")
	}
}

func TestAppIsTheSingleAudience(t *testing.T) {
	if got := (&Claims{}).Game(); got != "" {
		t.Fatalf("no audience gave %q", got)
	}
	c := Claims{}
	c.Audience = []string{"rashid.shipping"}
	if got := c.Game(); got != "rashid.shipping" {
		t.Fatalf("App() = %q", got)
	}
	c.Audience = []string{"a", "b"}
	if got := c.Game(); got != "" {
		t.Fatalf("two audiences named one app: %q", got)
	}
}

func TestBearer(t *testing.T) {
	if v, ok := Bearer("Bearer abc"); !ok || v != "abc" {
		t.Fatalf("Bearer(%q) = %q, %v", "Bearer abc", v, ok)
	}
	for _, h := range []string{"", "abc", "Basic abc", "Bearer", "Bearer "} {
		if _, ok := Bearer(h); ok {
			t.Fatalf("%q was read as a bearer token", h)
		}
	}
}

func TestJWKSPublishesOnlyPublicHalf(t *testing.T) {
	keys := signer(t).JWKS()
	if len(keys) != 1 {
		t.Fatalf("JWKS has %d keys, want 1", len(keys))
	}
	if keys[0].Kid == "" || keys[0].X == "" || keys[0].Y == "" {
		t.Fatalf("JWKS entry is incomplete: %+v", keys[0])
	}
	if keys[0].Crv != "P-256" || keys[0].Alg != "ES256" {
		t.Fatalf("JWKS says %s/%s", keys[0].Crv, keys[0].Alg)
	}
}
