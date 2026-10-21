package authn_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	// For its init: internal/http replaces huma.NewError with our body shape. It
	// imports authn, which is why this is an external test package.
	_ "github.com/jamaa/gathering/backend/internal/http"

	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// A Resolver rather than a signing key, because that is the seam: whether a
// token parses or was revoked is identity's answer and is tested there.

const public = "https://api.gathering.test"

// resolved is what the resolver will answer for the next request, so a test
// says who is calling by assigning to it rather than by minting anything.
type resolved struct {
	session *caller.Session
	reason  string
	err     error
}

func testAPI(t *testing.T, who *resolved) humatest.TestAPI {
	t.Helper()

	_, api := humatest.New(t, huma.DefaultConfig("test", "1"))
	api.UseMiddleware(authn.Auth(api, public,
		func(context.Context, string) (*caller.Session, string, error) {
			return who.session, who.reason, who.err
		}, authn.Cookie{}))

	register := func(id, path string, meta map[string]any) {
		huma.Register(api, huma.Operation{
			OperationID: id, Method: http.MethodGet, Path: path, Metadata: meta,
		}, func(ctx context.Context, _ *struct{}) (*struct{ Body string }, error) {
			// Echo what the handler can see, so a test can tell that the
			// session actually reached it.
			out := &struct{ Body string }{}
			if s := caller.FromContext(ctx); s != nil {
				out.Body = s.Subject
			}
			return out, nil
		})
	}

	register("open", "/open", nil)
	register("session", "/session", map[string]any{authn.ScopeKey: caller.ScopeSession})
	register("host", "/owner", map[string]any{authn.ScopeKey: caller.ScopeSession, authn.RoleKey: "host"})
	register("app", "/app", map[string]any{authn.ScopeKey: caller.ScopeGame})
	register("notify", "/notify", map[string]any{
		authn.ScopeKey: caller.ScopeGame, authn.PermKey: "notify:send",
	})
	register("otp", "/otp", map[string]any{authn.ScopeKey: caller.ScopeOTP})
	register("console", "/console", map[string]any{authn.ScopeKey: caller.ScopeAdmin})
	register("console-admin", "/console-admin", map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: "admin"})
	register("mcp", "/mcp", map[string]any{authn.ScopeKey: caller.ScopeRoom})
	// A role declared as a named string type, the way enum.Role is.
	register("typed-role", "/typed-role", map[string]any{authn.ScopeKey: caller.ScopeSession, authn.RoleKey: typedRole("host")})

	return api
}

// caller is a resolver that names somebody.
func somebody(s caller.Session) *resolved { return &resolved{session: &s} }

// nobody is a resolver that does not, and says why.
func nobody(reason string) *resolved { return &resolved{reason: reason} }

// An operation with no declared scope is public, and nothing is asked for it.
func TestPublicOperationNeedsNothing(t *testing.T) {
	if got := testAPI(t, nobody("missing_token")).Get("/open").Code; got != http.StatusOK {
		t.Fatalf("a public operation answered %d", got)
	}
}

// The reasons nobody is calling, and the differences a client acts on: sign in
// again, refresh, or stop sending that token.
func TestNoIdentityIsRefused(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"nothing at all", "missing_token"},
		{"a token that does not parse", "invalid_token"},
		{"a token that has expired", "expired_token"},
		{"a token that has been revoked", "revoked_token"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := testAPI(t, nobody(c.want)).Get("/session")
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("answered %d, want 401", res.Code)
			}
			if body := res.Body.String(); !strings.Contains(body, `"`+c.want+`"`) {
				t.Fatalf("the refusal said %s, want %s", body, c.want)
			}
		})
	}
}

// A token of the wrong kind is not a weaker token, it is the wrong one.
func TestWrongKindIsRefused(t *testing.T) {
	otp := testAPI(t, somebody(caller.Session{Scope: caller.ScopeOTP, Phone: "+9647700000001"}))
	if got := otp.Get("/session").Code; got != http.StatusForbidden {
		t.Fatalf("an otp token opened a session endpoint: %d", got)
	}

	session := testAPI(t, somebody(caller.Session{Scope: caller.ScopeSession, Room: "room_1"}))
	if got := session.Get("/otp").Code; got != http.StatusForbidden {
		t.Fatalf("a session token opened an otp endpoint: %d", got)
	}
}

func TestTheSessionReachesTheHandler(t *testing.T) {
	api := testAPI(t, somebody(caller.Session{
		Scope: caller.ScopeSession, Subject: "ply_9md4", Room: "room_1", Role: "host",
	}))

	res := api.Get("/session")
	if res.Code != http.StatusOK {
		t.Fatalf("a named caller gave %d", res.Code)
	}
	if body := strings.TrimSpace(res.Body.String()); body != `"ply_9md4"` {
		t.Fatalf("the handler saw %s", body)
	}
}

type typedRole string

// A rule that is not a plain string used to read as no rule, and an operator
// passed an owner-only route. It is a mistake in the route, so nobody passes,
// the owner included, and the mistake shows up as a 500 with its reason logged.
func TestARuleNotWrittenAsAStringRefusesEverybody(t *testing.T) {
	for _, role := range []string{"player", "host"} {
		api := testAPI(t, somebody(caller.Session{Scope: caller.ScopeSession, Room: "room_1", Role: role}))
		if got := api.Get("/typed-role").Code; got != http.StatusInternalServerError {
			t.Fatalf("an %s got %d", role, got)
		}
	}
}

func TestRoleIsChecked(t *testing.T) {
	operator := testAPI(t, somebody(caller.Session{Scope: caller.ScopeSession, Room: "room_1", Role: "player"}))
	if got := operator.Get("/owner").Code; got != http.StatusForbidden {
		t.Fatalf("an operator opened an owner-only operation: %d", got)
	}

	owner := testAPI(t, somebody(caller.Session{Scope: caller.ScopeSession, Room: "room_1", Role: "host"}))
	if got := owner.Get("/owner").Code; got != http.StatusOK {
		t.Fatalf("an owner was refused an owner-only operation: %d", got)
	}
}

// An admin's roles come from the identity provider as a list. No console route narrows by
// role yet; this is the mechanism the first one will use.
func TestAnAdminRoleIsCheckedAmongTheirRoles(t *testing.T) {
	operator := testAPI(t, somebody(caller.Session{Scope: caller.ScopeAdmin, Roles: []string{"player"}}))
	if got := operator.Get("/console").Code; got != http.StatusOK {
		t.Fatalf("an operator was refused the console: %d", got)
	}
	if got := operator.Get("/console-admin").Code; got != http.StatusForbidden {
		t.Fatalf("an operator opened an admin-only route: %d", got)
	}

	both := testAPI(t, somebody(caller.Session{Scope: caller.ScopeAdmin, Roles: []string{"admin", "player"}}))
	if got := both.Get("/console-admin").Code; got != http.StatusOK {
		t.Fatalf("an admin was refused an admin-only route: %d", got)
	}
}

// A good token that holds nothing here is a 403 the resolver decided, and it is
// sent as it is — not a 401 that would send them round to sign in again.
func TestARefusalFromTheResolverIsSentAsItIs(t *testing.T) {
	res := testAPI(t, &resolved{err: apperr.Forbidden("no_role")}).Get("/console")
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), `"no_role"`) {
		t.Fatalf("got %d %s, want 403 no_role", res.Code, res.Body)
	}
	if res.Result().Header.Get("WWW-Authenticate") == "" {
		t.Error("the 403 carried no challenge")
	}

	// A 5xx from resolving is not a refusal of the person: still identity_unavailable.
	res = testAPI(t, &resolved{err: apperr.Internal(errors.New("db"))}).Get("/console")
	if res.Code < 500 || !strings.Contains(res.Body.String(), "identity_unavailable") {
		t.Fatalf("got %d %s, want identity_unavailable", res.Code, res.Body)
	}
}

func TestPermissionIsChecked(t *testing.T) {
	api := testAPI(t, somebody(caller.Session{
		Kind: caller.ScopeGame, Scope: "identity:read friends:read", Room: "room_1",
	}))
	res := api.Get("/notify")
	if res.Code != http.StatusForbidden {
		t.Fatalf("an app without notify:send raised a notification: %d", res.Code)
	}
	// The body names the scope, so a partner knows which one to ask for.
	if body := res.Body.String(); !strings.Contains(body, `"scope":"notify:send"`) ||
		!strings.Contains(body, `"error":"scope_required"`) {
		t.Fatalf("the refusal did not name the scope: %s", body)
	}

	with := testAPI(t, somebody(caller.Session{
		Kind: caller.ScopeGame, Scope: "identity:read notify:send", Room: "room_1",
	}))
	if got := with.Get("/notify").Code; got != http.StatusOK {
		t.Fatalf("an app with notify:send was refused: %d", got)
	}
}

// A refusal has to say where a right token comes from: the header on the way out
// is the only place an MCP client learns that address.
func TestARefusalNamesWhereToGetAToken(t *testing.T) {
	api := testAPI(t, nobody("missing_token"))

	want := `Bearer error="invalid_token", ` +
		`resource_metadata="` + public + `/.well-known/oauth-protected-resource"`
	if got := api.Get("/session").Result().Header.Get("WWW-Authenticate"); got != want {
		t.Fatalf("no token answered\n got %q\nwant %q", got, want)
	}

	// The wrong kind of token is a 403, and still a dead end without this.
	wrong := testAPI(t, somebody(caller.Session{Scope: caller.ScopeOTP}))
	if got := wrong.Get("/session").Result().Header.Get("WWW-Authenticate"); got == "" {
		t.Fatal("the wrong kind of token got no challenge")
	}
}

// A scope never granted is a different move — ask for it, not sign in again.
// RFC 6750 names that, and this is where a client reads it.
func TestAMissingScopeAsksForThatScope(t *testing.T) {
	api := testAPI(t, somebody(caller.Session{
		Scope: "identity:read", Kind: caller.ScopeGame,
	}))

	got := api.Get("/notify").Result().Header.Get("WWW-Authenticate")

	want := `Bearer error="insufficient_scope", scope="notify:send", ` +
		`resource_metadata="` + public + `/.well-known/oauth-protected-resource"`
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}
