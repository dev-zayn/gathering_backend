// Package ssotest is a fake OIDC provider for tests: discovery and signing keys
// on a local server, and a key to sign tokens with. Only _test.go files import
// it.
package ssotest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	ClientID = "console-gathering"
	Audience = "gathering-api"
	KeyID    = "key-1"
)

// Provider is the fake. Its tokens are a person's: one subject, one name, one
// address, and whatever roles a test puts in.
type Provider struct {
	URL string
	Key *rsa.PrivateKey

	t    *testing.T
	down atomic.Bool
}

// New starts the fake and stops it when the test ends.
func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{Key: key, t: t}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if p.down.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.URL, "jwks_uri": p.URL + "/keys",
			"authorization_endpoint":                p.URL + "/authorize",
			"token_endpoint":                        p.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		if p.down.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		enc := base64.RawURLEncoding
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": KeyID,
			"n": enc.EncodeToString(key.N.Bytes()), "e": enc.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

// Down makes discovery and the keys answer 503, the way a provider mid-restart
// does.
func (p *Provider) Down(down bool) { p.down.Store(down) }

// Claims is a good token for a person: this issuer, our audience next to
// another app's, five minutes to live, a name and an address. No roles claim
// yet: a test puts one where it is testing, with Put.
func (p *Provider) Claims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": p.URL, "sub": "user-1",
		"aud":                []string{"console-other", Audience},
		"iat":                now.Unix(),
		"exp":                now.Add(5 * time.Minute).Unix(),
		"name":               "Sara Ali",
		"email":              "sara@jamaa.example",
		"preferred_username": "sara",
	}
}

// Token is a good token holding roles, at "roles" as a list: the common case,
// for tests that are about something else.
func (p *Provider) Token(roles ...string) string {
	return p.Mint(Put(p.Claims(), "roles", roles))
}

// Put writes value at a dotted path, "roles" or "realm_access.roles", making
// the objects on the way. It returns claims, so a call can sit inside Mint.
func Put(claims map[string]any, path string, value any) map[string]any {
	obj := claims
	parts := strings.Split(path, ".")
	for _, key := range parts[:len(parts)-1] {
		next, ok := obj[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			obj[key] = next
		}
		obj = next
	}
	obj[parts[len(parts)-1]] = value
	return claims
}

// Keyed is roles as an object whose keys are the names, the shape some
// providers send instead of a list. What each key holds is never read.
func Keyed(roles ...string) map[string]any {
	m := map[string]any{}
	for _, r := range roles {
		m[r] = map[string]string{"org-1": "jamaa.example"}
	}
	return m
}

// Mint signs claims with the fake's key, as the provider would.
func (p *Provider) Mint(claims map[string]any) string {
	p.t.Helper()
	return Sign(p.t, p.Key, map[string]string{"alg": "RS256", "typ": "JWT", "kid": KeyID}, claims)
}

// Sign is Mint with any key and header, for the tokens that should fail.
func Sign(t *testing.T, key *rsa.PrivateKey, head map[string]string, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(head)
	b, _ := json.Marshal(claims)
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(b)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + enc.EncodeToString(sig)
}
