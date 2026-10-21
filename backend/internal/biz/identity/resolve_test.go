package identity

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

func testIdentity(t *testing.T) (*Identities, *token.Signer, *fakeWhitelist) {
	t.Helper()

	signer := biztest.Signer(t)
	wl := &fakeWhitelist{}
	return &Identities{Signer: signer, Whitelist: wl}, signer, wl
}

func mint(t *testing.T, s *token.Signer, c token.Claims, ttl time.Duration) string {
	t.Helper()
	raw, _, err := s.Mint(c, ttl)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return "Bearer " + raw
}

func TestNoTokenIsNobody(t *testing.T) {
	uc, _, _ := testIdentity(t)

	for _, header := range []string{"", "Basic abc", "Bearer ", "Bearer nonsense"} {
		answer, err := uc.Of(context.Background(), header)
		if err != nil {
			t.Fatalf("%q: %v", header, err)
		}
		if answer.Session != nil {
			t.Fatalf("%q was let through", header)
		}
		if answer.Reason == "" {
			t.Fatalf("%q got no reason", header)
		}
	}
}

func TestAGoodTokenBecomesASession(t *testing.T) {
	uc, signer, _ := testIdentity(t)

	c := token.Claims{Scope: caller.ScopeSession, Session: "ses_1", Live: true}
	c.Subject = "ply_9md4"

	answer, err := uc.Of(context.Background(), mint(t, signer, c, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if answer.Session == nil {
		t.Fatal("a good token produced no session")
	}
	// Every field a handler reads has to survive the trip, and the only thing
	// that would catch one going missing is this.
	s := answer.Session
	if s.Subject != "ply_9md4" || s.Session != "ses_1" || !s.Live || !s.Is(caller.ScopeSession) {
		t.Fatalf("the session lost something: %+v", s)
	}
}

// A game's token names the game, carries its permissions and, opened into a
// room, the room: the bridge reads all three.
func TestAGameTokenKeepsItsAudienceAndRoom(t *testing.T) {
	uc, signer, wl := testIdentity(t)
	raw, _, err := NewMinter(signer).MintGame(GameClaims{
		GameID: "gam_x", PlayerID: "ply_1", RoomID: "room_1", Scopes: []string{"identity:read", "friends:read"}, Live: true,
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	answer, err := uc.Of(context.Background(), "Bearer "+raw)
	if err != nil {
		t.Fatal(err)
	}
	s := answer.Session
	if s == nil || !s.Is(caller.ScopeGame) || s.Game() != "gam_x" || s.Room != "room_1" || s.Subject != "ply_1" ||
		!s.Has("friends:read") || s.Has("notify:send") || !s.Live {
		t.Fatalf("the game token reads as %+v", answer)
	}
	if len(wl.asked) != 1 {
		t.Fatal("a game token was not checked against the revocation list")
	}
}

// A room ticket is ours, signed and current, and for a game's server: here it
// is a 403, not a 401, so the client does not go and sign in again. The
// whitelist is never asked, because it was never listed.
func TestARoomTicketOpensNothingHere(t *testing.T) {
	uc, signer, wl := testIdentity(t)
	raw, _, err := NewMinter(signer).MintRoom(RoomClaims{
		GameID: "gam_x", PlayerID: "ply_1", RoomID: "room_1", Role: "host", Seat: 2,
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// What the game's server reads through the JWKS.
	claims, err := signer.Parse(raw)
	if err != nil || !claims.Is(caller.ScopeRoom) || claims.Game() != "gam_x" || claims.Room != "room_1" || claims.Role != "host" || claims.Seat != 2 {
		t.Fatalf("the ticket reads as %+v, %v", claims, err)
	}

	answer, err := uc.Of(context.Background(), "Bearer "+raw)
	var e *apperr.Error
	if !errors.As(err, &e) || e.Status != http.StatusForbidden || e.Code != "wrong_token" || answer.Session != nil {
		t.Fatalf("got %+v, %v; want 403 wrong_token", answer, err)
	}
	if len(wl.asked) != 0 {
		t.Fatal("the revocation list was asked about a room ticket")
	}
}

// An expired token is a different move for the client from a bad one: refresh,
// rather than sign in again.
func TestExpiryIsToldApartFromNonsense(t *testing.T) {
	uc, signer, _ := testIdentity(t)

	c := token.Claims{Scope: caller.ScopeSession}
	answer, err := uc.Of(context.Background(), mint(t, signer, c, -time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if answer.Reason != "expired_token" {
		t.Fatalf("an expired token read as %q", answer.Reason)
	}
}

// A revoked token is signed and unexpired, and must still be refused. This is
// what makes signing out and leaving a game take effect at once.
func TestRevokedTokenIsRefused(t *testing.T) {
	for _, kind := range []string{caller.ScopeSession, caller.ScopeGame} {
		t.Run(kind, func(t *testing.T) {
			uc, signer, wl := testIdentity(t)
			wl.invalid = true

			c := token.Claims{Scope: kind}
			if kind == caller.ScopeGame {
				c = token.Claims{Kind: kind, Scope: "identity:read"}
			}

			answer, err := uc.Of(context.Background(), mint(t, signer, c, time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if answer.Session != nil {
				t.Fatal("a revoked token was let through")
			}
			if answer.Reason != "revoked_token" {
				t.Fatalf("read as %q", answer.Reason)
			}
			if len(wl.asked) == 0 {
				t.Fatal("the revocation list was never consulted")
			}
		})
	}
}

// If we cannot tell whether a token was revoked, we behave as though it was: a
// 502, and the request never reaches a rule.
func TestUnreadableWhitelistFailsClosed(t *testing.T) {
	uc, signer, wl := testIdentity(t)
	wl.validErr = errors.New("redis is down")

	c := token.Claims{Scope: caller.ScopeSession}
	answer, err := uc.Of(context.Background(), mint(t, signer, c, time.Minute))
	if err == nil {
		t.Fatalf("an unreadable revocation list produced an answer: %+v", answer)
	}
}

// An otp token is never listed, so asking would make signing in depend on Redis
// at exactly the moment a player has no other way in.
func TestShortLivedKindsSkipTheWhitelist(t *testing.T) {
	uc, signer, wl := testIdentity(t)
	wl.validErr = errors.New("redis is down")

	c := token.Claims{Scope: caller.ScopeOTP, Phone: "+9647700000001"}
	answer, err := uc.Of(context.Background(), mint(t, signer, c, time.Minute))
	if err != nil {
		t.Fatalf("an otp token was refused while Redis was down: %v", err)
	}
	if answer.Session == nil || answer.Session.Phone != "+9647700000001" {
		t.Fatalf("an otp token produced %+v", answer)
	}
	if len(wl.asked) != 0 {
		t.Fatalf("the revocation list was consulted for an otp token: %v", wl.asked)
	}
}

// The console's session is ours: the provider's token was checked once, at
// sign-in, and the session carries the roles it held then.
func TestAnAdminSessionResolvesWithItsRoles(t *testing.T) {
	uc, signer, wl := testIdentity(t)
	c := token.Claims{Scope: caller.ScopeAdmin, Roles: []string{"operator"}}
	c.Subject = "adm_1"

	answer, err := uc.Of(context.Background(), mint(t, signer, c, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	s := answer.Session
	if s == nil || !s.Is(caller.ScopeAdmin) || s.Subject != "adm_1" || !slices.Equal(s.Roles, []string{"operator"}) {
		t.Fatalf("got %+v", answer)
	}
	if len(wl.asked) != 1 {
		t.Error("the session was not moved on: asking is what keeps it alive")
	}
}

// Signed out, or idle past its time: the whitelist no longer has it.
func TestAnEndedAdminSessionIsRevoked(t *testing.T) {
	uc, signer, wl := testIdentity(t)
	wl.invalid = true
	c := token.Claims{Scope: caller.ScopeAdmin, Roles: []string{"admin"}}
	c.Subject = "adm_1"

	answer, err := uc.Of(context.Background(), mint(t, signer, c, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if answer.Session != nil || answer.Reason != "revoked_token" {
		t.Fatalf("got %+v", answer)
	}
}

// An admin token with no roles is not one Console.Open made.
func TestAnAdminTokenWithNoRolesIsRefused(t *testing.T) {
	uc, signer, _ := testIdentity(t)
	c := token.Claims{Scope: caller.ScopeAdmin, Role: "admin"}
	c.Subject = "adm_old"

	answer, err := uc.Of(context.Background(), mint(t, signer, c, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if answer.Session != nil || answer.Reason != "invalid_token" {
		t.Fatalf("an old admin token read as %+v", answer)
	}
}

// A token another key signed is somebody else's token, and nothing: the
// provider's own token is for signing in, once, and never a session here.
func TestAnotherSignersTokenIsNotASession(t *testing.T) {
	uc, _, _ := testIdentity(t)
	other := biztest.Signer(t)

	c := token.Claims{Scope: caller.ScopeAdmin, Roles: []string{"admin"}}
	answer, err := uc.Of(context.Background(), mint(t, other, c, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if answer.Session != nil || answer.Reason != "invalid_token" {
		t.Fatalf("another signer's token read as %+v", answer)
	}
}
