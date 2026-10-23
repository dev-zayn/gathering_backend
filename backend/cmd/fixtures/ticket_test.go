package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

const (
	game   = "fixture.xo"
	player = "ply_7f3k9d2m8q1x4b6n"
	room   = "room_7f3k9d2m8q1x4b6n"
)

// key is one signing key of the api's: the private half, the signer the api
// mints with, and its kid.
type key struct {
	priv   *ecdsa.PrivateKey
	signer *token.Signer
	minter identity.Minter
}

func newKey(t *testing.T) *key {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := token.NewSigner(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "https://api.gathering.test")
	if err != nil {
		t.Fatal(err)
	}
	return &key{priv: priv, signer: signer, minter: identity.NewMinter(signer)}
}

func (k *key) kid() string { return k.signer.JWKS()[0].Kid }

// ticket is a room ticket as the api mints it: the host, in seat 0, in the
// room's second round.
func (k *key) ticket(t *testing.T, aud string, ttl time.Duration) string {
	t.Helper()
	tok, _, err := k.minter.MintRoom(identity.RoomClaims{GameID: aud, PlayerID: player, RoomID: room, Role: "host", Seat: 0, Round: 2}, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// api serves the JWKS of whichever key is current, counting the fetches;
// down makes it answer 503.
type api struct {
	mu      sync.Mutex
	current *key
	down    bool
	fetches atomic.Int32
	server  *httptest.Server
}

func newAPI(t *testing.T, k *key) *api {
	t.Helper()
	a := &api{current: k}
	a.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		a.fetches.Add(1)
		if a.down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": a.current.signer.JWKS()})
	}))
	t.Cleanup(a.server.Close)
	return a
}

func (a *api) rotate(k *key) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.current = k
}

func (a *api) setDown(down bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.down = down
}

// verifier is the fixture's check against this api, on a clock the test
// moves.
func (a *api) verifier(clock *time.Time) *tickets {
	v := newTickets(a.server.URL+"/.well-known/jwks.json", game, a.server.Client())
	if clock != nil {
		v.now = func() time.Time { return *clock }
	}
	return v
}

func reasonOf(err error) string {
	var no *refused
	if errors.As(err, &no) {
		return no.reason
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// A ticket the api minted for this game verifies, and says who sits where.
// The key set is fetched once and kept.
func TestAGoodTicketVerifies(t *testing.T) {
	k := newKey(t)
	a := newAPI(t, k)
	v := a.verifier(nil)

	before := time.Now()
	c, err := v.verify(context.Background(), k.ticket(t, game, 10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != player || c.Room != room || c.Role != "host" || c.Seat == nil || *c.Seat != 0 ||
		c.Round == nil || *c.Round != 2 ||
		len(c.Audience) != 1 || c.Audience[0] != game || c.Kind != "room" || c.ExpiresAt == nil ||
		c.ExpiresAt.Before(before.Add(9*time.Minute)) {
		t.Fatalf("claims = %+v", c)
	}
	if _, err := v.verify(context.Background(), k.ticket(t, game, 10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := a.fetches.Load(); n != 1 {
		t.Fatalf("the key set was fetched %d times", n)
	}
}

// Every way a ticket is not one for this game's server, and the reason given.
func TestATicketIsRefused(t *testing.T) {
	k, stranger := newKey(t), newKey(t)
	a := newAPI(t, k)
	v := a.verifier(nil)

	good := k.ticket(t, game, 10*time.Minute)
	parts := strings.Split(good, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(payload), `"role":"host"`, `"role":"player"`, 1)
	if changed == string(payload) {
		t.Fatalf("no role to change in %s", payload)
	}
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(changed)) + "." + parts[2]

	// Another key's signature, under the api's own kid.
	forgery := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": player, "aud": []string{game}, "room": room, "role": "host", "seat": 0,
		"scope": "room", "kind": "room", "exp": time.Now().Add(10 * time.Minute).Unix(),
	})
	forgery.Header["kid"] = k.kid()
	forged, err := forgery.SignedString(stranger.priv)
	if err != nil {
		t.Fatal(err)
	}

	gameToken, _, err := k.minter.MintGame(identity.GameClaims{GameID: game, PlayerID: player, RoomID: room, Scopes: []string{"identity:read"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, token, reason string }{
		{"not a JWT", "nope", reasonMalformed},
		{"another game's ticket", k.ticket(t, "fixture.tap", 10*time.Minute), reasonAudience},
		{"an expired ticket", k.ticket(t, game, -time.Minute), reasonExpired},
		{"a game token for the same game", gameToken, reasonKind},
		{"a changed claim", tampered, reasonBadSignature},
		{"another key under the api's kid", forged, reasonBadSignature},
		{"a key the api never published", stranger.ticket(t, game, 10*time.Minute), reasonUnknownKey},
	} {
		if _, err := v.verify(context.Background(), tc.token); reasonOf(err) != tc.reason {
			t.Errorf("%s: got %q, want %q", tc.name, reasonOf(err), tc.reason)
		}
	}
	if n := a.fetches.Load(); n != 1 {
		t.Fatalf("the key set was fetched %d times", n)
	}
}

// A kid the cache does not have fetches the set again — not within ten
// seconds of the last fetch, so made-up kids cannot hammer the api — and a
// set older than five minutes is fetched again whatever the kid.
func TestARotatedKeyIsPickedUp(t *testing.T) {
	old, rotated := newKey(t), newKey(t)
	a := newAPI(t, old)
	clock := time.Now()
	v := a.verifier(&clock)

	if _, err := v.verify(context.Background(), old.ticket(t, game, 10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	a.rotate(rotated)
	next := rotated.ticket(t, game, 10*time.Minute)
	if _, err := v.verify(context.Background(), next); reasonOf(err) != reasonUnknownKey || a.fetches.Load() != 1 {
		t.Fatalf("within ten seconds: %v, %d fetches", err, a.fetches.Load())
	}
	clock = clock.Add(11 * time.Second)
	if _, err := v.verify(context.Background(), next); err != nil || a.fetches.Load() != 2 {
		t.Fatalf("after ten seconds: %v, %d fetches", err, a.fetches.Load())
	}
	if _, err := v.verify(context.Background(), next); err != nil || a.fetches.Load() != 2 {
		t.Fatalf("cached: %v, %d fetches", err, a.fetches.Load())
	}
	clock = clock.Add(5 * time.Minute)
	if _, err := v.verify(context.Background(), next); err != nil || a.fetches.Load() != 3 {
		t.Fatalf("after five minutes: %v, %d fetches", err, a.fetches.Load())
	}
}

// The route: 200 with the claims, 401 with the reason, 400 without a
// token, 502 when the api's keys cannot be read.
func TestTheRouteAnswers(t *testing.T) {
	k := newKey(t)
	a := newAPI(t, k)
	p := &partner{tickets: a.verifier(nil)}

	post := func(body string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		p.verifyTicket(w, httptest.NewRequest(http.MethodPost, "/partner/verify-ticket", bytes.NewBufferString(body)))
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v", w.Body, err)
		}
		return w.Code, out
	}

	code, out := post(`{"token":"` + k.ticket(t, game, 10*time.Minute) + `"}`)
	claims, _ := out["claims"].(map[string]any)
	aud, _ := claims["aud"].([]any)
	if code != 200 || out["valid"] != true || claims["sub"] != player || claims["room"] != room ||
		claims["role"] != "host" || claims["seat"] != float64(0) || claims["round"] != float64(2) || len(aud) != 1 || aud[0] != game ||
		claims["exp"].(float64) < float64(time.Now().Unix()) {
		t.Fatalf("a good ticket: %d %v", code, out)
	}
	if code, out := post(`{"token":"` + k.ticket(t, game, -time.Minute) + `"}`); code != 401 || out["valid"] != false || out["error"] != "expired" {
		t.Fatalf("an expired ticket: %d %v", code, out)
	}
	if code, out := post(`{}`); code != 400 || out["valid"] != false || out["error"] != "invalid_request" {
		t.Fatalf("no token: %d %v", code, out)
	}

	w := httptest.NewRecorder()
	p.verifyTicket(w, httptest.NewRequest(http.MethodGet, "/partner/verify-ticket", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("a GET: %d", w.Code)
	}

	a.setDown(true)
	p.tickets = a.verifier(nil)
	if code, out := post(`{"token":"` + k.ticket(t, game, 10*time.Minute) + `"}`); code != 502 || out["error"] != "jwks_unavailable" {
		t.Fatalf("the api down: %d %v", code, out)
	}
}
