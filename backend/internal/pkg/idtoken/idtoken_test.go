package idtoken

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/sso/ssotest"
)

// The fake provider names two audiences in its tokens, ours among them; a
// phone's token names one client, which is the same check.
const ours = ssotest.Audience

func verifier(t *testing.T, p *ssotest.Provider, audiences ...string) *Verifier {
	t.Helper()
	if len(audiences) == 0 {
		audiences = []string{"web-client", ours}
	}
	v, err := New(p.URL, audiences)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAGoodTokenAnswersItsSubject(t *testing.T) {
	p := ssotest.New(t)
	sub, err := verifier(t, p).Subject(t.Context(), p.Mint(p.Claims()))
	if err != nil || sub != "user-1" {
		t.Fatalf("got %q, %v; want user-1", sub, err)
	}
}

// Every way a token is not ours is the same refusal: invalid, never
// unavailable, so the app says "try another way" and not "try again later".
func TestEveryBadTokenIsInvalid(t *testing.T) {
	p := ssotest.New(t)
	stranger, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	with := func(key string, value any) string {
		c := p.Claims()
		c[key] = value
		return p.Mint(c)
	}

	tests := map[string]string{
		"not a token":          "x",
		"another app's":        with("aud", []string{"someone-else"}),
		"another issuer":       with("iss", "https://issuer.example"),
		"expired":              with("exp", time.Now().Add(-time.Minute).Unix()),
		"no subject":           with("sub", ""),
		"signed by a stranger": ssotest.Sign(t, stranger, map[string]string{"alg": "RS256", "kid": ssotest.KeyID}, p.Claims()),
	}
	v := verifier(t, p)
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := v.Subject(t.Context(), raw)
			if invalid, unavailable := Classify(err); !invalid || unavailable {
				t.Fatalf("got %v, want invalid", err)
			}
		})
	}
}

// Only the client ids this deployment names: a token for the fake's other
// audience alone is refused.
func TestTheAudienceIsOneOfOurs(t *testing.T) {
	p := ssotest.New(t)
	_, err := verifier(t, p, "web-client").Subject(t.Context(), p.Mint(p.Claims()))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want invalid", err)
	}
}

// A provider that is down is unavailable, remembered for a few seconds, and
// asked again after them.
func TestAProviderDownIsUnavailableThenAskedAgain(t *testing.T) {
	p := ssotest.New(t)
	v := verifier(t, p)
	now := time.Now()
	v.now = func() time.Time { return now }

	p.Down(true)
	if _, err := v.Subject(t.Context(), p.Mint(p.Claims())); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("down: got %v, want unavailable", err)
	}

	p.Down(false)
	if _, err := v.Subject(t.Context(), p.Mint(p.Claims())); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("within retryAfter: got %v, want unavailable", err)
	}

	now = now.Add(retryAfter)
	if sub, err := v.Subject(t.Context(), p.Mint(p.Claims())); err != nil || sub != "user-1" {
		t.Fatalf("back up: got %q, %v", sub, err)
	}
}

func TestNewRefusesBadSettings(t *testing.T) {
	if _, err := New("http://accounts.example", []string{"a"}); err == nil {
		t.Fatal("http issuer accepted")
	}
	if _, err := New(GoogleIssuer, nil); err == nil {
		t.Fatal("no client ids accepted")
	}
	if _, err := New(AppleIssuer, []string{"app.gathering.jamaa"}); err != nil {
		t.Fatal(err)
	}
}
