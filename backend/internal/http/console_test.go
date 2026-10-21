package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
	"github.com/jamaa/gathering/backend/internal/pkg/sso"
	"github.com/jamaa/gathering/backend/internal/pkg/sso/ssotest"
)

// The console's sign-in, through the real router and the real authn, with
// the identity provider answered by a fake. What each answer means is decided
// here, not in the verifier: these are the status codes a browser sees.

const consoleOrigin = "https://console.gathering.test"

// bridge is main's adapter, repeated: the verifier's failures sorted into the
// two identity tells apart.
type bridge struct{ v *sso.Verifier }

func (b bridge) Verify(ctx context.Context, raw string) (*identity.Person, error) {
	id, err := b.v.Verify(ctx, raw)
	if err != nil {
		if _, unavailable := b.v.Classify(err); unavailable {
			return nil, fmt.Errorf("%w: %v", identity.ErrSSOUnavailable, err)
		}
		return nil, fmt.Errorf("%w: %v", identity.ErrSSOInvalid, err)
	}
	return &identity.Person{Subject: id.Subject, Name: id.Name, Email: id.Email, Roles: id.Roles}, nil
}

// whitelist is the real one, in Redis: sign-out is a revocation, and a fake
// that forgets nothing cannot show it. Skips without TEST_REDIS_URL.
func whitelist(t *testing.T) identity.Whitelist {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	rdb, err := data.OpenRedis(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return identity.NewWhitelist(rdb)
}

func consoleRouter(t *testing.T, dev bool) (chi.Router, *ssotest.Provider) {
	t.Helper()
	db := biztest.DB(t)
	signer := biztest.Signer(t)
	p := ssotest.New(t)
	v, err := sso.New(sso.Config{
		Issuer: p.URL, ClientID: ssotest.ClientID, Audience: ssotest.Audience,
		RolesClaim: "roles", Service: "gathering",
	})
	if err != nil {
		t.Fatal(err)
	}

	wl := whitelist(t)
	console := &identity.Console{
		SSO: bridge{v: v}, Admins: identity.NewAdminStore(db), Minter: identity.NewMinter(signer),
		Whitelist: wl, Audit: &biztest.Audit{}, TTL: time.Hour, Idle: 30 * time.Minute, DevAllowed: dev,
	}
	r, api := New("gathering", "test", applog.New("gathering"))
	Mount(api, Deps{
		Resolve:     (&identity.Identities{Signer: signer, Whitelist: wl, AdminIdle: 30 * time.Minute}).Resolve,
		Console:     console,
		Admins:      identity.NewAdminStore(db),
		AdminCookie: authn.NewCookie(true, []string{consoleOrigin}),
		PublicURL:   "https://api.gathering.test",
	})
	return r, p
}

func do(r chi.Router, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// cookieOf turns a Set-Cookie into the Cookie header a browser would send back.
func cookieOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	set := rec.Header().Get("Set-Cookie")
	if set == "" {
		t.Fatalf("no Set-Cookie on %d: %s", rec.Code, rec.Body.String())
	}
	return strings.SplitN(set, ";", 2)[0]
}

func TestConsoleSignsInWithAProviderToken(t *testing.T) {
	r, p := consoleRouter(t, false)

	// A token with our role opens a session, in a cookie, from our origin only.
	rec := do(r, http.MethodPost, "/v1/admin/session", "", map[string]string{
		"Origin": consoleOrigin, "Authorization": "Bearer " + p.Token("gathering.admin", "other.thing"),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	var opened struct {
		Admin struct {
			ID    string   `json:"id"`
			Name  string   `json:"name"`
			Roles []string `json:"roles"`
		} `json:"admin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Admin.Name != "Sara Ali" || len(opened.Admin.Roles) != 1 || opened.Admin.Roles[0] != "admin" {
		t.Errorf("opened: %+v", opened)
	}
	cookie := cookieOf(t, rec)

	// The cookie is the session: me answers with the roles the session was
	// opened with, and the bearer is never sent again.
	rec = do(r, http.MethodGet, "/v1/admin/me", "", map[string]string{"Cookie": cookie, "Origin": consoleOrigin})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"admin"`) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}

	// The list knows them now.
	rec = do(r, http.MethodGet, "/v1/admin/admins", "", map[string]string{"Cookie": cookie, "Origin": consoleOrigin})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), opened.Admin.ID) {
		t.Fatalf("admins: %d %s", rec.Code, rec.Body.String())
	}

	// Sign-out ends it for every tab.
	rec = do(r, http.MethodDelete, "/v1/admin/session", "", map[string]string{"Cookie": cookie, "Origin": consoleOrigin})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("close: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(r, http.MethodGet, "/v1/admin/me", "", map[string]string{"Cookie": cookie, "Origin": consoleOrigin})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after close: %d %s", rec.Code, rec.Body.String())
	}
}

func TestConsoleRefusals(t *testing.T) {
	r, p := consoleRouter(t, false)
	cases := []struct {
		name    string
		origin  string
		token   string
		want    int
		wantErr string
	}{
		{"no role", consoleOrigin, p.Token("other.admin"), http.StatusForbidden, "no_role"},
		{"another site", "https://evil.test", p.Token("gathering.admin"), http.StatusForbidden, "bad_origin"},
		{"no origin", "", p.Token("gathering.admin"), http.StatusForbidden, "bad_origin"},
		{"not a token", consoleOrigin, "nonsense", http.StatusUnauthorized, "invalid_token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := map[string]string{"Authorization": "Bearer " + c.token}
			if c.origin != "" {
				h["Origin"] = c.origin
			}
			rec := do(r, http.MethodPost, "/v1/admin/session", "", h)
			if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.wantErr) {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body.String(), c.want, c.wantErr)
			}
		})
	}

	// The provider down is not the admin's fault, and not a 401. A fresh
	// router, so no key of the provider's is cached from the cases above.
	down, p2 := consoleRouter(t, false)
	p2.Down(true)
	rec := do(down, http.MethodPost, "/v1/admin/session", "", map[string]string{
		"Origin": consoleOrigin, "Authorization": "Bearer " + p2.Token("gathering.admin"),
	})
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "identity_unavailable") {
		t.Fatalf("provider down: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDevSignInOnlyWhereAllowed(t *testing.T) {
	body := `{"name":"zayn"}`
	refused, _ := consoleRouter(t, false)
	rec := do(refused, http.MethodPost, "/v1/admin/session/dev", body, map[string]string{"Origin": consoleOrigin})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dev sign-in where not allowed: %d %s", rec.Code, rec.Body.String())
	}

	allowed, _ := consoleRouter(t, true)
	rec = do(allowed, http.MethodPost, "/v1/admin/session/dev", body, map[string]string{"Origin": consoleOrigin})
	if rec.Code != http.StatusOK {
		t.Fatalf("dev sign-in: %d %s", rec.Code, rec.Body.String())
	}
	cookie := cookieOf(t, rec)
	rec = do(allowed, http.MethodGet, "/v1/admin/me", "", map[string]string{"Cookie": cookie, "Origin": consoleOrigin})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"zayn"`) {
		t.Fatalf("me with a dev session: %d %s", rec.Code, rec.Body.String())
	}
	// Another site cannot use the dev door either.
	rec = do(allowed, http.MethodPost, "/v1/admin/session/dev", body, map[string]string{"Origin": "https://evil.test"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("dev sign-in from elsewhere: %d %s", rec.Code, rec.Body.String())
	}
}
