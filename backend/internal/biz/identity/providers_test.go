package identity

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// google stands in for Google: the token is the subject, "bad" is a token it
// does not stand behind, and "down" is Google out of reach.
var google = IDTokenFunc(func(_ context.Context, raw string) (string, error) {
	switch raw {
	case "bad":
		return "", fmt.Errorf("%w: signature", ErrSSOInvalid)
	case "down":
		return "", fmt.Errorf("%w: keys", ErrSSOUnavailable)
	}
	return raw, nil
})

// providerSignIn is ProviderSignIn as main wires it with Google alone: the
// real stores on a test database, and a resolver that reads what it mints.
func providerSignIn(t *testing.T) (*ProviderSignIn, *gorm.DB, *fakeAudit, *token.Signer) {
	t.Helper()
	db := biztest.DB(t)
	signer := biztest.Signer(t)
	minter := NewMinter(signer)
	log := &fakeAudit{}
	return &ProviderSignIn{
		Providers: map[enum.Provider]IDToken{enum.ProviderGoogle: google},
		Players:   NewPlayerStore(db),
		Minter:    minter,
		Issuer: &Issuer{
			Sessions: NewSessionStore(db), Tokens: NewRefreshStore(db), Minter: minter,
			Whitelist: &fakeWhitelist{}, SessionTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		},
		Audit:     log,
		SignupTTL: 15 * time.Minute,
	}, db, log, signer
}

// seedAccount is a player who signed up with Google as subject.
func seedAccount(t *testing.T, db *gorm.DB, subject, nickname string) *model.Player {
	t.Helper()
	p := biztest.SeedPlayer(t, db, "", nickname)
	if err := NewPlayerStore(db).AddIdentity(context.Background(), &model.Identity{
		Provider: enum.ProviderGoogle, Subject: subject, PlayerID: p.ID,
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// An account nobody has signed up with is a signup ticket carrying it, and
// nothing is written: no player, no session, no line in the log.
func TestAnUnknownAccountGetsASignupTicket(t *testing.T) {
	s, db, log, signer := providerSignIn(t)

	out, err := s.Do(context.Background(), enum.ProviderGoogle, "g-1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Scope != caller.ScopeSignup || out.RefreshToken != "" || out.Player != nil || out.ExpiresIn != 900 {
		t.Fatalf("got %+v", out)
	}
	claims, err := signer.Parse(out.AccessToken)
	if err != nil || !claims.Is(caller.ScopeSignup) || claims.Phone != "" ||
		claims.Provider != "google" || claims.ProviderSubject != "g-1" {
		t.Fatalf("the ticket reads as %+v, %v", claims, err)
	}

	var players, sessions int64
	db.Table("players").Count(&players)
	db.Table("sessions").Count(&sessions)
	if players != 0 || sessions != 0 || len(log.written) != 0 {
		t.Fatalf("something was written: %d players, %d sessions, %+v", players, sessions, log.written)
	}
}

// A known account is a session for its player, a sighting, and a line in the
// log naming the provider.
func TestAKnownAccountGetsASession(t *testing.T) {
	s, db, log, signer := providerSignIn(t)
	ctx := context.Background()
	p := seedAccount(t, db, "g-1", "Sara")

	out, err := s.Do(ctx, enum.ProviderGoogle, "g-1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Scope != caller.ScopeSession || out.RefreshToken == "" || out.Player == nil || out.Player.ID != p.ID {
		t.Fatalf("got %+v", out)
	}
	claims, err := signer.Parse(out.AccessToken)
	if err != nil || claims.Subject != p.ID || claims.Session == "" {
		t.Fatalf("the token reads as %+v, %v", claims, err)
	}
	seen, _ := NewPlayerStore(db).ByID(ctx, p.ID)
	if seen.LastSeenAt == nil {
		t.Error("signing in was not a sighting")
	}
	if len(log.written) != 1 || log.written[0].Action != model.ActionSignedIn ||
		log.written[0].Detail["provider"] != enum.ProviderGoogle {
		t.Fatalf("the log holds %+v", log.written)
	}

	// The same subject at another provider is somebody else.
	s.Providers[enum.ProviderApple] = google
	other, err := s.Do(ctx, enum.ProviderApple, "g-1")
	if err != nil || other.Scope != caller.ScopeSignup {
		t.Fatalf("apple's g-1 got %+v, %v", other, err)
	}
}

// The refusals, in the contract's order: no client ids, the provider out of
// reach, a token it does not stand behind, a player switched off. None of
// them opens a session.
func TestProviderRefusals(t *testing.T) {
	s, db, log, _ := providerSignIn(t)
	ctx := context.Background()
	off := seedAccount(t, db, "g-off", "Off")
	if err := NewPlayerStore(db).SetDisabled(ctx, off.ID, true); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		provider enum.Provider
		token    string
		status   int
		code     string
	}{
		{"apple has no client ids here", enum.ProviderApple, "a-1", http.StatusNotImplemented, "not_supported"},
		{"google out of reach", enum.ProviderGoogle, "down", http.StatusBadGateway, "provider_unavailable"},
		{"a token google does not stand behind", enum.ProviderGoogle, "bad", http.StatusUnauthorized, "invalid_provider_token"},
		{"no subject", enum.ProviderGoogle, "", http.StatusUnauthorized, "invalid_provider_token"},
		{"switched off", enum.ProviderGoogle, "g-off", http.StatusForbidden, "player_disabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Do(ctx, tc.provider, tc.token)
			var e *errors.Error
			if !stderrors.As(err, &e) || e.Status != tc.status || e.Code != tc.code {
				t.Fatalf("got %v, want %d %s", err, tc.status, tc.code)
			}
		})
	}

	var sessions int64
	db.Table("sessions").Count(&sessions)
	if sessions != 0 {
		t.Fatal("a refusal opened a session")
	}
	if len(log.written) != 1 || log.written[0].Action != model.ActionRefused || log.written[0].Detail["reason"] != "disabled" {
		t.Fatalf("the log holds %+v", log.written)
	}
}
