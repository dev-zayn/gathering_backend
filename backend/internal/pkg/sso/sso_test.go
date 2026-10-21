package sso

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/sso/ssotest"
)

const (
	clientID = ssotest.ClientID
	audience = ssotest.Audience
	kid      = ssotest.KeyID
)

// fake wraps ssotest's provider with the config and verifier these tests use.
type fake struct{ *ssotest.Provider }

func newFake(t *testing.T) fake { return fake{ssotest.New(t)} }

func (p fake) config() Config {
	return Config{Issuer: p.URL, ClientID: clientID, Audience: audience, RolesClaim: "roles", Service: "gathering"}
}

func (p fake) verifier(t *testing.T, change ...func(c *Config)) *Verifier {
	t.Helper()
	cfg := p.config()
	for _, f := range change {
		f(&cfg)
	}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The two shapes a roles claim comes in: a list of names, or an object whose
// keys are the names. Both read the same.
func TestRolesComeAsAListOrAsKeys(t *testing.T) {
	p := newFake(t)
	v := p.verifier(t)

	for name, value := range map[string]any{
		"a list": []string{"gathering.operator", "gathering.admin"},
		"keys":   ssotest.Keyed("gathering.operator", "gathering.admin"),
	} {
		t.Run(name, func(t *testing.T) {
			id, err := v.Verify(t.Context(), p.Mint(ssotest.Put(p.Claims(), "roles", value)))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(id.Roles, []string{"admin", "operator"}) {
				t.Fatalf("roles %v, want [admin operator]", id.Roles)
			}
		})
	}
}

// The claim's name is a setting: a word, a dotted path into an object, or a URN.
// A name with dots in it that is a key of its own is read whole.
func TestTheClaimIsWhereverTheProviderPutsIt(t *testing.T) {
	p := newFake(t)

	tests := []struct {
		name  string
		claim string
		token string
	}{
		{name: "a path", claim: "realm_access.roles",
			token: p.Mint(ssotest.Put(p.Claims(), "realm_access.roles", []string{"gathering.admin"}))},
		{name: "a URN with keys", claim: "urn:example:project:123:roles",
			token: p.Mint(ssotest.Put(p.Claims(), "urn:example:project:123:roles", ssotest.Keyed("gathering.admin")))},
		{name: "a URL, dots and all", claim: "https://jamaa.example/roles",
			token: p.Mint(func() map[string]any {
				c := p.Claims()
				c["https://jamaa.example/roles"] = []string{"gathering.admin"}
				return c
			}())},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := p.verifier(t, func(c *Config) { c.RolesClaim = tc.claim })
			id, err := v.Verify(t.Context(), tc.token)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(id.Roles, []string{"admin"}) {
				t.Fatalf("roles %v, want [admin]", id.Roles)
			}
		})
	}

	// The path is not there: a good token with no roles, and the caller decides.
	v := p.verifier(t, func(c *Config) { c.RolesClaim = "realm_access.roles" })
	id, err := v.Verify(t.Context(), p.Token("gathering.admin"))
	if err != nil || len(id.Roles) != 0 {
		t.Fatalf("roles at the wrong path gave %v, %v", id, err)
	}
}

// The rule every service behind one provider keeps: only "<service>.<role>"
// counts, prefix stripped; a genuine token holding only another service's role
// opens nothing here, and says so with no roles rather than an error.
func TestOnlyThisServicesRolesSurvive(t *testing.T) {
	p := newFake(t)
	v := p.verifier(t)

	tests := []struct {
		name  string
		roles []string
		want  []string
	}{
		{name: "ours", roles: []string{"gathering.admin"}, want: []string{"admin"}},
		{name: "ours among others", roles: []string{"other.admin", "gathering.operator", "pulse.viewer"}, want: []string{"operator"}},
		{name: "sorted and once", roles: []string{"gathering.operator", "gathering.admin", "gathering.admin"}, want: []string{"admin", "operator"}},
		{name: "another service's only", roles: []string{"other.admin", "pulse.operator"}},
		{name: "lookalike prefixes", roles: []string{"gatheringx.admin", "gathering_admin", "gathering", "gathering."}},
		{name: "no roles", roles: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := v.Verify(t.Context(), p.Token(tc.roles...))
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !slices.Equal(id.Roles, tc.want) {
				t.Fatalf("roles %v, want %v", id.Roles, tc.want)
			}
		})
	}

	// No claim at all is the same answer, with a line in the log.
	id, err := v.Verify(t.Context(), p.Mint(p.Claims()))
	if err != nil || len(id.Roles) != 0 {
		t.Fatalf("no roles claim gave %v, %v", id, err)
	}
}

func TestVerify(t *testing.T) {
	p := newFake(t)
	v := p.verifier(t)

	with := func(change func(c map[string]any)) string {
		c := ssotest.Put(p.Claims(), "roles", []string{"gathering.admin"})
		if change != nil {
			change(c)
		}
		return p.Mint(c)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)

	tests := []struct {
		name  string
		token string
		want  error // nil means accepted
	}{
		{name: "good", token: with(nil)},

		// The times.
		{name: "expired beyond the skew", token: with(func(c map[string]any) { c["exp"] = time.Now().Add(-61 * time.Second).Unix() }), want: ErrInvalid},
		{name: "expired within the skew", token: with(func(c map[string]any) { c["exp"] = time.Now().Add(-30 * time.Second).Unix() })},
		{name: "not valid yet", token: with(func(c map[string]any) { c["nbf"] = time.Now().Add(2 * time.Minute).Unix() }), want: ErrInvalid},
		{name: "issued in the future", token: with(func(c map[string]any) { c["iat"] = time.Now().Add(2 * time.Minute).Unix() }), want: ErrInvalid},
		{name: "no exp", token: with(func(c map[string]any) { delete(c, "exp") }), want: ErrInvalid},
		{name: "no sub", token: with(func(c map[string]any) { delete(c, "sub") }), want: ErrInvalid},

		// Whose it is and who it is for.
		{name: "wrong audience", token: with(func(c map[string]any) { c["aud"] = []string{"console-other", clientID} }), want: ErrInvalid},
		{name: "one audience, ours", token: with(func(c map[string]any) { c["aud"] = audience })},
		{name: "wrong issuer", token: with(func(c map[string]any) { c["iss"] = "https://evil.example" }), want: ErrInvalid},
		{name: "issuer with a trailing slash", token: with(func(c map[string]any) { c["iss"] = p.URL + "/" }), want: ErrInvalid},

		// The signature.
		{name: "signed by another key", token: ssotest.Sign(t, other, map[string]string{"alg": "RS256", "kid": kid}, p.Claims()), want: ErrInvalid},
		{name: "unknown key id", token: ssotest.Sign(t, p.Key, map[string]string{"alg": "RS256", "kid": "key-9"}, p.Claims()), want: ErrInvalid},
		{name: "HS256 with the public key", token: hs256(p.Claims(), p.Key.N.Bytes()), want: ErrInvalid},
		{name: "alg none", token: unsigned(p.Claims()), want: ErrInvalid},
		{name: "tampered roles", token: tamper(t, p.Token("gathering.operator"), "gathering.admin"), want: ErrInvalid},
		{name: "not a JWT", token: "not-a-token", want: ErrInvalid},
		{name: "an opaque token", token: "ory_at_7f3k9d2m8q1x4b6n", want: ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := v.Verify(t.Context(), tc.token)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !slices.Equal(id.Roles, []string{"admin"}) {
				t.Fatalf("roles %v, want [admin]", id.Roles)
			}
		})
	}
}

// With no audience of its own, the API is what the client id names: the
// default a provider writes into aud.
func TestTheClientIDIsTheAudienceByDefault(t *testing.T) {
	p := newFake(t)
	v := p.verifier(t, func(c *Config) { c.Audience = "" })

	c := ssotest.Put(p.Claims(), "roles", []string{"gathering.admin"})
	c["aud"] = []string{clientID}
	if _, err := v.Verify(t.Context(), p.Mint(c)); err != nil {
		t.Fatalf("the client id was refused as audience: %v", err)
	}

	// And then our own audience, which the client id has replaced, is not it.
	c["aud"] = []string{audience}
	if _, err := v.Verify(t.Context(), p.Mint(c)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

func TestIdentity(t *testing.T) {
	p := newFake(t)
	v := p.verifier(t)

	id, err := v.Verify(t.Context(), p.Token("gathering.admin"))
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "user-1" || id.Name != "Sara Ali" || id.Email != "sara@jamaa.example" {
		t.Fatalf("identity is %+v", id)
	}

	// A provider that sends no name or address sends preferred_username.
	c := ssotest.Put(p.Claims(), "roles", []string{"gathering.admin"})
	delete(c, "name")
	delete(c, "email")
	c["preferred_username"] = "sara@jamaa.example"
	id, err = v.Verify(t.Context(), p.Mint(c))
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "sara@jamaa.example" || id.Email != "sara@jamaa.example" {
		t.Fatalf("identity without name and email is %+v", id)
	}

	// A bare username is a name, not an address.
	c["preferred_username"] = "sara"
	id, err = v.Verify(t.Context(), p.Mint(c))
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "sara" || id.Email != "" {
		t.Fatalf("identity with a bare username is %+v", id)
	}
}

// The provider down is not the api down: New never calls out, and a token asked
// about while it is down is "unavailable", then fine once it is back.
func TestTheProviderDownIsAskedAgainLater(t *testing.T) {
	p := newFake(t)
	p.Down(true)
	v := p.verifier(t)
	clock := time.Now()
	v.now = func() time.Time { return clock }

	token := p.Token("gathering.admin")
	if _, err := v.Verify(t.Context(), token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}

	p.Down(false)
	if _, err := v.Verify(t.Context(), token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("asked again at once: %v", err)
	}

	clock = clock.Add(retryAfter + time.Second)
	if _, err := v.Verify(t.Context(), token); err != nil {
		t.Fatalf("still refused once the provider was back: %v", err)
	}
}

// Discovery done and the keys out of reach is the provider unavailable too,
// not a bad token.
func TestKeysOutOfReachAreNotABadSignature(t *testing.T) {
	p := newFake(t)
	v := p.verifier(t)
	if _, err := v.discover(); err != nil {
		t.Fatal(err)
	}

	p.Down(true)
	token := p.Token("gathering.admin")
	if _, err := v.Verify(t.Context(), token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}

	p.Down(false)
	if _, err := v.Verify(t.Context(), token); err != nil {
		t.Fatalf("still refused once the keys were back: %v", err)
	}
}

// Nothing listens on port 1: an issuer that cannot be reached at all.
func TestAnUnreachableIssuerIsUnavailable(t *testing.T) {
	v, err := New(Config{Issuer: "http://127.0.0.1:1", ClientID: clientID, RolesClaim: "roles", Service: "gathering"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(t.Context(), newFake(t).Token("gathering.admin")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}

// On a laptop the api is in Docker and the issuer says localhost. Dial sends
// the connection elsewhere; the issuer, and so the iss check, stay the same.
func TestDialReachesTheIssuerSomewhereElse(t *testing.T) {
	p := newFake(t)
	u, _ := url.Parse(p.URL)
	_, port, _ := net.SplitHostPort(u.Host)

	// Nothing listens on port 1, so the discovery document can only come back
	// if the connection went to Dial instead.
	cfg := p.config()
	cfg.Issuer = "http://localhost:1"
	cfg.Dial = "127.0.0.1:" + port
	res, err := client(cfg).Get("http://localhost:1/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("Dial did not redirect the connection: %v", err)
	}
	_ = res.Body.Close()

	// Only the issuer's address is redirected, never anybody else's.
	cfg.Dial = "127.0.0.1:1"
	if res, err := client(cfg).Get(p.URL + "/.well-known/openid-configuration"); err != nil {
		t.Fatalf("a different host was redirected too: %v", err)
	} else {
		_ = res.Body.Close()
	}
}

// Classify is what main's adapter asks, so biz can name the two cases in
// sentinels of its own.
func TestClassify(t *testing.T) {
	p := newFake(t)
	v := p.verifier(t)

	for name, tc := range map[string]struct {
		err                  error
		invalid, unavailable bool
	}{
		"invalid":     {err: errors.Join(ErrInvalid, errors.New("expired")), invalid: true},
		"unavailable": {err: errors.Join(ErrUnavailable, errors.New("503")), unavailable: true},
		"no role":     {err: ErrNoRole},
		"nothing":     {},
	} {
		invalid, unavailable := v.Classify(tc.err)
		if invalid != tc.invalid || unavailable != tc.unavailable {
			t.Errorf("%s: classified as invalid=%v unavailable=%v", name, invalid, unavailable)
		}
	}
}

func TestConfigListsEveryProblem(t *testing.T) {
	err := Config{Issuer: "http://sso.jamaa.example", Service: "Super.Service", Dial: "nowhere"}.Validate()
	if err == nil {
		t.Fatal("a bad config passed")
	}
	for _, want := range []string{
		"SSO_CLIENT_ID", "SSO_ROLES_CLAIM", "must use https", "SSO_SERVICE", "SSO_DIAL",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("no mention of %q in: %v", want, err)
		}
	}

	// localhost over http, no audience of its own: a laptop against any provider.
	good := Config{Issuer: "http://localhost:8081", ClientID: "c", RolesClaim: "roles", Service: "gathering"}
	if err := good.Validate(); err != nil {
		t.Errorf("a good config was refused: %v", err)
	}
}

func hs256(claims map[string]any, secret []byte) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "kid": kid})
	b, _ := json.Marshal(claims)
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(b)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(signed))
	return signed + "." + enc.EncodeToString(m.Sum(nil))
}

func unsigned(claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "none"})
	b, _ := json.Marshal(claims)
	return enc.EncodeToString(h) + "." + enc.EncodeToString(b) + "."
}

// tamper swaps the payload for one granting role, keeping the old signature.
func tamper(t *testing.T, token, role string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	c["roles"] = []string{role}
	b, _ := json.Marshal(c)
	parts[1] = base64.RawURLEncoding.EncodeToString(b)
	return strings.Join(parts, ".")
}
