package games

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// fields is a refusal's field problems; it fails on anything else.
func fields(t *testing.T, err error) map[string]string {
	t.Helper()
	var e *errors.Error
	if !stderrors.As(err, &e) || e.Code != "invalid_fields" {
		t.Fatalf("want invalid_fields, got %v", err)
	}
	return e.Fields
}

// actions is what the audit was told, in order.
func actions(entries []model.Entry) []string {
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Action)
	}
	return out
}

// dropCategories takes away the categories a test makes, before and after it:
// the table is seeded by the migration, so the database keeps it between
// tests (biztest's vocabulary).
func dropCategories(t *testing.T, db *gorm.DB, ids ...string) {
	t.Helper()
	drop := func() {
		db.Exec(`UPDATE games SET category = 'other' WHERE category IN ?`, ids)
		db.Exec(`UPDATE game_builds SET category = 'other' WHERE category IN ?`, ids)
		db.Exec(`DELETE FROM game_categories WHERE id IN ?`, ids)
	}
	drop()
	t.Cleanup(drop)
}

func ptr[T any](v T) *T { return &v }

// The migration makes the eight there were, with the app's words, other last.
func TestTheStoreStartsWithTheEightCategories(t *testing.T) {
	k := newStudioKit(t)
	rows, err := k.cat.Categories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, c := range rows {
		ids = append(ids, c.ID)
	}
	want := "speed brain words luck guess draw party other"
	if got := strings.Join(ids, " "); got != want {
		t.Fatalf("categories %q; want %q", got, want)
	}
	if rows[1].NameAR != "ذكاء" || rows[1].NameEN != "Brain" || rows[7].NameAR != "أخرى" {
		t.Fatalf("names %+v, %+v", rows[1], rows[7])
	}
}

// An admin makes a category, a game and a build can carry it, the store can
// be narrowed to it, it is renamed, and deleting it moves its games to other.
func TestACategoryIsMadeRenamedAndDeleted(t *testing.T) {
	k := newStudioKit(t)
	dropCategories(t, k.db, "cards")
	ctx := context.Background()
	reg := k.studio.reg
	const admin = "adm_aaaaaaaaaaaaaaaa"

	// Every problem at once, and nothing written.
	bad := fields(t, func() error {
		_, err := reg.CreateCategory(ctx, admin, CategoryInput{NameAR: ptr("  "), Position: ptr(-1)})
		return err
	}())
	if bad["id"] != "required" || bad["name_ar"] != "required" || bad["name_en"] != "required" || bad["position"] != "0_to_10000" {
		t.Fatalf("problems %v", bad)
	}
	bad = fields(t, func() error {
		_, err := reg.CreateCategory(ctx, admin, CategoryInput{
			ID: "Cards!", NameAR: ptr(strings.Repeat("و", 41)), NameEN: ptr("Cards"),
		})
		return err
	}())
	if bad["id"] != "invalid" || bad["name_ar"] != "too_long" || len(bad) != 2 {
		t.Fatalf("problems %v", bad)
	}
	if len(k.audit.Written()) != 0 {
		t.Fatalf("a refusal was audited: %v", actions(k.audit.Written()))
	}

	// Without a position it goes after every category but other.
	made, err := reg.CreateCategory(ctx, admin, CategoryInput{ID: "cards", NameAR: ptr(" ورق "), NameEN: ptr("Cards")})
	if err != nil {
		t.Fatal(err)
	}
	if made.NameAR != "ورق" || made.Position != 80 || made.Games != 0 {
		t.Fatalf("made %+v; want ورق at 80", made.Category)
	}
	if _, err := reg.CreateCategory(ctx, admin, CategoryInput{ID: "cards", NameAR: ptr("ورق"), NameEN: ptr("Cards")}); code(t, err) != "category_exists" {
		t.Fatalf("making it twice: %v", err)
	}
	rows, _ := k.cat.Categories(ctx)
	if rows[7].ID != "cards" || rows[8].ID != "other" {
		t.Fatalf("order %v %v; want cards before other", rows[7].ID, rows[8].ID)
	}

	// A game and its build carry it; the store narrows to it.
	sg := k.create(t)
	if _, err := reg.Listing(ctx, admin, dice, model.Listing{Category: ptr("cards")}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.studio.Edit(ctx, k.who, sg.Build.ID, BuildCopy{Category: ptr("cards")}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Listing(ctx, admin, dice, model.Listing{Category: ptr("nope")}); fields(t, err)["category"] != "unknown" {
		t.Fatalf("an unknown category on a game: %v", err)
	}
	if _, err := k.studio.Edit(ctx, k.who, sg.Build.ID, BuildCopy{Category: ptr("nope")}); fields(t, err)["category"] != "unknown" {
		t.Fatalf("an unknown category on a build: %v", err)
	}
	if _, err := k.cat.List(ctx, "plr_aaaaaaaaaaaaaaaa", "cards"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.cat.List(ctx, "plr_aaaaaaaaaaaaaaaa", "nope"); fields(t, err)["category"] != "unknown" {
		t.Fatalf("the store narrowed to an unknown category: %v", err)
	}
	listed, _ := reg.Categories(ctx)
	for _, c := range listed {
		if c.ID == "cards" && c.Games != 1 {
			t.Fatalf("cards carried by %d games; want 1", c.Games)
		}
	}

	// Renamed: only what is sent.
	if _, err := reg.EditCategory(ctx, admin, "nope", CategoryInput{NameEN: ptr("x")}); code(t, err) != "not_found" {
		t.Fatalf("editing a missing category: %v", err)
	}
	if _, err := reg.EditCategory(ctx, admin, "cards", CategoryInput{NameAR: ptr("")}); fields(t, err)["name_ar"] != "required" {
		t.Fatalf("emptying a name: %v", err)
	}
	edited, err := reg.EditCategory(ctx, admin, "cards", CategoryInput{NameEN: ptr("Card games"), Position: ptr(5)})
	if err != nil {
		t.Fatal(err)
	}
	if edited.NameAR != "ورق" || edited.NameEN != "Card games" || edited.Position != 5 || edited.Games != 1 {
		t.Fatalf("edited %+v", edited)
	}
	rows, _ = k.cat.Categories(ctx)
	if rows[0].ID != "cards" {
		t.Fatalf("first %s; want cards at 5", rows[0].ID)
	}

	// Other stays; deleting cards moves the game and its build to other.
	if _, err := reg.DeleteCategory(ctx, admin, "other"); code(t, err) != "category_protected" {
		t.Fatalf("deleting other: %v", err)
	}
	if _, err := reg.DeleteCategory(ctx, admin, "nope"); code(t, err) != "not_found" {
		t.Fatalf("deleting a missing category: %v", err)
	}
	moved, err := reg.DeleteCategory(ctx, admin, "cards")
	if err != nil || moved != 1 {
		t.Fatalf("deleted, %d moved, %v; want 1", moved, err)
	}
	if g := gameOf(t, k.db, dice); g.Category != "other" {
		t.Fatalf("the game is in %s; want other", g.Category)
	}
	if b := mustBuild(t, k.db, sg.Build.ID); b.Category != "other" {
		t.Fatalf("the build is in %s; want other", b.Category)
	}
	if ok, _ := knownCategory(ctx, k.db, "cards"); ok {
		t.Fatal("cards is still there")
	}

	got := []string{}
	for _, a := range actions(k.audit.Written()) {
		if strings.HasPrefix(a, "category.") {
			got = append(got, a)
		}
	}
	if strings.Join(got, " ") != "category.created category.edited category.deleted" {
		t.Fatalf("audited %v", got)
	}
}
