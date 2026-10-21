package authn_test

import (
	"net/http"
	"testing"

	"github.com/jamaa/gathering/backend/internal/http/authn"
)

func TestTheCookieIsNamedForWhereItLives(t *testing.T) {
	if c := authn.NewCookie(true, nil); c.Name != "__Host-gathering_admin" || !c.Issue("v").Secure {
		t.Errorf("secure: %+v", c)
	}
	// __Host- is only accepted on a Secure cookie, so a laptop without one
	// gets a plain name rather than a cookie the browser throws away.
	if c := authn.NewCookie(false, nil); c.Name != "gathering_admin" || c.Issue("v").Secure {
		t.Errorf("insecure: %+v", c)
	}
}

func TestTheCookieIsReadOutOfTheHeader(t *testing.T) {
	c := authn.NewCookie(true, nil)
	for header, want := range map[string]string{
		"__Host-gathering_admin=abc":                      "abc",
		"theme=dark; __Host-gathering_admin=abc; lang=ar": "abc",
		"gathering_admin=abc":                             "", // the unprefixed one is not ours here
		"":                                                "",
		"garbage;;;=":                                     "",
	} {
		if got := c.Read(header); got != want {
			t.Errorf("%q: got %q, want %q", header, got, want)
		}
	}
}

func TestOnlyTheConsolesOriginMaySendIt(t *testing.T) {
	c := authn.NewCookie(true, []string{"https://console.gathering.example"})
	for _, tc := range []struct {
		origin, method string
		want           bool
	}{
		{"https://console.gathering.example", http.MethodPost, true},
		{"https://console.gathering.example", http.MethodGet, true},
		{"https://docs.gathering.example", http.MethodGet, false},
		{"https://console.gathering.example.evil.test", http.MethodPost, false},
		{"null", http.MethodPost, false},
		{"", http.MethodGet, true},
		{"", http.MethodDelete, false},
		{"", http.MethodPost, false},
	} {
		if got := c.From(tc.origin, tc.method); got != tc.want {
			t.Errorf("%s from %q: got %v, want %v", tc.method, tc.origin, got, tc.want)
		}
	}
}
