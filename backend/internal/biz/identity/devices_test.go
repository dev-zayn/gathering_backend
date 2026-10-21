package identity_test

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// A Firebase token names the app install, so it follows the install from one
// session to the next; a session holds one phone; and a phone goes with its
// session, by Forget or by the sweep.
func TestATokenFollowsTheInstallAndGoesWithItsSession(t *testing.T) {
	db := biztest.DB(t)
	ctx := context.Background()
	p := biztest.SeedPlayer(t, db, "+9647700000001", "Ali")
	sessions := identity.NewSessionStore(db)
	for _, id := range []string{"ses_1", "ses_2"} {
		if err := sessions.Create(ctx, &model.Session{ID: id, PlayerID: p.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	devices := identity.NewDevices(db)

	if _, err := devices.Register(ctx, "ses_1", p.ID, enum.PlatformIOS, "tok-a"); err != nil {
		t.Fatal(err)
	}
	moved, err := devices.Register(ctx, "ses_2", p.ID, enum.PlatformAndroid, "tok-a")
	if err != nil {
		t.Fatal(err)
	}
	if rows := all(t, db); len(rows) != 1 || rows[0].ID != moved.ID || rows[0].SessionID != "ses_2" {
		t.Fatalf("after moving the install: %+v", rows)
	}

	if _, err := devices.Register(ctx, "ses_2", p.ID, enum.PlatformAndroid, "tok-b"); err != nil {
		t.Fatal(err)
	}
	if rows := all(t, db); len(rows) != 1 || rows[0].FCMToken != "tok-b" {
		t.Fatalf("a session holding two phones: %+v", rows)
	}

	if err := devices.Forget(ctx, "ses_2"); err != nil {
		t.Fatal(err)
	}
	if rows := all(t, db); len(rows) != 0 {
		t.Fatalf("forgotten and still here: %+v", rows)
	}

	if _, err := devices.Register(ctx, "ses_1", p.ID, enum.PlatformIOS, "tok-c"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Revoke(ctx, "ses_1"); err != nil {
		t.Fatal(err)
	}
	if n, err := devices.Sweep(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("swept %d, %v; want 1", n, err)
	}
}

func all(t *testing.T, db *gorm.DB) []model.Device {
	t.Helper()
	var rows []model.Device
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// What arrives is checked before it reaches the table.
func TestARegistrationIsChecked(t *testing.T) {
	devices := identity.NewDevices(biztest.DB(t))
	for _, tc := range []struct {
		platform enum.Platform
		token    string
		field    string
	}{
		{enum.PlatformIOS, "   ", "token"},
		{"windows", "tok", "platform"},
	} {
		_, err := devices.Register(context.Background(), "ses_1", "ply_1", tc.platform, tc.token)
		if e, ok := err.(*errors.Error); !ok || e.Code != "invalid_fields" || e.Fields[tc.field] == "" {
			t.Fatalf("%s: got %v", tc.field, err)
		}
	}
}
