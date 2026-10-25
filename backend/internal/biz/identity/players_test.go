package identity_test

import (
	"context"
	"testing"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

func players(t *testing.T) (*identity.PlayerStore, func(phone, nickname string) *model.Player) {
	t.Helper()
	db := biztest.DB(t)
	return identity.NewPlayerStore(db), func(phone, nickname string) *model.Player {
		return biztest.SeedPlayer(t, db, phone, nickname)
	}
}

// A player is found by id, by phone and by handle, and the handle is case
// blind both on the way in and when asked for.
func TestAPlayerIsFoundByIDPhoneAndHandle(t *testing.T) {
	store, _ := players(t)
	ctx := context.Background()
	phone := "+9647700000001"
	p := &model.Player{ID: "ply_1", Phone: &phone, Nickname: "Ali", Handle: "Ali_1234"}
	if err := store.Create(ctx, p); err != nil {
		t.Fatal(err)
	}

	byID, _ := store.ByID(ctx, "ply_1")
	byPhone, _ := store.ByPhone(ctx, phone)
	byHandle, _ := store.ByHandle(ctx, " ALI_1234 ")
	for name, got := range map[string]*model.Player{"id": byID, "phone": byPhone, "handle": byHandle} {
		if got == nil || got.ID != "ply_1" || got.Handle != "ali_1234" || got.Language != enum.Arabic {
			t.Fatalf("by %s: %+v", name, got)
		}
	}
	if taken, _ := store.HandleTaken(ctx, "ALI_1234"); !taken {
		t.Error("a held handle reads as free")
	}
	if taken, _ := store.HandleTaken(ctx, "ali_9999"); taken {
		t.Error("a free handle reads as held")
	}
	if nobody, err := store.ByPhone(ctx, "+9647700000009"); nobody != nil || err != nil {
		t.Fatalf("an unknown number: %+v, %v", nobody, err)
	}
}

// Search is a prefix over handles, case blind, shortest first, and a
// disabled player is not in it. The underscore is a letter here, not a
// wildcard: ali_ does not find alix.
func TestSearchIsAPrefixOverHandlesAndSkipsTheDisabled(t *testing.T) {
	store, seed := players(t)
	ctx := context.Background()
	seed("+9647700000001", "Ali")
	seed("+9647700000002", "Alix")
	off := seed("+9647700000003", "Ali")
	seed("+9647700000004", "Bob")
	if err := store.SetDisabled(ctx, off.ID, true); err != nil {
		t.Fatal(err)
	}

	found, err := store.Search(ctx, "ALI", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Handle[:4] != "ali_" || found[1].Handle[:4] != "alix" {
		t.Fatalf("found %+v", handles(found))
	}
	found, _ = store.Search(ctx, "ali_", 20)
	if len(found) != 1 || found[0].Handle[:4] != "ali_" {
		t.Fatalf("ali_ found %v", handles(found))
	}
	if found, _ = store.Search(ctx, "a", 1); len(found) != 1 {
		t.Fatalf("the limit was not kept: %v", handles(found))
	}
	if found, _ = store.Search(ctx, "  ", 20); len(found) != 0 {
		t.Fatalf("nothing typed found %v", handles(found))
	}
}

// The console's list is everybody, the newest first, with nothing typed, and
// the handles that start with what was typed otherwise. The switched off are
// in both: the console is where they are switched back on.
func TestTheConsoleListIsNewestFirstAndKeepsTheSwitchedOff(t *testing.T) {
	db := biztest.DB(t)
	store := identity.NewPlayerStore(db)
	ctx := context.Background()
	oldest := biztest.SeedPlayer(t, db, "+9647700000001", "Ali")
	middle := biztest.SeedPlayer(t, db, "+9647700000002", "Bob")
	newest := biztest.SeedPlayer(t, db, "+9647700000003", "Alix")
	for i, p := range []*model.Player{oldest, middle, newest} {
		if err := db.Exec(`UPDATE players SET created_at = now() - make_interval(hours => ?) WHERE id = ?`, 3-i, p.ID).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetDisabled(ctx, middle.ID, true); err != nil {
		t.Fatal(err)
	}

	all, err := store.List(ctx, "  ", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != newest.ID || all[1].ID != middle.ID || all[2].ID != oldest.ID {
		t.Fatalf("nothing typed listed %v", handles(all))
	}
	if !all[1].Disabled {
		t.Fatal("the switched-off player lost their switch in the list")
	}
	if found, _ := store.List(ctx, "@", 50); len(found) != 0 {
		t.Fatalf("@ listed %v", handles(found))
	}
	found, _ := store.List(ctx, "ALI", 50)
	if len(found) != 2 || found[0].ID != oldest.ID || found[1].ID != newest.ID {
		t.Fatalf("ali listed %v", handles(found))
	}
	if found, _ = store.List(ctx, "bo", 50); len(found) != 1 || found[0].ID != middle.ID {
		t.Fatalf("a switched-off player is not found by handle: %v", handles(found))
	}
	if found, _ = store.List(ctx, "", 2); len(found) != 2 {
		t.Fatalf("the limit was not kept: %v", handles(found))
	}
}

func handles(ps []model.Player) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Handle)
	}
	return out
}

// What a player changes about themselves lands; a sighting lands; the admin's
// switch stops the next sign-in and nothing else about them.
func TestUpdateTouchAndTheSwitch(t *testing.T) {
	store, seed := players(t)
	ctx := context.Background()
	p := seed("+9647700000001", "Ali")

	p.Nickname, p.Bio, p.Language, p.Handle = "Ali H.", "plays late", enum.English, "AliH_2"
	if err := store.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.Touch(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDisabled(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}

	row, _ := store.ByID(ctx, p.ID)
	if row.Nickname != "Ali H." || row.Bio != "plays late" || row.Language != enum.English || row.Handle != "alih_2" {
		t.Fatalf("after update: %+v", row)
	}
	if row.LastSeenAt == nil || row.CanSignIn() || *row.Phone != "+9647700000001" {
		t.Fatalf("after touch and switch: %+v", row)
	}
}

// A phone and a handle each belong to one player; the database says so, and
// the store can tell which one was the problem.
func TestAPhoneAndAHandleBelongToOnePlayer(t *testing.T) {
	store, seed := players(t)
	ctx := context.Background()
	held := seed("+9647700000001", "Ali")

	phone := "+9647700000001"
	err := store.Create(ctx, &model.Player{ID: "ply_2", Phone: &phone, Nickname: "Ali", Handle: "ali_free"})
	if !identity.IsPhoneConflict(err) || identity.IsHandleConflict(err) {
		t.Fatalf("the same number: %v", err)
	}
	other := "+9647700000002"
	err = store.Create(ctx, &model.Player{ID: "ply_3", Phone: &other, Nickname: "Ali", Handle: held.Handle})
	if !identity.IsHandleConflict(err) || identity.IsPhoneConflict(err) {
		t.Fatalf("the same handle: %v", err)
	}
	if identity.IsPhoneConflict(nil) || identity.IsHandleConflict(nil) {
		t.Fatal("no error read as a conflict")
	}
}
