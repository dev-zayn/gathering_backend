package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// sessions is the whitelist as Redis keeps it: listed until revoked, and how
// long each was allowed to sit idle.
type sessions struct {
	fakeWhitelist
	mu    sync.Mutex
	idle  map[string]time.Duration
	owner map[string]string
}

func (s *sessions) Allow(_ context.Context, jti, owner string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle == nil {
		s.idle, s.owner = map[string]time.Duration{}, map[string]string{}
	}
	s.idle[jti], s.owner[jti] = ttl, owner
	return nil
}

func (s *sessions) RevokeSession(_ context.Context, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for jti, o := range s.owner {
		if o == owner {
			delete(s.idle, jti)
		}
	}
	return nil
}

func (s *sessions) Touch(_ context.Context, jti string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.idle[jti]
	return ok, nil
}

func (s *sessions) Revoke(_ context.Context, jti string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.idle, jti)
	return nil
}

// console is Console as main wires it, with a fake provider and the resolver
// that reads what it mints. The provider knows a few people: sara is an
// operator, ali an admin (said twice), and two more hold nothing here: one a
// role the provider package did not strip, so not ours, one a role we do not
// have.
func console(t *testing.T) (*Console, *Identities, *fakeSSO, *sessions, *fakeAudit) {
	t.Helper()
	uc, signer, _ := testIdentity(t)
	sso := &fakeSSO{
		ids: map[string]*Person{
			"sara":   {Subject: "user-1", Name: " Sara Ali ", Email: " Sara@Example.IQ ", Roles: []string{"operator"}},
			"ali":    {Subject: "user-2", Name: "Ali", Email: "ali@example.iq", Roles: []string{"operator", "admin", "admin"}},
			"nobody": {Subject: "user-3", Name: "N", Roles: []string{"pulse.admin"}},
			"viewer": {Subject: "user-4", Name: "V", Roles: []string{"viewer"}},
		},
		errs: map[string]error{
			"expired": fmt.Errorf("%w: expired", ErrSSOInvalid),
			"machine": apperr.Forbidden("machine_user"),
		},
	}
	wl, log := &sessions{}, &fakeAudit{}
	uc.Whitelist, uc.AdminIdle = wl, 30*time.Minute
	c := &Console{
		SSO: sso, Admins: &fakeAdmins{}, Minter: NewMinter(signer), Whitelist: wl, Audit: log,
		TTL: 12 * time.Hour, Idle: 30 * time.Minute,
	}
	return c, uc, sso, wl, log
}

// Signing in: the provider's token in, a session of ours out, which the
// resolver reads as the admin with only this service's roles. Signing out
// ends it.
func TestASignInIsASessionOfOurs(t *testing.T) {
	c, uc, _, wl, _ := console(t)
	ctx := context.Background()

	opened, err := c.Open(ctx, "sara")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Admin.ID != "adm_user-1" || opened.Admin.Name != "Sara Ali" || opened.Admin.Email != "sara@example.iq" {
		t.Fatalf("admin is %+v", opened.Admin)
	}
	if !slices.Equal(opened.Roles, []string{"operator"}) {
		t.Fatalf("roles are %v", opened.Roles)
	}
	if d := time.Until(opened.ExpiresAt); d < 11*time.Hour || d > 12*time.Hour {
		t.Errorf("expires in %s, want 12h", d)
	}

	answer, err := uc.Of(ctx, "Bearer "+opened.Token)
	s := answer.Session
	if err != nil || s == nil || !s.Is(caller.ScopeAdmin) || s.Subject != "adm_user-1" || !slices.Equal(s.Roles, []string{"operator"}) {
		t.Fatalf("the session reads as %+v, %v", answer, err)
	}
	if wl.idle[s.ID] != 30*time.Minute {
		t.Errorf("listed for %s idle, want 30m", wl.idle[s.ID])
	}

	if err := c.Close(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if answer, _ := uc.Of(ctx, "Bearer "+opened.Token); answer.Session != nil || answer.Reason != "revoked_token" {
		t.Fatalf("after signing out: %+v", answer)
	}
}

// A genuine token holding no role of ours — a spelling the provider package
// did not strip, or a role we do not have — is a 403, not a 401. It is who
// they say; they just have nothing here, and nothing is written down about
// them.
func TestAnotherServicesRoleIsForbidden(t *testing.T) {
	c, _, _, _, log := console(t)

	for _, raw := range []string{"nobody", "viewer"} {
		opened, err := c.Open(context.Background(), raw)
		var e *apperr.Error
		if opened != nil || !errors.As(err, &e) || e.Status != http.StatusForbidden || e.Code != "no_role" {
			t.Fatalf("%s: got %+v, %v; want 403 no_role", raw, opened, err)
		}
	}
	if len(c.Admins.(*fakeAdmins).rows) != 0 || len(log.written) != 0 {
		t.Fatal("somebody with no role here was written down")
	}
}

// The first sign-in is on the record under our id, with the provider's subject
// beside it. After that, nothing new happened.
func TestAFirstSignInIsOnTheRecord(t *testing.T) {
	c, _, _, _, log := console(t)
	for range 2 {
		opened, err := c.Open(context.Background(), "ali")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(opened.Roles, []string{"admin", "operator"}) {
			t.Fatalf("roles are %v, want each once, sorted", opened.Roles)
		}
	}
	if len(log.written) != 1 {
		t.Fatalf("the log holds %+v", log.written)
	}
	e := log.written[0]
	if e.Action != model.ActionAdminFirstSeen || e.Actor != "adm_user-2" || e.Detail["subject"] != "user-2" {
		t.Fatalf("wrote %+v", e)
	}
}

func TestEachRefusalAtSignIn(t *testing.T) {
	c, _, _, _, _ := console(t)

	for _, tc := range []struct {
		name, raw string
		want      int
		code      string
	}{
		{"expired", "expired", http.StatusUnauthorized, "invalid_token"},
		{"not one of the provider's", "abc123", http.StatusUnauthorized, "invalid_token"},
		// A refusal the provider package decided itself travels as it is.
		{"a status of the provider's own", "machine", http.StatusForbidden, "machine_user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Open(context.Background(), tc.raw)
			var e *apperr.Error
			if !errors.As(err, &e) || e.Status != tc.want || e.Code != tc.code {
				t.Fatalf("got %v, want %d %s", err, tc.want, tc.code)
			}
		})
	}
}

// The provider down: nothing can be checked, so nobody is let in, and it is
// not a refusal of the person either.
func TestTheProviderDownFailsClosed(t *testing.T) {
	c, _, sso, _, _ := console(t)
	sso.down = fmt.Errorf("%w: discovery timed out", ErrSSOUnavailable)

	_, err := c.Open(context.Background(), "ali")
	if status(err) != http.StatusBadGateway {
		t.Fatalf("got %v, want 502", err)
	}
}

// Ending somebody's sessions reaches every one they hold, and nobody else's,
// and the log says who did it.
func TestEndingSomebodysSessionsReachesThemAll(t *testing.T) {
	c, uc, _, _, log := console(t)
	ctx := context.Background()

	var hers []string
	for range 2 {
		opened, err := c.Open(ctx, "sara")
		if err != nil {
			t.Fatal(err)
		}
		hers = append(hers, opened.Token)
	}
	mine, err := c.Open(ctx, "ali")
	if err != nil {
		t.Fatal(err)
	}

	if err := c.EndAll(ctx, "adm_user-2", "adm_user-1"); err != nil {
		t.Fatal(err)
	}
	for i, tok := range hers {
		if answer, _ := uc.Of(ctx, "Bearer "+tok); answer.Session != nil {
			t.Errorf("her session %d still opens the console", i)
		}
	}
	if answer, _ := uc.Of(ctx, "Bearer "+mine.Token); answer.Session == nil {
		t.Error("ending hers ended mine")
	}

	last := log.written[len(log.written)-1]
	if last.Action != model.ActionAdminSessionsEnded || last.Actor != "adm_user-2" || last.Subject != "adm_user-1" {
		t.Fatalf("logged %+v", last)
	}
}

func TestEndingSessionsOfNobodyIsNotFound(t *testing.T) {
	c, _, _, _, _ := console(t)
	if err := c.EndAll(context.Background(), "adm_user-2", "adm_nobody"); status(err) != http.StatusNotFound {
		t.Fatalf("got %v, want 404", err)
	}
}

// A dev sign-in does not exist unless a laptop switched it on: a 404, as if
// there were no such route. Switched on, a name is an admin, under a subject
// no provider could issue.
func TestADevAdminIsRefusedUnlessAllowed(t *testing.T) {
	c, uc, _, _, log := console(t)
	ctx := context.Background()

	opened, err := c.OpenDev(ctx, "zayn")
	if opened != nil || status(err) != http.StatusNotFound {
		t.Fatalf("got %+v, %v; want 404", opened, err)
	}
	if len(c.Admins.(*fakeAdmins).rows) != 0 || len(log.written) != 0 {
		t.Fatal("a refused dev sign-in was written down")
	}

	c.DevAllowed = true
	if _, err := c.OpenDev(ctx, "  "); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("no name: got %v, want 422", err)
	}
	opened, err = c.OpenDev(ctx, "zayn")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Admin.Subject != "dev:zayn" || opened.Admin.Name != "zayn" || !slices.Equal(opened.Roles, []string{"admin"}) {
		t.Fatalf("got %+v", opened)
	}
	answer, err := uc.Of(ctx, "Bearer "+opened.Token)
	if err != nil || answer.Session == nil || !answer.Session.HasRole("admin") {
		t.Fatalf("the dev session reads as %+v, %v", answer, err)
	}
}

// The console's password: with none set the sign-in is not there at all. Set,
// the right password and a name are an admin, the same one for the same name
// however it is typed; the wrong one is a 401 that writes nobody down; every
// try counts, and past the limit the hash is not even asked.
func TestThePasswordSignIn(t *testing.T) {
	c, uc, _, _, log := console(t)
	ctx := context.Background()

	if opened, err := c.OpenPassword(ctx, "zayn", "open sesame"); opened != nil || status(err) != http.StatusNotFound {
		t.Fatalf("no password set: got %+v, %v; want 404", opened, err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("open sesame"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	limits := &countingLimiter{}
	c.Password, c.Limiter = hash, limits

	var invalid *apperr.Error
	_, err = c.OpenPassword(ctx, "  ", "")
	if !errors.As(err, &invalid) || invalid.Status != http.StatusUnprocessableEntity ||
		invalid.Fields["name"] != "required" || invalid.Fields["password"] != "required" {
		t.Fatalf("no name and no password: got %v, want 422 with both fields", err)
	}
	if _, err := c.OpenPassword(ctx, strings.Repeat("ز", model.MaxName+1), "open sesame"); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("a long name: got %v, want 422", err)
	}
	if limits.seen["console:password"] != 0 {
		t.Fatal("a refused field counted as a try")
	}
	if _, err := c.OpenPassword(ctx, "zayn", "open sesame!"); status(err) != http.StatusUnauthorized {
		t.Fatalf("the wrong password: got %v, want 401", err)
	}
	if len(c.Admins.(*fakeAdmins).rows) != 0 || len(log.written) != 0 {
		t.Fatal("a refused password sign-in was written down")
	}

	opened, err := c.OpenPassword(ctx, " Zayn ", "open sesame")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Admin.Subject != "password:zayn" || opened.Admin.Name != "Zayn" || !slices.Equal(opened.Roles, []string{"admin"}) {
		t.Fatalf("got %+v", opened)
	}
	again, err := c.OpenPassword(ctx, "ZAYN", "open sesame")
	if err != nil || again.Admin.ID != opened.Admin.ID {
		t.Fatalf("the same name twice is two admins: %+v, %v", again, err)
	}
	answer, err := uc.Of(ctx, "Bearer "+opened.Token)
	if err != nil || answer.Session == nil || !answer.Session.HasRole("admin") {
		t.Fatalf("the password session reads as %+v, %v", answer, err)
	}

	limits.seen["console:password"] = passwordTries
	if _, err := c.OpenPassword(ctx, "zayn", "open sesame"); status(err) != http.StatusTooManyRequests {
		t.Fatalf("past the limit, the right password: got %v, want 429", err)
	}
}
