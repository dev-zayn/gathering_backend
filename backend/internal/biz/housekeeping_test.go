package biz

import (
	"context"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// The clock's round forgets a notification 90 days after it was last
// raised, and keeps everything younger.
func TestTheSweepForgetsOldNotifications(t *testing.T) {
	db := biztest.DB(t)
	ctx := context.Background()
	sara := biztest.SeedPlayer(t, db, "", "Sara")
	in := inbox.New(db)

	old, err := in.Raise(ctx, inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "قديم"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE notifications SET created_at = ? WHERE id = ?`, time.Now().Add(-91*24*time.Hour), old.ID).Error; err != nil {
		t.Fatal(err)
	}
	kept, err := in.Raise(ctx, inbox.Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "جديد"})
	if err != nil {
		t.Fatal(err)
	}

	m := &Maintenance{Notifications: in}
	swept, err := m.Sweep(ctx)
	if err != nil || swept.Notifications != 1 || !swept.Any() {
		t.Fatalf("the round: %+v %v", swept, err)
	}
	var left []string
	db.Model(&model.Notification{}).Pluck("id", &left)
	if len(left) != 1 || left[0] != kept.ID {
		t.Fatalf("left %v, want %s", left, kept.ID)
	}
	if swept, _ := m.Sweep(ctx); swept.Any() {
		t.Fatalf("a second round removed %+v", swept)
	}
}
