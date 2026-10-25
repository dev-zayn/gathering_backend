package http

import (
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/http/authn"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// publicOperations is every route reachable with no token, one id at a time so
// adding to it is a decision a reviewer can see.
var publicOperations = map[string]string{
	"health": "the load balancer has no token and the answer names nothing",
	"ready":  "the same, and says only ok or not_ready",

	// Getting a token cannot require a token.
	"auth-signin":  "asks for the OTP; this is where a session starts",
	"auth-sso":     "a provider's token would be the credential; today it answers not_supported",
	"auth-refresh": "the refresh token is the credential, and it is in the body",
	// The provider's token is the credential, checked by the handler: the authn
	// middleware only reads tokens of ours. The dev sign-in exists only with DEBUG.
	"admin-session-open":     "trades a provider token for a console session",
	"admin-session-dev":      "a laptop's sign-in; refused outright unless DEBUG and DEV_ADMIN",
	"admin-session-password": "the console's password sign-in; not found unless CONSOLE_PASSWORD_HASH",

	// The game's own server proves who it is with its client secret, in the
	// form body; there is no token of ours to hold before this answers.
	"oauth-token": "trades a one-time code, or a refresh token, for a game token; the client secret is the credential",

	// RFC 9728 and RFC 7517. A client reads these before it has anything,
	// which is the whole point of discovery.
	"oauth-protected-resource": "discovery: names the authorization server for this API",
	"openid-configuration":     "discovery: the OIDC spelling of the same document",
	"jwks":                     "the public half of the signing key, which is public by definition",

	// The invite link (docs/JOINING.md). Whoever opens a room's link holds
	// nothing of ours yet, and neither does a phone checking that the link
	// host is the app's before it lets the app catch the link.
	"invite-link":                "the page behind a room's link: the game and the code, and nothing else about the room",
	"tournament-link":            "the page behind a tournament's link (docs/TOURNAMENTS.md): its name, its game and its code, nothing else",
	"assetlinks":                 "Android's check of the link host: the app's package and certificate fingerprints, public by design",
	"apple-app-site-association": "iOS's check of the link host: the app's id and the paths it takes, public by design",
}

// atLeast is a floor, not a count: it only has to fail if Mount silently stops
// registering, which is the one way this test could pass by examining nothing.
const atLeast = 40

// A missing scope means public, so forgetting the line looks exactly like
// deciding not to need one. The list above is the claim; this is the check.
func TestEveryRouteIsGuardedOrNamedPublic(t *testing.T) {
	_, api := New("gathering", "test", applog.New("gathering"))
	Mount(api, Deps{})

	seen := 0
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{
			item.Get, item.Post, item.Put, item.Patch, item.Delete,
		} {
			if op == nil {
				continue
			}
			seen++

			if _, ok := op.Metadata[authn.ScopeKey]; ok {
				continue
			}
			if why, ok := publicOperations[op.OperationID]; ok {
				t.Logf("public on purpose: %s %s — %s", op.Method, path, why)
				continue
			}
			t.Errorf("%s %s (%s) declares no scope and is not named public:\n"+
				"anyone can call it with no token at all", op.Method, path, op.OperationID)
		}
	}

	if seen < atLeast {
		t.Fatalf("walked %d operations, expected at least %d — Mount is not\n"+
			"registering what it used to, so this test is checking nothing", seen, atLeast)
	}
}

// An exception that applies to nothing cannot be seen to be wrong, and the next
// route to take the name inherits it. A rule that cannot fail is not a rule.
func TestNothingIsExcusedThatDoesNotExist(t *testing.T) {
	_, api := New("gathering", "test", applog.New("gathering"))
	Mount(api, Deps{})

	registered := map[string]bool{}
	for _, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{
			item.Get, item.Post, item.Put, item.Patch, item.Delete,
		} {
			if op != nil {
				registered[op.OperationID] = true
			}
		}
	}

	for id, why := range publicOperations {
		if !registered[id] {
			t.Errorf("publicOperations names %q, which no route registers:\n"+
				"  %s\nit excuses nothing, and would excuse whatever takes the name next", id, why)
		}
	}
}

// A rule is read as a plain string. enum.RoleOwner is not one, and once read
// that way it used to mean no rule at all: an operator uninstalled apps. authn
// now refuses such a route outright; this finds it before anyone calls it.
func TestEveryRuleIsAPlainString(t *testing.T) {
	_, api := New("gathering", "test", applog.New("gathering"))
	Mount(api, Deps{})

	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{
			item.Get, item.Post, item.Put, item.Patch, item.Delete,
		} {
			if op == nil {
				continue
			}
			for _, key := range []string{authn.ScopeKey, authn.RoleKey, authn.PermKey} {
				v, ok := op.Metadata[key]
				if !ok {
					continue
				}
				if s, isString := v.(string); !isString || s == "" {
					t.Errorf("%s %s (%s) declares %s as %T %v, not a plain string",
						op.Method, path, op.OperationID, key, v, v)
				}
			}
		}
	}
}

// consoleOpenToEveryRole is every console change an operator may make as well
// as an admin, one id at a time like the public list above.
var consoleOpenToEveryRole = map[string]string{
	"admin-session-close": "anybody signed in may sign themselves out",
}

// Reading is any console role's; changing something is an admin's. A console
// route that changes something and forgets the role is open to every
// operator, which looks exactly like deciding it should be: the rooms' close
// and the announcements are the newest of these.
func TestConsoleChangesNeedTheAdminRole(t *testing.T) {
	_, api := New("gathering", "test", applog.New("gathering"))
	Mount(api, Deps{})

	checked := 0
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Post, item.Put, item.Patch, item.Delete} {
			if op == nil || op.Metadata[authn.ScopeKey] != "admin" {
				continue
			}
			if why, ok := consoleOpenToEveryRole[op.OperationID]; ok {
				t.Logf("any role on purpose: %s %s — %s", op.Method, path, why)
				continue
			}
			checked++
			if op.Metadata[authn.RoleKey] != "admin" {
				t.Errorf("%s %s (%s) changes something from the console without the admin role",
					op.Method, path, op.OperationID)
			}
		}
	}
	for _, id := range []string{"admin-room-close", "admin-notify"} {
		if !registered(api, id) {
			t.Errorf("%s is not registered", id)
		}
	}
	if checked == 0 {
		t.Fatal("no console change was checked: Mount is not registering what it used to")
	}
}

// registered says whether an operation id is mounted.
func registered(api huma.API, id string) bool {
	for _, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op != nil && op.OperationID == id {
				return true
			}
		}
	}
	return false
}
