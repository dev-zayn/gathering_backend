package identity_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// issuer is Issuer and Refresh as main wires them, over a test database.
func issuer(t *testing.T) (*identity.Issuer, *identity.Refresh, *gorm.DB, *biztest.Whitelist, *token.Signer) {
	t.Helper()
	db := biztest.DB(t)
	signer := biztest.Signer(t)
	wl := &biztest.Whitelist{}
	iss := &identity.Issuer{
		Sessions: identity.NewSessionStore(db), Tokens: identity.NewRefreshStore(db),
		Minter: identity.NewMinter(signer), Whitelist: wl,
		SessionTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	}
	ref := &identity.Refresh{
		Tokens: iss.Tokens, Sessions: iss.Sessions, Players: identity.NewPlayerStore(db),
		Whitelist: wl, Issuer: iss,
	}
	return iss, ref, db, wl, signer
}

func code(err error) string {
	if e, ok := err.(*errors.Error); ok {
		return e.Code
	}
	return ""
}

// A refresh is a new pair on the same session, not live. The spent token
// coming back means a copy is loose: the whole session dies, the new pair
// with it.
func TestARefreshRotatesAndAReplayKillsTheSession(t *testing.T) {
	iss, ref, db, wl, signer := issuer(t)
	ctx := context.Background()
	p := biztest.SeedPlayer(t, db, "+9647700000001", "Ali")

	first, err := iss.Open(ctx, p, "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ref.Do(ctx, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken || second.Player.ID != p.ID {
		t.Fatalf("nothing rotated: %+v", second)
	}
	a, _ := signer.Parse(first.AccessToken)
	b, _ := signer.Parse(second.AccessToken)
	if a.Session != b.Session || !a.Live || b.Live {
		t.Fatalf("the sessions read as %+v and %+v", a, b)
	}

	if _, err := ref.Do(ctx, first.RefreshToken); code(err) != "invalid_refresh_token" {
		t.Fatalf("a replay got %v", err)
	}
	sess, _ := iss.Sessions.ByID(ctx, a.Session)
	if sess.RevokedAt == nil || len(wl.Revoked) != 1 || wl.Revoked[0] != a.Session {
		t.Fatalf("the session survived a replay: %+v, revoked %v", sess, wl.Revoked)
	}
	if _, err := ref.Do(ctx, second.RefreshToken); code(err) != "invalid_refresh_token" {
		t.Fatalf("the new pair survived the replay: %v", err)
	}
}

// Switched off since they last used the app: the session goes with them.
func TestADisabledPlayerCannotRefresh(t *testing.T) {
	iss, ref, db, wl, _ := issuer(t)
	ctx := context.Background()
	p := biztest.SeedPlayer(t, db, "+9647700000001", "Ali")

	opened, err := iss.Open(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ref.Players.SetDisabled(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ref.Do(ctx, opened.RefreshToken); code(err) != "invalid_refresh_token" {
		t.Fatalf("got %v", err)
	}
	if len(wl.Revoked) != 1 {
		t.Fatal("the session was not ended")
	}
}

// Signing out kills the record: the refresh token is worthless after, and
// every token the session held is cleared.
func TestLogoutEndsTheSession(t *testing.T) {
	iss, ref, db, wl, signer := issuer(t)
	ctx := context.Background()
	p := biztest.SeedPlayer(t, db, "+9647700000001", "Ali")
	opened, err := iss.Open(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := signer.Parse(opened.AccessToken)

	if err := ref.Logout(ctx, claims.Session); err != nil {
		t.Fatal(err)
	}
	if _, err := ref.Do(ctx, opened.RefreshToken); code(err) != "invalid_refresh_token" {
		t.Fatalf("after signing out: %v", err)
	}
	if len(wl.Revoked) != 1 || wl.Revoked[0] != claims.Session {
		t.Fatalf("revoked %v", wl.Revoked)
	}
	live, _ := iss.Sessions.ListByPlayer(ctx, p.ID)
	if len(live) != 0 {
		t.Fatalf("still listed: %+v", live)
	}
}

// Not found, spent, revoked and expired all read alike, so a token in the
// wrong hands says nothing about which it is.
func TestAnUnknownRefreshTokenIsInvalid(t *testing.T) {
	_, ref, _, _, _ := issuer(t)
	_, err := ref.Do(context.Background(), "rft_nobodyhasthisone")
	if e, ok := err.(*errors.Error); !ok || e.Status != http.StatusUnauthorized || e.Code != "invalid_refresh_token" {
		t.Fatalf("got %v", err)
	}
}
