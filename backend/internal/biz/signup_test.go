package biz

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

func signup(t *testing.T) (*Signup, *gorm.DB, *biztest.Audit) {
	t.Helper()
	db := biztest.DB(t)
	log := &biztest.Audit{}
	minter := identity.NewMinter(biztest.Signer(t))
	return &Signup{
		Tx:      data.NewTx(db),
		Players: identity.NewPlayerStore(db),
		Issuer: &identity.Issuer{
			Sessions: identity.NewSessionStore(db), Tokens: identity.NewRefreshStore(db),
			Minter: minter, Whitelist: &biztest.Whitelist{},
			SessionTTL: time.Hour, RefreshTTL: 24 * time.Hour,
		},
		Audit: log,
	}, db, log
}

// fixDigits pins the four digits, in order, for one test.
func fixDigits(t *testing.T, seq ...string) {
	t.Helper()
	was := digits
	i := 0
	digits = func() string {
		d := seq[min(i, len(seq)-1)]
		i++
		return d
	}
	t.Cleanup(func() { digits = was })
}

// Signing up is a player and a session in one call. The handle is the latin
// of the nickname with four digits, so two people called Ali can both be Ali,
// and the sign-up is on the record.
func TestSignupMakesAPlayerWithAUniqueHandle(t *testing.T) {
	s, db, log := signup(t)
	ctx := context.Background()

	v, err := s.Do(ctx, SignupRequest{Phone: "+9647700000001", Nickname: " Ali Hasan ", Avatar: "bundled:3"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Scope != caller.ScopeSession || v.RefreshToken == "" || v.Player == nil {
		t.Fatalf("got %+v", v)
	}
	p := v.Player
	if p.Nickname != "Ali Hasan" || *p.Phone != "+9647700000001" || p.AvatarKey != "bundled:3" || p.Language != enum.Arabic {
		t.Fatalf("player is %+v", p)
	}
	if !regexp.MustCompile(`^alihasan_[0-9]{4}$`).MatchString(p.Handle) {
		t.Fatalf("handle is %q", p.Handle)
	}
	row, _ := s.Players.ByID(ctx, p.ID)
	if row == nil || row.Handle != p.Handle {
		t.Fatalf("not written: %+v", row)
	}
	w := log.Written()
	if len(w) != 1 || w[0].Action != model.ActionSignedUp || w[0].Actor != p.ID || w[0].Detail["handle"] != p.Handle {
		t.Fatalf("the log holds %+v", w)
	}

	again, err := s.Do(ctx, SignupRequest{Phone: "+9647700000002", Nickname: "Ali Hasan", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Player.Handle == p.Handle || again.Player.Language != enum.English {
		t.Fatalf("the second Ali is %+v", again.Player)
	}
	var sessions int64
	db.Table("sessions").Count(&sessions)
	if sessions != 2 {
		t.Fatalf("%d sessions, want 2", sessions)
	}
}

// A nickname with no latin in it is shown as it is; the handle is player_
// and four digits, nothing transliterated.
func TestANicknameWithNoLatinIsAPlayer(t *testing.T) {
	s, _, _ := signup(t)
	v, err := s.Do(context.Background(), SignupRequest{Phone: "+9647700000001", Nickname: "سجاد"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Player.Nickname != "سجاد" || !regexp.MustCompile(`^player_[0-9]{4}$`).MatchString(v.Player.Handle) {
		t.Fatalf("player is %+v", v.Player)
	}
}

// Four digits are ten thousand Alis, and the index decides the rare clash:
// the digits are drawn again, and after five clashes it is a failure rather
// than a loop.
func TestAHandleClashIsDrawnAgain(t *testing.T) {
	s, db, _ := signup(t)
	ctx := context.Background()
	if err := db.Exec(`UPDATE players SET handle = 'ali_1234' WHERE id = ?`,
		biztest.SeedPlayer(t, db, "+9647700000009", "x").ID).Error; err != nil {
		t.Fatal(err)
	}

	fixDigits(t, "1234", "1234", "5678")
	v, err := s.Do(ctx, SignupRequest{Phone: "+9647700000001", Nickname: "Ali"})
	if err != nil || v.Player.Handle != "ali_5678" {
		t.Fatalf("got %+v, %v", v, err)
	}

	fixDigits(t, "1234")
	_, err = s.Do(ctx, SignupRequest{Phone: "+9647700000002", Nickname: "Ali"})
	if e, ok := err.(*errors.Error); !ok || e.Status != http.StatusInternalServerError {
		t.Fatalf("five clashes got %v", err)
	}
	var players, sessions int64
	db.Table("players").Where("phone = ?", "+9647700000002").Count(&players)
	db.Table("sessions").Count(&sessions)
	if players != 0 || sessions != 1 {
		t.Fatalf("after the clashes: %d rows for the number, %d sessions; want none and the one from before", players, sessions)
	}
}

// A number signs up once. The second attempt is told so, whichever check
// catches it.
func TestAPhoneSignsUpOnce(t *testing.T) {
	s, db, _ := signup(t)
	biztest.SeedPlayer(t, db, "+9647700000001", "Ali")

	_, err := s.Do(context.Background(), SignupRequest{Phone: "+9647700000001", Nickname: "Ali"})
	if e, ok := err.(*errors.Error); !ok || e.Status != http.StatusConflict || e.Code != "phone_taken" {
		t.Fatalf("got %v, want 409 phone_taken", err)
	}
}

// The profile is checked before anything is written, every problem at once.
func TestTheProfileIsChecked(t *testing.T) {
	s, db, _ := signup(t)
	ctx := context.Background()

	for _, tc := range []struct {
		in    SignupRequest
		field string
		want  string
	}{
		{SignupRequest{Nickname: "   "}, "nickname", "required"},
		{SignupRequest{Nickname: "A"}, "nickname", "too_short"},
		{SignupRequest{Nickname: "abcdefghijklmnopqrstu"}, "nickname", "too_long"},
		{SignupRequest{Nickname: "Ali", Language: "fr"}, "language", "unknown"},
	} {
		tc.in.Phone = "+9647700000001"
		_, err := s.Do(ctx, tc.in)
		e, ok := err.(*errors.Error)
		if !ok || e.Code != "invalid_fields" || e.Fields[tc.field] != tc.want {
			t.Fatalf("%+v: got %v, want %s %s", tc.in, err, tc.field, tc.want)
		}
	}
	var players int64
	db.Table("players").Count(&players)
	if players != 0 {
		t.Fatal("a refused sign-up was written")
	}
}
