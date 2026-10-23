// Package token mints and reads every credential Gathering issues — one
// package, so signing and verifying never drift. Nothing here reaches Postgres.
package token

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

type Claims struct {
	jwt.RegisteredClaims

	// Scope is the kind for our own tokens, and the space-separated permission
	// list for one handed to a game: "identity:read friends:read".
	Scope string `json:"scope"`

	// Kind names the sort of token when Scope is a permission list.
	Kind string `json:"kind,omitempty"`

	// Phone is set only while signing in, before there is anyone to point at.
	Phone string `json:"phone,omitempty"`

	// Room, Role, Seat and Round are a room ticket's: which room, what the
	// player is there (host, player, spectator), where they sit, and the
	// room's round when it was minted. Seat and Round are pointers so a
	// ticket always carries them — the host sits in seat 0, a lobby that never
	// started is round 0 — and no other token carries them at all.
	Room  string `json:"room,omitempty"`
	Role  string `json:"role,omitempty"`
	Seat  *int   `json:"seat,omitempty"`
	Round *int   `json:"round,omitempty"`

	// Roles are an admin's, as the identity provider gave them at sign-in,
	// prefix stripped.
	Roles []string `json:"roles,omitempty"`

	// Session is the device. Signing out revokes the session, not the token.
	Session string `json:"sid,omitempty"`

	// Live says a person was present. Elevated permissions need it, and a refresh
	// cannot invent one.
	Live bool `json:"live,omitempty"`
}

// Is reports whether the token is of the given kind.
func (c *Claims) Is(scope string) bool {
	if c.Kind != "" {
		return c.Kind == scope
	}
	return c.Scope == scope
}

// Game is the one game a token was minted for, or empty.
func (c *Claims) Game() string {
	if len(c.Audience) == 1 {
		return c.Audience[0]
	}
	return ""
}

// Has reports whether a space-separated permission list contains one permission.
func (c *Claims) Has(permission string) bool {
	for _, s := range strings.Fields(c.Scope) {
		if s == permission {
			return true
		}
	}
	return false
}

var (
	ErrInvalidToken = errors.Unauthorized("invalid_token")
	ErrExpiredToken = errors.Unauthorized("expired_token")
)

// Signer holds the keys. ES256 rather than a shared secret so a partner can
// verify without calling us. No previous-key slot: rotating signs everyone out.
type Signer struct {
	priv   *ecdsa.PrivateKey
	pub    *ecdsa.PublicKey
	kid    string
	issuer string

	// Built at construction, so a key we cannot publish stops the api starting
	// rather than emptying the JWKS.
	jwk JWK
}

// NewSigner takes the private key as PEM, or the same DER base64-encoded onto
// one line. The public half, which verifies and is published, comes from it,
// so the two can never disagree.
func NewSigner(privateKey, issuer string) (*Signer, error) {
	s := &Signer{issuer: issuer}

	if privateKey == "" {
		return nil, fmt.Errorf("token: no key given")
	}
	der, err := decode(privateKey)
	if err != nil {
		return nil, fmt.Errorf("token: private key: %w", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("token: private key: %w", err)
	}
	priv, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("token: private key is %T, want an ECDSA P-256 key", key)
	}
	s.priv, s.pub = priv, &priv.PublicKey

	if s.pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("token: key is not on P-256")
	}

	s.kid = thumbprint(s.pub)

	jwk, err := publish(s.pub, s.kid)
	if err != nil {
		return nil, fmt.Errorf("token: public key: %w", err)
	}
	s.jwk = jwk

	return s, nil
}

// publish turns the key into the form a standard client library reads. The
// coordinates come from the encoded point, not the discouraged big.Int fields.
func publish(pub *ecdsa.PublicKey, kid string) (JWK, error) {
	point, err := pub.Bytes()
	if err != nil {
		return JWK{}, err
	}
	// Uncompressed: one 0x04 byte, then X and Y, each the curve's byte length.
	if len(point) < 3 || point[0] != 4 || len(point)%2 == 0 {
		return JWK{}, fmt.Errorf("public key is not an uncompressed point")
	}
	size := (len(point) - 1) / 2

	return JWK{
		Kty: "EC", Crv: "P-256", Use: "sig", Alg: "ES256", Kid: kid,
		X: base64.RawURLEncoding.EncodeToString(point[1 : 1+size]),
		Y: base64.RawURLEncoding.EncodeToString(point[1+size:]),
	}, nil
}

// decode accepts a PEM block or bare base64 DER: same key either way.
func decode(in string) ([]byte, error) {
	in = strings.TrimSpace(in)
	if strings.Contains(in, "-----BEGIN") {
		block, _ := pem.Decode([]byte(in))
		if block == nil {
			return nil, fmt.Errorf("not a PEM block")
		}
		return block.Bytes, nil
	}
	return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(in), ""))
}

// thumbprint changes when the key does, so a cached JWKS with the old kid misses
// rather than verifying against the wrong thing.
func thumbprint(pub *ecdsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "key"
	}
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])[:16]
}

// Mint fills in issuer, id and timestamps, then signs. The caller sets the scope.
func (s *Signer) Mint(c Claims, ttl time.Duration) (string, string, error) {
	now := time.Now()
	jti := ids.New("tok")

	c.Issuer = s.issuer
	c.ID = jti
	c.IssuedAt = jwt.NewNumericDate(now)
	c.NotBefore = jwt.NewNumericDate(now)
	c.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))

	t := jwt.NewWithClaims(jwt.SigningMethodES256, c)
	t.Header["kid"] = s.kid

	signed, err := t.SignedString(s.priv)
	if err != nil {
		return "", "", errors.Internal(err)
	}
	return signed, jti, nil
}

// Parse verifies the signature and the times. Revocation is the whitelist's
// answer, not ours.
func (s *Signer) Parse(raw string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return s.pub, nil
	}, jwt.WithIssuer(s.issuer), jwt.WithValidMethods([]string{"ES256"}))

	switch {
	case err == nil:
		return &c, nil
	case strings.Contains(err.Error(), jwt.ErrTokenExpired.Error()):
		return nil, ErrExpiredToken
	default:
		return nil, ErrInvalidToken.Wrap(err)
	}
}

// JWK is one public key in the form a standard client library reads.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// JWKS is the public half, so nothing has to ask us whether a token is real.
func (s *Signer) JWKS() []JWK { return []JWK{s.jwk} }

func (s *Signer) Issuer() string { return s.issuer }

// Bearer pulls the token out of an Authorization header.
func Bearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(header[len(prefix):]), true
}

// For reports whether this token was minted to be spent at the given address
// (RFC 8707). No audience is not for anywhere in particular, and fails.
func (c *Claims) For(resource string) bool {
	for _, a := range c.Audience {
		if a == resource {
			return true
		}
	}
	return false
}
