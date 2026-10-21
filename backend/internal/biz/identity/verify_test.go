package identity

import (
	"context"
	stderrors "errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// staleOTP is what a request already in flight sees: it read the code before
// anybody cleared it, so the code is still there for it. Only the counter is
// shared and current, as Redis's INCR is.
type staleOTP struct {
	mu    sync.Mutex
	hash  string
	tries int
}

func (s *staleOTP) Put(context.Context, string, string, time.Duration) error     { return nil }
func (s *staleOTP) Replace(context.Context, string, string, time.Duration) error { return nil }
func (s *staleOTP) Clear(context.Context, string) error                          { return nil }

func (s *staleOTP) Get(context.Context, string) (Pending, bool, error) {
	return Pending{Hash: s.hash}, true, nil
}

func (s *staleOTP) Attempt(context.Context, string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tries++
	return s.tries, nil
}

// Five guesses per code, however many arrive together. Requests that read the
// code before the fifth wrong guess cleared it must not get to compare: the
// sixth is refused even when it is right.
func TestNoGuessIsComparedPastTheLimit(t *testing.T) {
	v := &Verify{
		Codes:  &staleOTP{hash: model.HashCode("424242")},
		Policy: VerifyPolicy{MaxAttempts: 5},
	}
	for i := range 5 {
		if _, err := v.Do(context.Background(), "+9647700000001", "000000"); !isWrongCode(err) {
			t.Fatalf("guess %d: err = %v", i+1, err)
		}
	}

	_, err := v.Do(context.Background(), "+9647700000001", "424242")
	var e *errors.Error
	if !stderrors.As(err, &e) || e.Code != "wrong_code" || e.AttemptsLeft == nil || *e.AttemptsLeft != 0 {
		t.Fatalf("the sixth guess, the right one, got %v", err)
	}
}

func isWrongCode(err error) bool {
	var e *errors.Error
	return stderrors.As(err, &e) && e.Code == "wrong_code"
}

// verify is Verify as main wires it: the real stores on a test database, the
// code store in memory, and a resolver that reads what it mints.
func verify(t *testing.T) (*Verify, *memOTP, *gorm.DB, *fakeAudit, *token.Signer) {
	t.Helper()
	db := biztest.DB(t)
	signer := biztest.Signer(t)
	minter := NewMinter(signer)
	codes, log := &memOTP{}, &fakeAudit{}
	return &Verify{
		Codes: codes, Audit: log, Players: NewPlayerStore(db), Minter: minter,
		Issuer: &Issuer{
			Sessions: NewSessionStore(db), Tokens: NewRefreshStore(db), Minter: minter,
			Whitelist: &fakeWhitelist{}, SessionTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		},
		Policy: VerifyPolicy{MaxAttempts: 5, SignupTTL: 15 * time.Minute},
	}, codes, db, log, signer
}

const phone1 = "+9647700000001"

// A known number with the right code is a session: a token the resolver reads
// as this player on this device, a refresh token, and a line in the log. The
// code is spent by it.
func TestAKnownNumberGetsASession(t *testing.T) {
	v, codes, db, log, signer := verify(t)
	ctx := context.Background()
	p := biztest.SeedPlayer(t, db, phone1, "Ali")
	_ = codes.Put(ctx, phone1, model.HashCode("424242"), time.Minute)

	out, err := v.Do(ctx, phone1, "424242")
	if err != nil {
		t.Fatal(err)
	}
	if out.Scope != caller.ScopeSession || out.RefreshToken == "" || out.Player == nil || out.Player.ID != p.ID {
		t.Fatalf("got %+v", out)
	}
	claims, err := signer.Parse(out.AccessToken)
	if err != nil || claims.Subject != p.ID || claims.Session == "" || !claims.Live {
		t.Fatalf("the token reads as %+v, %v", claims, err)
	}

	seen, _ := NewPlayerStore(db).ByID(ctx, p.ID)
	if seen.LastSeenAt == nil {
		t.Error("signing in was not a sighting")
	}
	if len(log.written) != 1 || log.written[0].Action != model.ActionSignedIn || log.written[0].Actor != p.ID {
		t.Fatalf("the log holds %+v", log.written)
	}

	if _, err := v.Do(ctx, phone1, "424242"); status(err) != http.StatusUnauthorized {
		t.Fatalf("the code was not spent: %v", err)
	}
}

// A number nobody has registered gets a signup ticket and nothing else: no
// session, no row, no line in the log.
func TestAnUnknownNumberGetsASignupTicketAndNothingIsWritten(t *testing.T) {
	v, codes, db, log, signer := verify(t)
	ctx := context.Background()
	_ = codes.Put(ctx, phone1, model.HashCode("424242"), time.Minute)

	out, err := v.Do(ctx, phone1, "424242")
	if err != nil {
		t.Fatal(err)
	}
	if out.Scope != caller.ScopeSignup || out.RefreshToken != "" || out.Player != nil {
		t.Fatalf("got %+v", out)
	}
	claims, err := signer.Parse(out.AccessToken)
	if err != nil || claims.Phone != phone1 || !claims.Is(caller.ScopeSignup) {
		t.Fatalf("the ticket reads as %+v, %v", claims, err)
	}

	var players, sessions int64
	db.Table("players").Count(&players)
	db.Table("sessions").Count(&sessions)
	if players != 0 || sessions != 0 || len(log.written) != 0 {
		t.Fatalf("something was written: %d players, %d sessions, %+v", players, sessions, log.written)
	}
}

// Switched off by an admin: the right code is still refused, saying why, and
// the refusal is on the record. They keep their row.
func TestADisabledPlayerCannotSignIn(t *testing.T) {
	v, codes, db, log, _ := verify(t)
	ctx := context.Background()
	p := biztest.SeedPlayer(t, db, phone1, "Ali")
	if err := NewPlayerStore(db).SetDisabled(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	_ = codes.Put(ctx, phone1, model.HashCode("424242"), time.Minute)

	_, err := v.Do(ctx, phone1, "424242")
	var e *errors.Error
	if !stderrors.As(err, &e) || e.Status != http.StatusForbidden || e.Code != "player_disabled" {
		t.Fatalf("got %v, want 403 player_disabled", err)
	}
	var sessions int64
	db.Table("sessions").Count(&sessions)
	if sessions != 0 {
		t.Fatal("a session was opened for a disabled player")
	}
	if len(log.written) != 1 || log.written[0].Action != model.ActionRefused || log.written[0].Detail["reason"] != "disabled" {
		t.Fatalf("the log holds %+v", log.written)
	}
}

// A wrong guess says how many are left, and the fifth one takes the code away.
func TestWrongGuessesCountDownToNothing(t *testing.T) {
	v, codes, _, _, _ := verify(t)
	ctx := context.Background()
	_ = codes.Put(ctx, phone1, model.HashCode("424242"), time.Minute)

	for want := 4; want >= 0; want-- {
		_, err := v.Do(ctx, phone1, "000000")
		var e *errors.Error
		if !stderrors.As(err, &e) || e.Code != "wrong_code" || e.AttemptsLeft == nil || *e.AttemptsLeft != want {
			t.Fatalf("got %v, want wrong_code with %d left", err, want)
		}
	}
	if _, err := v.Do(ctx, phone1, "424242"); status(err) != http.StatusUnauthorized {
		t.Fatalf("the code survived five wrong guesses: %v", err)
	}
}

func status(err error) int {
	var e *errors.Error
	if stderrors.As(err, &e) {
		return e.Status
	}
	return 0
}
