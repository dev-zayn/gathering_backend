// Package sso checks the access tokens an OIDC provider signs for the console.
// Any provider that publishes a discovery document and puts a person's roles in
// a claim will do: which claim, and what prefix our roles carry in it, are
// settings. Nothing here knows one provider from another.
//
// One provider can serve several services, so a token may carry roles for all
// of them. The role prefix is the boundary: only "<service>.<role>" roles are
// kept, and everything else is dropped before an Identity exists.
//
// Bridging to biz: biz/identity classifies what Verify returns through two
// sentinels of its own, identity.ErrSSOInvalid and identity.ErrSSOUnavailable,
// so that package never names this one's errors. main wires the two together
// with a small adapter: call Verify, ask Classify about the error, and wrap it
// in identity's sentinel. Nothing here imports biz, and nothing in biz has to
// import this.
package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

const (
	// clockSkew is the only leeway, on exp, nbf and iat alike.
	clockSkew = 60 * time.Second

	// retryAfter is how long a failed discovery is remembered, so a provider
	// that is down is asked again soon but not on every request.
	retryAfter = 10 * time.Second
)

// signingAlgs are the asymmetric ones go-oidc knows. A shared secret (HS*) is
// not a provider's signature, so none of those.
var signingAlgs = []string{
	oidc.RS256, oidc.RS384, oidc.RS512,
	oidc.ES256, oidc.ES384, oidc.ES512,
	oidc.PS256, oidc.PS384, oidc.PS512,
	oidc.EdDSA,
}

// Config is read from the environment by conf, which copies its fields here:
// pkg takes what it needs as arguments and never reads the environment itself.
type Config struct {
	Issuer   string // SSO_ISSUER, exactly as the provider writes iss
	ClientID string // SSO_CLIENT_ID, the console's app at the provider

	// Audience is what a token must name in aud to be meant for this API.
	// Empty means the client id, which is what a provider puts there unless
	// told otherwise.
	Audience string // SSO_AUDIENCE

	// RolesClaim is where the roles are: "roles", a dotted path into an object
	// ("realm_access.roles"), or a URN. The whole name is tried before the path.
	RolesClaim string // SSO_ROLES_CLAIM

	Service string // SSO_SERVICE, the role prefix: "gathering" keeps "gathering.*"

	// Dial is where connections to the issuer go instead, for a laptop only: in
	// Docker, "localhost:8081" is the container itself. The URL and the Host
	// header stay the issuer's, which is what the provider answers to.
	Dial string // SSO_DIAL
}

var serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Validate lists every problem at once, so a bad deploy dies at boot saying all
// of what is wrong.
func (c Config) Validate() error {
	var errs []error
	for _, kv := range [][2]string{
		{"SSO_ISSUER", c.Issuer}, {"SSO_CLIENT_ID", c.ClientID},
		{"SSO_ROLES_CLAIM", c.RolesClaim}, {"SSO_SERVICE", c.Service},
	} {
		if strings.TrimSpace(kv[1]) == "" {
			errs = append(errs, fmt.Errorf("%s is required", kv[0]))
		}
	}
	if c.Issuer != "" && !httpsOrLocal(c.Issuer) {
		errs = append(errs, errors.New("SSO_ISSUER must use https (http only for localhost)"))
	}
	if c.Service != "" && !serviceName.MatchString(c.Service) {
		errs = append(errs, fmt.Errorf("SSO_SERVICE %q must be lowercase letters, digits, - or _ (no dots)", c.Service))
	}
	if c.Dial != "" {
		if _, _, err := net.SplitHostPort(c.Dial); err != nil {
			errs = append(errs, fmt.Errorf("SSO_DIAL %q must be host:port", c.Dial))
		}
	}
	return errors.Join(errs...)
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

// Identity is who the token names. Roles holds this service's only, prefix
// stripped and sorted. None is a good token from somebody who is not ours, and
// the caller says what that means.
type Identity struct {
	Subject string
	Name    string
	Email   string
	Roles   []string
}

var (
	// ErrInvalid is a token that is not a good one from this provider: not a
	// JWT, a bad signature, another issuer or audience, expired.
	ErrInvalid = errors.New("sso: invalid token")

	// ErrUnavailable is the provider out of reach, its discovery document or
	// its keys, so nothing can be checked yet.
	ErrUnavailable = errors.New("sso: identity provider unavailable")

	// ErrNoRole is a good token that holds no role here. Verify never returns
	// it: an Identity with no Roles is the caller's to refuse, and this is the
	// error for it, so every caller says it the same way.
	ErrNoRole = errors.New("sso: no role for this service")
)

// Verifier checks tokens. The provider is looked up on the first token rather
// than at boot: SSO being down locks the console out, not every player.
type Verifier struct {
	cfg    Config
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	checker *oidc.IDTokenVerifier
	failed  time.Time
}

// New checks the settings and nothing else: no network until a token arrives.
func New(cfg Config) (*Verifier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Verifier{cfg: cfg, client: client(cfg), now: time.Now}, nil
}

// Issuer is the one iss this verifier accepts. The resolver reads it to send a
// token here rather than to our own signer.
func (v *Verifier) Issuer() string { return v.cfg.Issuer }

// Classify says which of the two sentinels err carries, for the adapter in
// main that turns them into biz/identity's own. A good token with no role is
// neither: that is an Identity with no Roles, not an error.
func (v *Verifier) Classify(err error) (invalid, unavailable bool) {
	return errors.Is(err, ErrInvalid), errors.Is(err, ErrUnavailable)
}

// client bounds every call to the provider, and on a laptop sends it to Dial
// while the URL and Host header stay the issuer's.
func client(cfg Config) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if cfg.Dial != "" {
		u, _ := url.Parse(cfg.Issuer)
		issuerAddr := u.Host
		if u.Port() == "" {
			issuerAddr = net.JoinHostPort(u.Hostname(), map[string]string{"https": "443", "http": "80"}[u.Scheme])
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr == issuerAddr {
				addr = cfg.Dial
			}
			return dialer.DialContext(ctx, network, addr)
		}
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport}
}

// discover reads the provider's discovery document once. go-oidc then fetches,
// caches and rotates the signing keys itself. A failure is kept for a few
// seconds so a provider that is down is not asked on every request.
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
	// its deadline. go-oidc also refuses a document whose issuer is not exactly
	// this string.
	ctx := oidc.ClientContext(context.Background(), v.client)
	provider, err := oidc.NewProvider(ctx, v.cfg.Issuer)
	if err != nil {
		v.failed = v.now()
		return nil, fmt.Errorf("%w: discovery at %s: %v", ErrUnavailable, v.cfg.Issuer, err)
	}

	v.checker = provider.Verifier(&oidc.Config{
		SkipClientIDCheck:    true, // below: the audience is a setting of its own
		SkipExpiryCheck:      true, // below, with the flat 60 seconds of skew
		SupportedSigningAlgs: signingAlgs,
	})
	return v.checker, nil
}

// Verify checks the signature, the issuer, the audience and the times, then
// keeps this service's roles.
func (v *Verifier) Verify(ctx context.Context, raw string) (*Identity, error) {
	checker, err := v.discover()
	if err != nil {
		return nil, err
	}

	tok, err := checker.Verify(ctx, raw)
	if err != nil {
		// go-oidc flattens the chain under its signature check, so keys out of
		// reach can be told from a bad signature only by its words.
		if strings.Contains(err.Error(), "fetching keys") {
			return nil, fmt.Errorf("%w: keys of %s: %v", ErrUnavailable, v.cfg.Issuer, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	var claims map[string]json.RawMessage
	var std struct {
		Sub      string  `json:"sub"`
		Name     string  `json:"name"`
		Email    string  `json:"email"`
		Username string  `json:"preferred_username"`
		Exp      float64 `json:"exp"`
		Nbf      float64 `json:"nbf"`
		Iat      float64 `json:"iat"`
	}
	if err := errors.Join(tok.Claims(&claims), tok.Claims(&std)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	// The line between tokens: one meant for this API names it, a sibling
	// app's names that app. The client id is what a provider writes there
	// unless it is told an audience of its own.
	want := v.cfg.Audience
	if want == "" {
		want = v.cfg.ClientID
	}
	if !slices.Contains(tok.Audience, want) {
		return nil, fmt.Errorf("%w: audience %v, want %q", ErrInvalid, tok.Audience, want)
	}

	now := v.now()
	switch {
	case std.Exp == 0:
		return nil, fmt.Errorf("%w: no exp", ErrInvalid)
	case now.After(unix(std.Exp).Add(clockSkew)):
		return nil, fmt.Errorf("%w: expired", ErrInvalid)
	case std.Nbf != 0 && now.Add(clockSkew).Before(unix(std.Nbf)):
		return nil, fmt.Errorf("%w: not valid yet", ErrInvalid)
	case std.Iat != 0 && now.Add(clockSkew).Before(unix(std.Iat)):
		return nil, fmt.Errorf("%w: issued in the future", ErrInvalid)
	case std.Sub == "":
		return nil, fmt.Errorf("%w: no sub", ErrInvalid)
	}

	names, found := rolesOf(claims, v.cfg.RolesClaim)
	if !found {
		// The one misconfiguration every deployment meets once: the provider
		// is not putting roles in the access token, or the claim is named
		// wrong. A line here says so, where the refusal the caller sends cannot.
		applog.From(ctx).Warn("sso roles claim missing", "claim", v.cfg.RolesClaim, "sub", std.Sub)
	}

	// The boundary: "<service>.<role>", prefix stripped. Every other service's
	// roles are dropped.
	var mine []string
	for _, name := range names {
		if r, ok := strings.CutPrefix(name, v.cfg.Service+"."); ok && r != "" && !slices.Contains(mine, r) {
			mine = append(mine, r)
		}
	}
	slices.Sort(mine)

	// A provider that sends no name sends preferred_username, which is often
	// the address too.
	name := strings.TrimSpace(std.Name)
	if name == "" {
		name = strings.TrimSpace(std.Username)
	}
	email := strings.TrimSpace(std.Email)
	if email == "" && strings.Contains(std.Username, "@") {
		email = strings.TrimSpace(std.Username)
	}

	return &Identity{Subject: std.Sub, Name: name, Email: email, Roles: mine}, nil
}

// rolesOf reads the roles claim: a list of names, or an object whose keys are
// the names (the Zitadel shape). The claim's name is tried whole first, so a
// URL-shaped name keeps its dots; then as a path into nested objects. Not found
// is not an error here: the caller treats it as no roles.
func rolesOf(claims map[string]json.RawMessage, path string) ([]string, bool) {
	raw, ok := claims[path]
	if !ok {
		raw, ok = descend(claims, strings.Split(path, "."))
	}
	if !ok {
		return nil, false
	}

	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list, true
	}
	var keyed map[string]json.RawMessage
	if json.Unmarshal(raw, &keyed) == nil && keyed != nil {
		names := make([]string, 0, len(keyed))
		for name := range keyed {
			names = append(names, name)
		}
		return names, true
	}
	return nil, false
}

// descend follows a dotted path through nested objects to the value at its end.
func descend(obj map[string]json.RawMessage, path []string) (json.RawMessage, bool) {
	for i, key := range path {
		raw, ok := obj[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return raw, true
		}
		obj = nil
		if json.Unmarshal(raw, &obj) != nil || obj == nil {
			return nil, false
		}
	}
	return nil, false
}

func unix(sec float64) time.Time {
	s, frac := math.Modf(sec)
	return time.Unix(int64(s), int64(frac*1e9))
}
