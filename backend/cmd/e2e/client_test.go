//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
	"time"
)

// Where the stack is, and the one code every sign-in uses. Both have the
// laptop stack's defaults.
var (
	apiURL  = env("E2E_API", "http://localhost:8080")
	code    = env("E2E_CODE", "123456")
	console = env("E2E_CONSOLE_ORIGIN", "http://localhost:8085")
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// client is one caller: a bearer, or the console's cookie jar. A console
// client also sends its Origin on every call, as a browser does, because a
// cookie-bearing request from nowhere is refused.
type client struct {
	t      *testing.T
	bearer string
	origin string
	http   *http.Client
}

func newClient(t *testing.T) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &client{t: t, http: &http.Client{Timeout: 15 * time.Second, Jar: jar}}
}

// call sends one request and decodes the answer; a status other than want
// fails the test with the body.
func (c *client) call(method, path string, body any, want int, out any, headers ...string) {
	c.t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiURL+path, buf)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: got %d, want %d: %s", method, path, res.StatusCode, want, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: bad JSON: %v: %s", method, path, err, raw)
		}
	}
	c.t.Logf("ok   %s %s → %d", method, path, res.StatusCode)
}

// phone is a fresh Iraqi mobile number per run, so a sweep never trips over
// the last one's players.
func phone(n int) string {
	return fmt.Sprintf("+96477%08d", (time.Now().UnixNano()/1000+int64(n))%100000000)
}

// player signs a new number up, or in, and comes back with a session.
func player(t *testing.T, nickname string) (*client, map[string]any) {
	t.Helper()
	c := newClient(t)
	var ticket struct {
		Scope       string `json:"scope"`
		AccessToken string `json:"access_token"`
	}
	c.call("POST", "/v1/auth/signin", map[string]any{"phone": phone(len(nickname))}, 200, &ticket)
	c.bearer = ticket.AccessToken
	var verified struct {
		Scope        string         `json:"scope"`
		AccessToken  string         `json:"access_token"`
		RefreshToken string         `json:"refresh_token"`
		Player       map[string]any `json:"player"`
	}
	c.call("POST", "/v1/auth/otp/verify", map[string]any{"code": code}, 200, &verified)
	c.bearer = verified.AccessToken
	if verified.Scope == "signup" {
		c.call("POST", "/v1/auth/signup", map[string]any{"nickname": nickname, "avatar": "a03", "language": "ar"}, 201, &verified)
		c.bearer = verified.AccessToken
	}
	if verified.Player == nil {
		t.Fatal("no player after sign-in")
	}
	verified.Player["refresh_token"] = verified.RefreshToken
	return c, verified.Player
}
