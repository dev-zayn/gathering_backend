package friends

import (
	"testing"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

func TestTheBlockedListNamesThemNewestFirst(t *testing.T) {
	f := newFixture(t)
	zayn, sara, ali, gone := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Gone")
	for _, p := range []*model.Player{sara, gone, ali} {
		if err := f.s.Block(f.ctx, zayn.ID, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Sara blocking Zayn is hers, not his to list.
	if err := f.s.Block(f.ctx, sara.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	// Spread the blocks over time: the list is newest first.
	for i, p := range []*model.Player{sara, gone, ali} {
		err := f.db.Exec(`UPDATE blocks SET created_at = now() - make_interval(mins => ?) WHERE blocker_id = ? AND blocked_id = ?`,
			10-i, zayn.ID, p.ID).Error
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, gone.ID).Error; err != nil {
		t.Fatal(err)
	}

	got, err := f.s.BlockedPlayers(f.ctx, zayn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != ali.ID || got[1].ID != sara.ID {
		t.Fatalf("want Ali then Sara, the switched off left out: %+v", got)
	}
	if got[0].Nickname != "Ali" || got[0].Handle != ali.Handle || got[0].BlockedAt == "" {
		t.Fatalf("a row without its card or its time: %+v", got[0])
	}
	// The ids the chat reads still carry everybody blocked.
	ids, err := f.s.Blocked(f.ctx, zayn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("the chat's ids lost someone: %v", ids)
	}

	// Unblocking takes them off the list.
	if err := f.s.Unblock(f.ctx, zayn.ID, ali.ID); err != nil {
		t.Fatal(err)
	}
	got, err = f.s.BlockedPlayers(f.ctx, zayn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != sara.ID {
		t.Fatalf("Ali is still listed: %+v", got)
	}
	none, err := f.s.BlockedPlayers(f.ctx, ali.ID)
	if err != nil || len(none) != 0 {
		t.Fatalf("nobody blocked by Ali: %+v %v", none, err)
	}
}

func TestTheConsoleSeesBlocksBothWaysAndLiftsOne(t *testing.T) {
	f := newFixture(t)
	log := &biztest.Audit{}
	f.s.Audit = log
	zayn, sara, ali, gone := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Gone")
	for _, pair := range [][2]*model.Player{{zayn, sara}, {zayn, gone}, {ali, zayn}} {
		if err := f.s.Block(f.ctx, pair[0].ID, pair[1].ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, gone.ID).Error; err != nil {
		t.Fatal(err)
	}

	blocked, by, err := f.s.Blocks(f.ctx, zayn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 2 || len(by) != 1 || by[0].ID != ali.ID {
		t.Fatalf("want two blocked and Ali blocking: %+v / %+v", blocked, by)
	}
	marked := 0
	for _, b := range blocked {
		if b.Disabled {
			marked++
			if b.ID != gone.ID {
				t.Fatalf("the wrong one marked switched off: %+v", b)
			}
		}
		if b.BlockedAt == "" {
			t.Fatalf("a row without its time: %+v", b)
		}
	}
	if marked != 1 {
		t.Fatalf("the switched-off player is not in the console's list, marked: %+v", blocked)
	}

	wantCode(t, "the blocks of nobody", errOf(f.s.Blocks(f.ctx, "ply_nobodyxxxxxxxxxx")), 404, "not_found")
	wantCode(t, "lifting for nobody", f.s.Lift(f.ctx, "adm_1", "ply_nobodyxxxxxxxxxx", sara.ID), 404, "not_found")

	// Lifting Zayn's block of Sara, and of the switched-off player.
	if err := f.s.Lift(f.ctx, "adm_1", zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Lift(f.ctx, "adm_1", zayn.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	// A block that is not there: not refused, nothing written.
	if err := f.s.Lift(f.ctx, "adm_1", zayn.ID, sara.ID); err != nil {
		t.Fatal(err)
	}
	blocked, _, err = f.s.Blocks(f.ctx, zayn.ID)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("blocks left after lifting both: %+v %v", blocked, err)
	}
	lines := log.Written()
	if len(lines) != 2 {
		t.Fatalf("want one line per block that went: %+v", lines)
	}
	l := lines[0]
	if l.Action != model.ActionBlockLifted || l.Actor != "adm_1" || l.Subject != zayn.ID || l.Detail["blocked"] != sara.ID {
		t.Fatalf("the line: %+v", l)
	}

	// The block against Zayn, lifted the other way round.
	if err := f.s.Lift(f.ctx, "adm_1", ali.ID, zayn.ID); err != nil {
		t.Fatal(err)
	}
	if _, by, _ = f.s.Blocks(f.ctx, zayn.ID); len(by) != 0 {
		t.Fatalf("Ali's block is still there: %+v", by)
	}

	// A deleted account's blocks are not the console's to read.
	if err := f.db.Exec(`UPDATE players SET deleted_at = now() WHERE id = ?`, sara.ID).Error; err != nil {
		t.Fatal(err)
	}
	wantCode(t, "a deleted account's blocks", errOf(f.s.Blocks(f.ctx, sara.ID)), 404, "not_found")
}

// errOf keeps the error of a call that answers two lists too.
func errOf(_, _ []BlockRow, err error) error { return err }
