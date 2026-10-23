package inbox

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// handed is a Pusher that keeps what it is handed, and the title each row
// had for anybody outside the write when it was handed over: a row pushed
// before its commit reads as missing, or as its words before the write.
type handed struct {
	db *gorm.DB

	mu     sync.Mutex
	ids    []string
	titles map[string]string
}

func (h *handed) Push(ids ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range ids {
		var title []string
		_ = h.db.Raw(`SELECT title FROM notifications WHERE id = ?`, id).Scan(&title).Error
		h.ids = append(h.ids, id)
		if len(title) == 1 {
			h.titles[id] = title[0]
		}
	}
}

func (h *handed) got() ([]string, map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.ids), h.titles
}

// Every raise is handed to the pusher once its write is there for anybody
// to read: at once outside a transaction, after the commit inside one,
// never when it rolls back; a raise that updates a row in place is handed
// over again, as it now reads.
func TestARaiseIsPushedOnceCommitted(t *testing.T) {
	f := newFixture(t)
	h := &handed{db: f.db, titles: map[string]string{}}
	f.in.Pusher = h
	sara := f.player("Sara")

	alone := f.raise(Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "وحده"})

	tx := data.NewTx(f.db)
	var first *Raised
	err := tx.Do(f.ctx, func(ctx context.Context) error {
		var err error
		if first, err = f.in.Raise(ctx, Notice{PlayerID: sara.ID, Kind: enum.NoticeRoomInvite, GameID: xo,
			Title: "دعوة للعب", Key: "room_invite:room_1"}); err != nil {
			return err
		}
		if ids, _ := h.got(); len(ids) != 1 {
			t.Errorf("handed over inside the transaction: %v", ids)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = tx.Do(f.ctx, func(ctx context.Context) error {
		if _, err := f.in.Raise(ctx, Notice{PlayerID: sara.ID, Kind: enum.NoticeRoomInvite, GameID: xo,
			Title: "دعوة ثانية", Key: "room_invite:room_1"}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	refused := errors.New("refused")
	err = tx.Do(f.ctx, func(ctx context.Context) error {
		if _, err := f.in.Raise(ctx, Notice{PlayerID: sara.ID, Kind: enum.NoticeSystem, Title: "لا"}); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatal(err)
	}

	ids, titles := h.got()
	if !slices.Equal(ids, []string{alone.ID, first.ID, first.ID}) {
		t.Fatalf("handed over: %v", ids)
	}
	if titles[alone.ID] != "وحده" || titles[first.ID] != "دعوة ثانية" {
		t.Fatalf("as read when handed over: %v", titles)
	}
}

// An announcement hands over every row it wrote, after its commit; a dry run
// hands over nothing.
func TestAnAnnouncementIsPushedOnceCommitted(t *testing.T) {
	f := newFixture(t)
	h := &handed{db: f.db, titles: map[string]string{}}
	f.in.Pusher = h
	admin := biztest.SeedAdmin(t, f.db, "dev:sweep", "Sweep")
	a, b := f.player("Zayn"), f.player("Sara")
	audience := &AnnouncementAudience{Kind: AudiencePlayers, Players: []string{a.ID, b.ID}}

	if _, err := f.in.Announce(f.ctx, admin.ID, Announce{Audience: audience, Title: "تجربة", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	sent, err := f.in.Announce(f.ctx, admin.ID, Announce{Audience: audience, Title: "صيانة"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	if err := f.db.Raw(`SELECT id FROM notifications WHERE key = ? ORDER BY id`, "announcement:"+sent.ID).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	ids, titles := h.got()
	slices.Sort(ids)
	if len(rows) != 2 || !slices.Equal(ids, rows) || titles[rows[0]] != "صيانة" || titles[rows[1]] != "صيانة" {
		t.Fatalf("handed over %v (%v), written %v", ids, titles, rows)
	}
}
