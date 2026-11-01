// Package idtoken checks the id tokens Google and Apple sign for a player on a
// phone (docs/SSO.md): the signature against the provider's published keys,
// the issuer, the audience and the times, then the person's id there. Nothing
// else in the token is read. pkg/sso is the console's, which reads roles;
// this one reads only who.
//
// Bridging to biz works as pkg/sso's does: biz/identity has two sentinels of
// its own, and main wraps this package's errors in them.
package idtoken

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// The issuers, exactly as each provider's discovery document writes them.
// go-oidc also takes Google's scheme-less "accounts.google.com" in a token.
const (
	GoogleIssuer = "https://accounts.google.com"
	AppleIssuer  = "https://appleid.apple.com"
)

// retryAfter is how long a failed discovery is remembered, so a provider that
// is down is asked again soon but not on every request.
const retryAfter = 10 * time.Second

var (
	// ErrInvalid is a token that is not a good one from this provider for this
	// app: not a JWT, a bad signature, another issuer or audience, expired.
	ErrInvalid = errors.New("idtoken: invalid token")

	// ErrUnavailable is the provider's discovery document or keys out of
	// reach, so nothing can be checked yet.
	ErrUnavailable = errors.New("idtoken: provider unavailable")
)

// Verifier checks one provider's tokens. The provider is looked up on the
// first token rather than at boot: Google being down locks out Google's
// players, not the api.
type Verifier struct {
	issuer    string
	audiences []string
	client    *http.Client

	mu      sync.Mutex
	checker *oidc.IDTokenVerifier
	failed  time.Time
	now     func() time.Time
}

// New checks the settings and nothing else: no network until a token arrives.
// audiences are the client ids a token may name in aud; at least one.
func New(issuer string, audiences []string) (*Verifier, error) {
	if !httpsOrLocal(issuer) {
		return nil, fmt.Errorf("idtoken: issuer %q must use https (http only for localhost)", issuer)
	}
	if len(audiences) == 0 {
		return nil, fmt.Errorf("idtoken: %s needs at least one client id", issuer)
	}
	return &Verifier{
		issuer:    issuer,
		audiences: slices.Clone(audiences),
		client:    &http.Client{Timeout: 10 * time.Second},
		now:       time.Now,
	}, nil
}

func httpsOrLocal(issuer string) bool {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	return u.Scheme == "https" || (u.Scheme == "http" && local)
}

// discover reads the provider's discovery document once. go-oidc then
// fetches, caches and rotates the signing keys itself.
func (v *Verifier) discover() (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.checker != nil {
		return v.checker, nil
	}
	if !v.failed.IsZero() && v.now().Sub(v.failed) < retryAfter {
		return nil, ErrUnavailable
	}

	// Key refreshes happen long after this request, so they must not inherit
	// its deadline.
	ctx := oidc.ClientContext(context.Background(), v.client)
	provider, err := oidc.NewProvider(ctx, v.issuer)
	if err != nil {
		v.failed = v.now()
		return nil, fmt.Errorf("%w: discovery at %s: %v", ErrUnavailable, v.issuer, err)
	}

	v.checker = provider.Verifier(&oidc.Config{
		SkipClientIDCheck: true, // below: any of several client ids
		Now:               func() time.Time { return v.now() },
	})
	return v.checker, nil
}

// Subject checks the token and answers the person's id at the provider.
func (v *Verifier) Subject(ctx context.Context, raw string) (string, error) {
	checker, err := v.discover()
	if err != nil {
		return "", err
	}

	tok, err := checker.Verify(ctx, raw)
	if err != nil {
		// go-oidc flattens the chain under its signature check, so keys out of
		// reach can be told from a bad signature only by its words.
		if strings.Contains(err.Error(), "fetching keys") {
			return "", fmt.Errorf("%w: keys of %s: %v", ErrUnavailable, v.issuer, err)
		}
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	if !slices.ContainsFunc(tok.Audience, func(aud string) bool { return slices.Contains(v.audiences, aud) }) {
		return "", fmt.Errorf("%w: audience %v is none of ours", ErrInvalid, tok.Audience)
	}
	if tok.Subject == "" {
		return "", fmt.Errorf("%w: no sub", ErrInvalid)
	}
	return tok.Subject, nil
}

// Classify says which of the two sentinels err carries, for the adapter in
// main that turns them into biz/identity's own.
func Classify(err error) (invalid, unavailable bool) {
	return errors.Is(err, ErrInvalid), errors.Is(err, ErrUnavailable)
}
