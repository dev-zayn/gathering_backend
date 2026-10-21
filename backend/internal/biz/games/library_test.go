package games

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

func testCatalogue(t *testing.T) (*Catalogue, *biztest.Whitelist, *biztest.Audit, *gorm.DB) {
	t.Helper()

	db := biztest.DB(t)
	biztest.SeedGame(t, db, xo, xoManifest())

	tap := xoManifest()
	tap.Name, tap.Entry = "سباق النقر", "https://games.yourstudio.example/tap/"
	tap.Room.Players.Max, tap.Room.Host, tap.Room.Pace = 8, enum.HostPeer, enum.PaceRealtime
	biztest.SeedGame(t, db, "fixture.tap", tap)

	wl := &biztest.Whitelist{}
	audit := &biztest.Audit{}
	revoker := &Revoker{Tokens: identity.NewGameTokenStore(db), Whitelist: wl}
	return NewCatalogue(db, revoker, audit), wl, audit, db
}

// The first open is the whole of installing: the row is written, and every
// open after it is a play counted and a time moved.
func TestTheFirstOpenAddsTheGameAndTheRestCountPlays(t *testing.T) {
	c, _, audit, db := testCatalogue(t)
	ctx := context.Background()

	first, err := c.Open(ctx, "ply_1", xo)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if first.Plays != 1 || first.LastPlayedAt == nil {
		t.Fatalf("after the first open: %+v", first)
	}
	if len(audit.Written()) != 1 || audit.Written()[0].Action != model.ActionLibraryAdded {
		t.Fatalf("entries = %+v", audit.Written())
	}

	again, err := c.Open(ctx, "ply_1", xo)
	if err != nil {
		t.Fatalf("open again: %v", err)
	}
	if again.Plays != 2 || !again.AddedAt.Equal(first.AddedAt) {
		t.Fatalf("after the second open: %+v", again)
	}
	row, _ := libraryRow(ctx, db, "ply_1", xo)
	if row == nil || row.Plays != 2 || row.LastPlayedAt == nil || row.LastPlayedAt.Before(*first.LastPlayedAt) {
		t.Fatalf("stored: %+v", row)
	}
	// Opening again is not a second line in the log: the shelf did not change.
	if len(audit.Written()) != 1 {
		t.Fatalf("entries = %+v", audit.Written())
	}

	// A game that is not published cannot be opened, and the answer does not
	// say whether it exists.
	if _, err := c.Open(ctx, "ply_1", "nobody.game"); err == nil || err.Error() != "not_found" {
		t.Fatalf("an unknown game opened: %v", err)
	}
	if err := db.Exec(`UPDATE games SET listing = ? WHERE id = ?`, enum.Unpublished, "fixture.tap").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := c.Open(ctx, "ply_1", "fixture.tap"); err == nil || err.Error() != "not_found" {
		t.Fatalf("an unpublished game opened: %v", err)
	}
}

// The store is every published game, the featured ones first, each saying
// whether this player has it.
func TestTheStoreSaysWhatThePlayerHas(t *testing.T) {
	c, _, _, db := testCatalogue(t)
	ctx := context.Background()
	if _, err := c.Open(ctx, "ply_1", "fixture.tap"); err != nil {
		t.Fatal(err)
	}

	rows, err := c.List(ctx, "ply_1", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 || rows[0].Game.ID != "fixture.tap" || rows[1].Game.ID != xo {
		t.Fatalf("rows = %+v", rows)
	}
	if !rows[0].InLibrary || rows[1].InLibrary {
		t.Fatalf("in_library = %v %v", rows[0].InLibrary, rows[1].InLibrary)
	}

	// Featured leads, whatever the id says.
	if err := db.Exec(`UPDATE games SET featured = true WHERE id = ?`, xo).Error; err != nil {
		t.Fatal(err)
	}
	rows, _ = c.List(ctx, "ply_1", "")
	if rows[0].Game.ID != xo {
		t.Fatalf("featured did not lead: %s", rows[0].Game.ID)
	}

	// Another player sees the same store and holds nothing.
	rows, _ = c.List(ctx, "ply_2", "")
	for _, r := range rows {
		if r.InLibrary {
			t.Fatalf("ply_2 holds %s", r.Game.ID)
		}
	}

	// A category narrows it; an unknown one is a field error, not an empty list.
	if err := db.Exec(`UPDATE games SET category = 'speed' WHERE id = ?`, "fixture.tap").Error; err != nil {
		t.Fatal(err)
	}
	rows, _ = c.List(ctx, "ply_1", "speed")
	if len(rows) != 1 || rows[0].Game.ID != "fixture.tap" {
		t.Fatalf("speed = %+v", rows)
	}
	if _, err := c.List(ctx, "ply_1", "sports"); err == nil {
		t.Fatal("an unknown category listed something")
	}

	d, err := c.Detail(ctx, "ply_1", "fixture.tap")
	if err != nil || d.Library == nil || d.Library.Plays != 1 {
		t.Fatalf("detail: %v %+v", err, d)
	}
	d, err = c.Detail(ctx, "ply_1", xo)
	if err != nil || d.Library != nil {
		t.Fatalf("detail of a game not held: %v %+v", err, d)
	}
}

// Removing is the revocation: the row goes, the refresh tokens go, and the
// whitelist is told so the access token stops now.
func TestRemovingAGameEndsItsTokens(t *testing.T) {
	c, wl, audit, db := testCatalogue(t)
	ctx := context.Background()
	if _, err := c.Open(ctx, "ply_1", xo); err != nil {
		t.Fatal(err)
	}
	tokens := identity.NewGameTokenStore(db)
	for _, p := range []string{"ply_1", "ply_2"} {
		err := tokens.Create(ctx, &model.GameToken{
			Hash: model.HashCredential("rft_" + p), GameID: xo, PlayerID: p, CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := c.Remove(ctx, "ply_1", xo); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if row, _ := libraryRow(ctx, db, "ply_1", xo); row != nil {
		t.Fatal("the row is still there")
	}
	if tok, _ := tokens.Get(ctx, model.HashCredential("rft_ply_1")); tok != nil {
		t.Fatal("the player's refresh token survived the remove")
	}
	if tok, _ := tokens.Get(ctx, model.HashCredential("rft_ply_2")); tok == nil {
		t.Fatal("another player's refresh token was taken with it")
	}
	if len(wl.RevokedGames) != 1 || wl.RevokedGames[0] != xo+":ply_1" {
		t.Fatalf("revoked %v", wl.RevokedGames)
	}
	if got := audit.Written()[1].Action; got != model.ActionLibraryRemoved {
		t.Fatalf("logged as %s", got)
	}

	// Gone is gone: removing again names the real reason.
	if err := c.Remove(ctx, "ply_1", xo); err == nil || err.Error() != "not_in_library" {
		t.Fatalf("removing twice gave %v", err)
	}
}

// Grant is the one gate: real, published, and on the shelf. A suspended game
// is on nobody's shelf as far as a token is concerned.
func TestGrantIsTheOneGate(t *testing.T) {
	c, _, _, db := testCatalogue(t)
	ctx := context.Background()

	grant, err := c.Grant(ctx, "ply_1", xo)
	if err != nil || grant.InLibrary {
		t.Fatalf("before opening: %v %+v", err, grant)
	}
	if _, err := c.Open(ctx, "ply_1", xo); err != nil {
		t.Fatal(err)
	}
	grant, _ = c.Grant(ctx, "ply_1", xo)
	if !grant.InLibrary || len(grant.Scopes) != 1 || grant.Scopes[0].Key != "identity:read" {
		t.Fatalf("after opening: %+v", grant)
	}

	if err := db.Exec(`UPDATE games SET listing = ? WHERE id = ?`, enum.Suspended, xo).Error; err != nil {
		t.Fatal(err)
	}
	grant, _ = c.Grant(ctx, "ply_1", xo)
	if grant.InLibrary {
		t.Fatal("a suspended game still counts as held")
	}
	if _, err := c.Grant(ctx, "ply_1", "nobody.game"); err == nil {
		t.Fatal("an unknown game was granted")
	}
}
