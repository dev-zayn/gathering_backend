package main

// The room ticket, verified the way a studio's server does it: no session of
// ours, no call per ticket — the api's public keys, fetched now and then,
// and the claims read off the token itself. docs/ROOMS.md, "The partner
// fixture verifies a ticket", is the contract.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The reasons a ticket is refused, as the answer names them.
const (
	reasonMalformed    = "malformed"
	reasonUnknownKey   = "unknown_key"
	reasonBadSignature = "bad_signature"
	reasonExpired      = "expired"
	reasonNotYetValid  = "not_yet_valid"
	reasonAudience     = "wrong_audience"
	reasonKind         = "wrong_kind"
)

// refused is a ticket that is not one, and why.
type refused struct{ reason string }

func (r *refused) Error() string { return "ticket refused: " + r.reason }

// errJWKS is a key set that could not be fetched: the api's trouble, not the
// ticket's.
var errJWKS = errors.New("jwks unavailable")

// ticketClaims is a room ticket as the api mints it. Round is the room's
// round when it was minted; a ticket from before rounds has none, and reads
// as 0.
type ticketClaims struct {
	jwt.RegisteredClaims
	Scope string `json:"scope"`
	Kind  string `json:"kind"`
	Room  string `json:"room"`
	Role  string `json:"role"`
	Seat  *int   `json:"seat"`
	Round *int   `json:"round"`
}

// tickets checks room tickets against the api's JWKS, kept for ttl. A kid it
// has not seen fetches the set again, no more than once every refetch, so a
// rotated key is picked up without waiting the cache out and a stream of
// made-up kids cannot turn into a stream of fetches.
type tickets struct {
	jwksURL  string
	audience string
	client   *http.Client
	ttl      time.Duration
	refetch  time.Duration
	now      func() time.Time

	mu      sync.Mutex
	keys    map[string]*ecdsa.PublicKey
	fetched time.Time
}

func newTickets(jwksURL, audience string, client *http.Client) *tickets {
	return &tickets{
		jwksURL: jwksURL, audience: audience, client: client,
		ttl: 5 * time.Minute, refetch: 10 * time.Second, now: time.Now,
	}
}

// verify answers the claims of a good ticket. A *refused is the ticket's
// fault; errJWKS is the api's.
func (v *tickets) verify(ctx context.Context, raw string) (*ticketClaims, error) {
	var c ticketClaims
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(v.now),
	)
	_, err := parser.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.key(ctx, kid)
	})
	switch {
	case err == nil:
	case errors.Is(err, errJWKS):
		return nil, errJWKS
	case errors.Is(err, errUnknownKey):
		return nil, &refused{reasonUnknownKey}
	case errors.Is(err, jwt.ErrTokenMalformed):
		return nil, &refused{reasonMalformed}
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return nil, &refused{reasonBadSignature}
	case errors.Is(err, jwt.ErrTokenExpired):
		return nil, &refused{reasonExpired}
	case errors.Is(err, jwt.ErrTokenNotValidYet), errors.Is(err, jwt.ErrTokenUsedBeforeIssued):
		return nil, &refused{reasonNotYetValid}
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return nil, &refused{reasonAudience}
	default:
		// An unexpected algorithm, or claims that are not claims: either
		// way, not a ticket.
		return nil, &refused{reasonMalformed}
	}
	// A game token is signed by the same key and addressed to the same
	// game: only the kind tells a ticket from it.
	if c.Kind != "room" || c.Room == "" || c.Seat == nil {
		return nil, &refused{reasonKind}
	}
	return &c, nil
}

// errUnknownKey is a kid the key set does not have, even freshly fetched.
var errUnknownKey = errors.New("no such key in the JWKS")

// key is the public key a kid names.
func (v *tickets) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	age := v.now().Sub(v.fetched)
	fresh := !v.fetched.IsZero() && age < v.ttl
	if k, ok := v.keys[kid]; ok && fresh {
		return k, nil
	}
	if fresh && age < v.refetch {
		return nil, errUnknownKey
	}
	if err := v.fetch(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", errJWKS, err)
	}
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, errUnknownKey
}

// jwk is one key of the set, as RFC 7517 writes an EC public key.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
}

// fetch reads the key set and keeps the P-256 keys in it, by kid. Called with
// the lock held.
func (v *tickets) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", v.jwksURL, res.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&set); err != nil {
		return err
	}

	keys := make(map[string]*ecdsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" || k.Kid == "" {
			continue
		}
		pub, err := publicKey(k)
		if err != nil {
			return fmt.Errorf("key %s: %w", k.Kid, err)
		}
		keys[k.Kid] = pub
	}
	v.keys, v.fetched = keys, v.now()
	return nil
}

// publicKey builds the P-256 point from the key's coordinates: 0x04, then X
// and Y, each 32 bytes, as the uncompressed encoding writes them.
func publicKey(k jwk) (*ecdsa.PublicKey, error) {
	const size = 32
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, err
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, err
	}
	if len(x) > size || len(y) > size {
		return nil, errors.New("a coordinate is longer than the curve")
	}
	point := make([]byte, 1+2*size)
	point[0] = 4
	copy(point[1+size-len(x):1+size], x)
	copy(point[1+2*size-len(y):], y)
	return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
}

// verifyAnswer is what /partner/verify-ticket says.
type verifyAnswer struct {
	Valid  bool         `json:"valid"`
	Claims *claimsShown `json:"claims,omitempty"`
	Error  string       `json:"error,omitempty"`
}

// claimsShown is who sits where, and in which round, as the ticket says it:
// aud as the ticket carries it, exp in seconds since the epoch.
type claimsShown struct {
	Sub   string   `json:"sub"`
	Room  string   `json:"room"`
	Role  string   `json:"role"`
	Seat  int      `json:"seat"`
	Round int      `json:"round"`
	Aud   []string `json:"aud"`
	Exp   int64    `json:"exp"`
}

// verifyTicket is POST /partner/verify-ticket {token}: 200 with the claims
// of a good ticket, 401 with the reason for any other, 400 for a body with
// no token, 502 when the api's keys cannot be read.
func (p *partner) verifyTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil || in.Token == "" {
		answer(w, http.StatusBadRequest, verifyAnswer{Error: "invalid_request"})
		return
	}

	c, err := p.tickets.verify(r.Context(), in.Token)
	var no *refused
	switch {
	case errors.As(err, &no):
		answer(w, http.StatusUnauthorized, verifyAnswer{Error: no.reason})
	case err != nil:
		answer(w, http.StatusBadGateway, verifyAnswer{Error: "jwks_unavailable"})
	default:
		shown := &claimsShown{Sub: c.Subject, Room: c.Room, Role: c.Role, Seat: *c.Seat, Aud: c.Audience}
		if c.Round != nil {
			shown.Round = *c.Round
		}
		if c.ExpiresAt != nil {
			shown.Exp = c.ExpiresAt.Unix()
		}
		answer(w, http.StatusOK, verifyAnswer{Valid: true, Claims: shown})
	}
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
