package identity_test

import (
	"context"
	stderrors "errors"
	"sync"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// sessionList remembers which sessions were ended.
type sessionList struct {
	mu    sync.Mutex
	ended []string
}

func (s *sessionList) Allow(context.Context, string, string, time.Duration) error { return nil }
func (s *sessionList) Valid(context.Context, string) (bool, error)                { return true, nil }
func (s *sessionList) Revoke(context.Context, string) error                       { return nil }
func (s *sessionList) Touch(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (s *sessionList) RevokeSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = append(s.ended, id)
	return nil
}
func (s *sessionList) AllowGame(context.Context, string, string, string, time.Duration) error {
	return nil
}
func (s *sessionList) RevokeGame(context.Context, string, string) error { return nil }

type openLimiter struct{}

func (openLimiter) Allow(context.Context, string, int, time.Duration) (bool, int, error) {
	return true, 0, nil
}

func publishersFor(t *testing.T) (*identity.Publishers, *sessionList) {
	t.Helper()
	list := &sessionList{}
	return &identity.Publishers{
		DB: biztest.DB(t), Minter: identity.NewMinter(biztest.Signer(t)), Whitelist: list,
		Limiter: openLimiter{}, TTL: time.Hour, Idle: 30 * time.Minute,
	}, list
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *errors.Error
	if !stderrors.As(err, &e) {
		t.Fatalf("not one of our errors: %v", err)
	}
	return e.Code
}

// An account starts with no password: its one-time link chooses one, works
// once, and the sign-in that follows tells nobody which half was wrong.
func TestAPublisherChoosesItsPasswordOnce(t *testing.T) {
	p, _ := publishersFor(t)
	ctx := context.Background()

	row, setup, err := p.Create(ctx, "adm_1", "Dice Studio", " Team@Studio.Example ")
	if err != nil {
		t.Fatal(err)
	}
	if row.Email != "team@studio.example" || row.Status() != enum.PublisherInvited {
		t.Fatalf("made %+v; want the address lower-cased, invited", row)
	}
	if _, _, err := p.Create(ctx, "adm_1", "Twin", "team@studio.example"); codeOf(t, err) != "email_taken" {
		t.Fatalf("the same address twice: %v", err)
	}
	if _, err := p.SignIn(ctx, "team@studio.example", "anything long"); codeOf(t, err) != "wrong_password" {
		t.Fatalf("signing in with no password yet: %v", err)
	}
	if _, err := p.UseSetup(ctx, setup.Token, "short"); codeOf(t, err) != "invalid_fields" {
		t.Fatalf("a short password: %v", err)
	}

	opened, err := p.UseSetup(ctx, setup.Token, "a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Token == "" || opened.Publisher.Status() != enum.PublisherActive {
		t.Fatalf("the link opened %+v", opened)
	}
	if _, err := p.UseSetup(ctx, setup.Token, "another long password"); codeOf(t, err) != "invalid_setup" {
		t.Fatalf("the link a second time: %v", err)
	}

	if _, err := p.SignIn(ctx, "TEAM@studio.example", "a long enough password"); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"team@studio.example", "nobody@studio.example"} {
		if _, err := p.SignIn(ctx, email, "the wrong password"); codeOf(t, err) != "wrong_password" {
			t.Fatalf("%s with a wrong password: %v", email, err)
		}
	}
}

// Switching an account off ends every session at once and keeps it out; a
// new link does not change the password until it is used.
func TestSwitchingAPublisherOff(t *testing.T) {
	p, list := publishersFor(t)
	ctx := context.Background()
	row, setup, err := p.Create(ctx, "adm_1", "Dice Studio", "team@studio.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UseSetup(ctx, setup.Token, "a long enough password"); err != nil {
		t.Fatal(err)
	}

	if _, err := p.NewSetup(ctx, "adm_1", row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SignIn(ctx, "team@studio.example", "a long enough password"); err != nil {
		t.Fatalf("a waiting link changed the password: %v", err)
	}

	if _, err := p.Disable(ctx, "adm_1", row.ID); err != nil {
		t.Fatal(err)
	}
	if len(list.ended) != 1 || list.ended[0] != row.ID {
		t.Fatalf("sessions ended %v; want the account's", list.ended)
	}
	if _, err := p.SignIn(ctx, "team@studio.example", "a long enough password"); codeOf(t, err) != "publisher_disabled" {
		t.Fatalf("a switched-off account signing in: %v", err)
	}
	if _, err := p.Me(ctx, row.ID); codeOf(t, err) != "publisher_disabled" {
		t.Fatalf("a switched-off account's own record: %v", err)
	}
	if _, err := p.Enable(ctx, "adm_1", row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SignIn(ctx, "team@studio.example", "a long enough password"); err != nil {
		t.Fatalf("back on: %v", err)
	}
}
