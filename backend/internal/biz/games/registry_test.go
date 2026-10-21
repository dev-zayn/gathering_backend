package games

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// ── the fixture ──────────────────────────────────────────────────────────────

const xo = "fixture.xo"

// xoManifest is the game as its studio publishes it: two players, in turns,
// the host writing the board.
func xoManifest() model.Manifest {
	m := model.Manifest{
		Name: "إكس أو", Version: "1.4.0", Mode: enum.ModeEmbedded,
		Entry:  "https://games.yourstudio.example/xo/",
		Scopes: []string{"identity:read"},
	}
	m.Publisher.Tier = enum.TierPartner
	m.Publisher.Name = "استوديو التجربة"
	m.Room.Players.Min, m.Room.Players.Max = 2, 2
	m.Room.Host, m.Room.Pace = enum.HostAuthoritative, enum.PaceTurn
	return m
}

func testReread(t *testing.T) (*Registry, *biztest.Fetcher, *biztest.Audit, *gorm.DB) {
	t.Helper()

	db := biztest.DB(t)
	biztest.SeedGame(t, db, xo, xoManifest())
	stale(t, db, xo)

	// One player has it, so a manifest that widens has somebody to reach.
	biztest.SeedLibrary(t, db, "ply_1", xo)

	fetcher := biztest.NewFetcher()
	audit := &biztest.Audit{}

	reg := NewRegistry(db, fetcher, audit, nil)
	reg.MaxAge = 24 * time.Hour
	reg.Batch = 20
	return reg, fetcher, audit, db
}

// stale is a game the clock is due to re-read: one whose document was never
// read, which the seed's fresh stamp would otherwise hide.
func stale(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	if err := db.Exec(`UPDATE games SET fetched_at = NULL WHERE id = ?`, id).Error; err != nil {
		t.Fatal(err)
	}
}

// gameOf reads a row back the way the registry would, so a test asserts on
// what was actually written rather than on what a fake remembered.
func gameOf(t *testing.T, db *gorm.DB, id string) *model.Game {
	t.Helper()

	g, err := anyGame(context.Background(), db, id)
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		t.Fatalf("game %q is not there", id)
	}
	return g
}

// wider is the same game asking for a scope it did not have, at a new version.
func wider() *model.Manifest {
	m := xoManifest()
	m.ID = xo
	m.Version = "1.5.0"
	m.Scopes = []string{"identity:read", "friends:read"}
	return &m
}

// same is the document as it stands: nothing to hold, nothing to say.
func same() *model.Manifest {
	m := xoManifest()
	m.ID = xo
	return &m
}

// ── the tests ────────────────────────────────────────────────────────────────

// Nobody is asked at install, so a published game asking for more waits for
// the admin who is: the document is held, the game and every grant stay as
// they were, and accepting it is what moves them.
func TestAWiderRereadIsHeldUntilAccepted(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = wider()

	done, err := uc.Reread(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if done.Read != 1 || done.Changed != 1 || done.Failed != 0 {
		t.Fatalf("counts = %+v", done)
	}

	// The game row did not move; the document is parked beside it.
	g := gameOf(t, db, xo)
	if g.HeldAt == nil {
		t.Fatal("nothing was held")
	}
	if g.Version != "1.4.0" || len(g.Scopes) != 1 {
		t.Fatalf("the game moved to %s %v", g.Version, g.Scopes)
	}
	grant, err := NewCatalogue(db, nil, nil).Grant(context.Background(), "ply_1", xo)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if len(grant.Scopes) != 1 {
		t.Fatalf("the live grant moved to %v", grant.Scopes)
	}
	if held, _ := audit.Written()[0].Detail["held"].([]string); len(held) != 1 || held[0] != "friends:read" {
		t.Fatalf("the log says %v", audit.Written()[0].Detail)
	}

	// Accepting is the approval.
	accepted, err := uc.Accept(context.Background(), "adm_1", xo)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if accepted.Version != "1.5.0" || len(accepted.Scopes) != 2 || accepted.HeldAt != nil {
		t.Fatalf("after accept: %s %v held=%v", accepted.Version, accepted.Scopes, accepted.HeldAt)
	}
	grant, _ = NewCatalogue(db, nil, nil).Grant(context.Background(), "ply_1", xo)
	if !grant.InLibrary || len(grant.Scopes) != 2 {
		t.Fatalf("the live grant is %v", grant.Scopes)
	}
	if got := audit.Written()[1].Action; got != model.ActionAccepted {
		t.Fatalf("logged as %s", got)
	}
	if _, err := uc.Accept(context.Background(), "adm_1", xo); err == nil {
		t.Fatal("accepting twice must be refused")
	}
}

// The room block is what every player's room is made under, and the
// permissions are what their phone is asked for. A published game changing
// either waits the same way a wider scope list does.
func TestARoomOrPermissionChangeIsHeldToo(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(m *model.Manifest)
		want string
	}{
		{"more players", func(m *model.Manifest) { m.Room.Players.Max = 4 }, "room"},
		{"a peer host", func(m *model.Manifest) { m.Room.Host = enum.HostPeer }, "room"},
		{"the microphone", func(m *model.Manifest) { m.Permissions = []string{"microphone"} }, "permissions"},
	} {
		t.Run(c.name, func(t *testing.T) {
			uc, fetcher, audit, db := testReread(t)
			doc := same()
			c.edit(doc)
			fetcher.Docs[gameOf(t, db, xo).ManifestURL] = doc

			done, err := uc.Reread(context.Background())
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if done.Changed != 1 {
				t.Fatalf("counts = %+v", done)
			}
			// The row holds the seed's room, with the default the seed filled in.
			before := xoManifest().Room
			before.State = enum.StateBlob
			g := gameOf(t, db, xo)
			if g.HeldAt == nil || g.Room != before || len(g.Permissions) != 0 {
				t.Fatalf("the row moved: held=%v room=%+v permissions=%v", g.HeldAt, g.Room, g.Permissions)
			}
			if got, _ := audit.Written()[0].Detail["changed"].([]string); len(got) != 1 || got[0] != c.want {
				t.Fatalf("the log says %v", audit.Written()[0].Detail)
			}

			accepted, err := uc.Accept(context.Background(), "adm_1", xo)
			if err != nil {
				t.Fatalf("accept: %v", err)
			}
			want := doc.Room
			want.State = enum.StateBlob
			if accepted.Room != want || !sameSet(accepted.Permissions, doc.Permissions) {
				t.Fatalf("after accept: room=%+v permissions=%v", accepted.Room, accepted.Permissions)
			}
		})
	}
}

// An unpublished game has nobody to reach, so its document is saved as read.
func TestAnUnpublishedGameWidensFreely(t *testing.T) {
	uc, fetcher, _, db := testReread(t)
	if err := db.Exec(`UPDATE games SET listing = ? WHERE id = ?`, enum.Unpublished, xo).Error; err != nil {
		t.Fatal(err)
	}
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = wider()

	if _, err := uc.Reread(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	g := gameOf(t, db, xo)
	if g.Version != "1.5.0" || len(g.Scopes) != 2 || g.HeldAt != nil {
		t.Fatalf("game = %s %v held=%v", g.Version, g.Scopes, g.HeldAt)
	}
}

// A suspended game keeps its shelves. If its document could widen freely
// while suspended, publishing it again would hand every shelf the new scopes
// with nobody having accepted them.
func TestASuspendedGamesWiderRereadIsHeld(t *testing.T) {
	uc, fetcher, _, db := testReread(t)
	if err := db.Exec(`UPDATE games SET listing = ? WHERE id = ?`, enum.Suspended, xo).Error; err != nil {
		t.Fatal(err)
	}
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = wider()

	if _, err := uc.Reread(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	g := gameOf(t, db, xo)
	if g.Version != "1.4.0" || len(g.Scopes) != 1 || g.HeldAt == nil {
		t.Fatalf("game = %s %v held=%v", g.Version, g.Scopes, g.HeldAt)
	}

	if _, err := uc.Publish(context.Background(), "adm_1", xo); err != nil {
		t.Fatalf("publish: %v", err)
	}
	grant, err := NewCatalogue(db, nil, nil).Grant(context.Background(), "ply_1", xo)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if !grant.InLibrary || len(grant.Scopes) != 1 {
		t.Fatalf("after publish the live grant is %v", grant.Scopes)
	}
}

// The log is read to answer "who did this". Nobody did.
func TestARereadIsRecordedAgainstTheWorker(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	bumped := same()
	bumped.Version = "1.5.0" // the version moved, nothing else did
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = bumped

	if _, err := uc.Reread(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}

	if len(audit.Written()) != 1 {
		t.Fatalf("wrote %d entries", len(audit.Written()))
	}
	e := audit.Written()[0]
	if e.Actor != model.ActorWorker {
		t.Errorf("actor = %q", e.Actor)
	}
	if e.Action != model.ActionRefreshed || e.Subject != xo {
		t.Errorf("entry = %+v", e)
	}
	if e.Detail["from"] != "1.4.0" || e.Detail["to"] != "1.5.0" {
		t.Errorf("detail = %v", e.Detail)
	}
}

// A studio's server being down is their problem for a day, not ours forever.
// The row is left as it was and the next round is the retry.
func TestAnUnreachableManifestLeavesTheRowAlone(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	fetcher.Errs[gameOf(t, db, xo).ManifestURL] = errors.New("dial tcp: connection refused")

	done, err := uc.Reread(context.Background())
	if err != nil {
		t.Fatalf("a failing fetch failed the round: %v", err)
	}
	if done.Failed != 1 || done.Read != 0 {
		t.Fatalf("counts = %+v", done)
	}
	if got := gameOf(t, db, xo).Version; got != "1.4.0" {
		t.Errorf("the row moved to %s on a failed read", got)
	}
	if len(audit.Written()) != 0 {
		t.Errorf("a failed read wrote %d entries", len(audit.Written()))
	}
}

// A document that has not changed is read, and then nothing is said about it.
// A daily job writing a line per game per day buries the days something
// happened.
func TestAnUnchangedManifestSaysNothing(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = same()

	done, err := uc.Reread(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if done.Read != 1 || done.Changed != 0 {
		t.Fatalf("counts = %+v", done)
	}
	if len(audit.Written()) != 0 {
		t.Fatalf("a re-read that changed nothing wrote %d entries", len(audit.Written()))
	}
}

// A person pressing the button is a different matter: they asked, so the log
// says what came back even when it was the same document.
func TestAskingForARefreshIsAlwaysRecorded(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = same()

	if _, err := uc.Refresh(context.Background(), "adm_1", xo); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(audit.Written()) != 1 || audit.Written()[0].Actor != "adm_1" {
		t.Fatalf("entries = %+v", audit.Written())
	}
}

// The icon and the screenshots are files of ours: put in the bucket under the
// game's prefix, named in the row, and linked on the way out.
func TestPicturesAreFilesUnderTheGamesPrefix(t *testing.T) {
	uc, _, audit, _ := testReread(t)
	store := biztest.NewFiles()
	uc.Files = store
	ctx := context.Background()
	png := Picture{ContentType: "image/png", Data: []byte("png")}

	g, err := uc.SetIcon(ctx, "adm_1", xo, png)
	if err != nil {
		t.Fatalf("icon: %v", err)
	}
	if g.IconKey != "games/fixture.xo/icon.png" || store.TypeOf(g.IconKey) != "image/png" {
		t.Fatalf("icon key = %q, type = %q", g.IconKey, store.TypeOf(g.IconKey))
	}
	if IconURL(ctx, store, g) != "https://signed.test/games/fixture.xo/icon.png" {
		t.Fatalf("icon url = %q", IconURL(ctx, store, g))
	}
	if store.Signed[g.IconKey] != PictureTTL {
		t.Fatalf("the link lives %v", store.Signed[g.IconKey])
	}

	g, err = uc.AddScreenshot(ctx, "adm_1", xo, png)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if len(g.Screenshots) != 1 || !strings.HasPrefix(g.Screenshots[0], "games/fixture.xo/screens/") {
		t.Fatalf("screenshots = %v", g.Screenshots)
	}
	shots := ScreenshotURLs(ctx, store, g)
	if len(shots) != 1 || shots[0].ID == "" {
		t.Fatalf("links = %+v", shots)
	}

	g, err = uc.RemoveScreenshot(ctx, "adm_1", xo, shots[0].ID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(g.Screenshots) != 0 || len(store.Deleted) != 1 {
		t.Fatalf("after remove: %v, deleted %v", g.Screenshots, store.Deleted)
	}
	if len(audit.Written()) != 3 {
		t.Fatalf("entries = %+v", audit.Written())
	}
}

// Not a picture, or too big, is refused before the bucket is touched; and the
// page holds six at most.
func TestAPictureMustBeAPicture(t *testing.T) {
	uc, _, _, _ := testReread(t)
	store := biztest.NewFiles()
	uc.Files = store
	ctx := context.Background()

	if _, err := uc.SetIcon(ctx, "adm_1", xo, Picture{ContentType: "image/svg+xml", Data: []byte("<svg/>")}); err == nil {
		t.Fatal("svg was accepted")
	}
	if _, err := uc.SetIcon(ctx, "adm_1", xo, Picture{ContentType: "image/png", Data: make([]byte, MaxPicture+1)}); err == nil {
		t.Fatal("a picture over the cap was accepted")
	}
	if len(store.Stored()) != 0 {
		t.Fatalf("put = %v", store.Stored())
	}

	png := Picture{ContentType: "image/jpeg", Data: []byte("jpg")}
	for i := 0; i < MaxScreenshots; i++ {
		if _, err := uc.AddScreenshot(ctx, "adm_1", xo, png); err != nil {
			t.Fatalf("screenshot %d: %v", i+1, err)
		}
	}
	if _, err := uc.AddScreenshot(ctx, "adm_1", xo, png); err == nil {
		t.Fatal("a seventh screenshot was accepted")
	}
}

// A studio that moves its document keeps its game: the new address is read,
// written, and logged with where it came from.
func TestMovingTheManifestKeepsTheGame(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	fetcher.Docs["https://new.yourstudio.example/gathering-game.json"] = same()

	g, err := uc.Move(context.Background(), "adm_1", xo, "https://new.yourstudio.example/gathering-game.json")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if g.ManifestURL != "https://new.yourstudio.example/gathering-game.json" {
		t.Fatalf("manifest_url = %q", g.ManifestURL)
	}
	if got := gameOf(t, db, xo).ManifestURL; got != g.ManifestURL {
		t.Fatalf("stored = %q", got)
	}
	w := audit.Written()
	if len(w) != 1 || w[0].Action != model.ActionMoved || w[0].Actor != "adm_1" {
		t.Fatalf("entries = %+v", w)
	}
}

// The new address must still be this game. Pointing one game at another's
// document is refused before anything is written.
func TestMovingToAnotherGamesManifestIsRefused(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	other := same()
	other.ID = "someone.else"
	fetcher.Docs["https://new.yourstudio.example/gathering-game.json"] = other

	_, err := uc.Move(context.Background(), "adm_1", xo, "https://new.yourstudio.example/gathering-game.json")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := gameOf(t, db, xo).ManifestURL; got != "https://games.yourstudio.example/xo"+model.WellKnown {
		t.Fatalf("stored = %q", got)
	}
	if len(audit.Written()) != 0 {
		t.Fatalf("entries = %+v", audit.Written())
	}
}

// Scopes moving under a version that did not is still a change worth a line,
// and still held: the version is the studio's word, the scopes are the fact.
func TestScopesMovingWithoutAVersionIsStillAChange(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	sneaky := wider()
	sneaky.Version = "1.4.0" // unchanged, while the scopes are not
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = sneaky

	done, err := uc.Reread(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if done.Changed != 1 {
		t.Fatalf("counts = %+v", done)
	}
	if len(audit.Written()) != 1 {
		t.Fatalf("wrote %d entries", len(audit.Written()))
	}
}

// A service down all week must not come back and fetch every studio's
// document at once. Checked by what was fetched, not by what the query asked
// for.
func TestTheRoundIsCapped(t *testing.T) {
	uc, fetcher, _, db := testReread(t)

	for _, id := range []string{"a.one", "b.two", "c.three"} {
		m := xoManifest()
		m.Entry = "https://" + id + "/game/"
		biztest.SeedGame(t, db, id, m)
		stale(t, db, id)
	}
	uc.Batch = 2

	done, err := uc.Reread(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// The cap is how many servers one round asks, so that is what is counted.
	// The seeded games have no document and fail, which is another test's
	// subject.
	if len(fetcher.Hits) != 2 {
		t.Errorf("four games were stale and the round fetched %d: %v", len(fetcher.Hits), fetcher.Hits)
	}
	if done.Read+done.Failed != 2 {
		t.Errorf("the round handled %d games: %+v", done.Read+done.Failed, done)
	}
}

// Nothing read recently is read again. The cut-off is what keeps a daily job
// daily rather than a job that re-reads the whole registry every time it runs.
func TestSomethingReadRecentlyIsLeftAlone(t *testing.T) {
	uc, fetcher, _, db := testReread(t)

	fresh := time.Now()
	if err := db.Model(&model.Game{}).Where("id = ?", xo).
		Update("fetched_at", fresh).Error; err != nil {
		t.Fatal(err)
	}

	done, err := uc.Reread(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if done.Read != 0 || len(fetcher.Hits) != 0 {
		t.Fatalf("a game read just now was read again: %+v, %v", done, fetcher.Hits)
	}
}

// The tier is the one field that is a claim about us. A studio must not grant
// it to themselves.
func TestAManifestCannotPromoteItselfToFirstParty(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)

	promoting := same()
	promoting.Publisher.Tier = enum.TierFirstParty
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = promoting

	done, err := uc.Reread(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	if got := gameOf(t, db, xo).Tier; got != enum.TierPartner {
		t.Errorf("the game promoted itself to %q", got)
	}
	if gameOf(t, db, xo).FirstParty {
		t.Error("the game made itself first-party")
	}

	// And the claim is on the record, because an admin should see it and decide.
	if done.Changed != 1 || len(audit.Written()) != 1 {
		t.Fatalf("changed = %d, entries = %d", done.Changed, len(audit.Written()))
	}
	if got := audit.Written()[0].Detail["claimed_tier"]; got != enum.TierFirstParty {
		t.Errorf("claimed_tier = %v", got)
	}
}

// The same claim made to somebody's face still does not take, and still says so.
func TestAskingForARefreshDoesNotTakeTheClaimEither(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)

	promoting := wider()
	promoting.Publisher.Tier = enum.TierFirstParty
	fetcher.Docs[gameOf(t, db, xo).ManifestURL] = promoting

	after, err := uc.Refresh(context.Background(), "adm_1", xo)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if after.Tier != enum.TierPartner || after.FirstParty {
		t.Errorf("tier = %q, first-party = %v", after.Tier, after.FirstParty)
	}
	if got := audit.Written()[0].Detail["claimed_tier"]; got != enum.TierFirstParty {
		t.Errorf("claimed_tier = %v", got)
	}
}

// Registering is reading a document and issuing a secret. The same id twice,
// from wherever, is one game and one mistake.
func TestRegisteringReadsTheDocumentAndIssuesASecret(t *testing.T) {
	uc, fetcher, audit, db := testReread(t)
	tap := xoManifest()
	tap.ID, tap.Name, tap.Entry = "fixture.tap", "سباق النقر", "https://games.yourstudio.example/tap/"
	tap.Room.Players.Max, tap.Room.Host, tap.Room.Pace = 8, enum.HostPeer, enum.PaceRealtime
	fetcher.Docs["https://games.yourstudio.example/tap"+model.WellKnown] = &tap

	g, secret, err := uc.Register(context.Background(), "adm_1", "https://games.yourstudio.example/tap/")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if g.Listing != enum.Unpublished || g.Category != enum.CategoryOther || !strings.HasPrefix(secret, "sec_") {
		t.Fatalf("registered as %s/%s with %q", g.Listing, g.Category, secret)
	}
	if g.Room.Players.Max != 8 || g.Room.Host != enum.HostPeer || g.Orientation != enum.Portrait || g.Room.State != enum.StateBlob {
		t.Fatalf("the manifest's room did not land: %+v orientation=%q", g.Room, g.Orientation)
	}
	if _, err := NewCatalogue(db, nil, nil).Authenticate(context.Background(), "fixture.tap", secret); err != nil {
		t.Fatalf("the secret does not open the game: %v", err)
	}
	if _, err := NewCatalogue(db, nil, nil).Authenticate(context.Background(), "fixture.tap", "sec_wrong"); err == nil {
		t.Fatal("a wrong secret opened the game")
	}
	if len(audit.Written()) != 1 || audit.Written()[0].Action != model.ActionRegistered {
		t.Fatalf("entries = %+v", audit.Written())
	}

	if _, _, err := uc.Register(context.Background(), "adm_1", "https://games.yourstudio.example/tap/"); err == nil {
		t.Fatal("registering twice was accepted")
	}

	// A document with everything wrong comes back as the whole list.
	broken := &model.Manifest{ID: "fixture.broken"}
	fetcher.Docs["https://broken.example"+model.WellKnown] = broken
	_, _, err = uc.Register(context.Background(), "adm_1", "https://broken.example")
	if err == nil || err.Error() != "invalid_fields" {
		t.Fatalf("a broken manifest gave %v", err)
	}
}

// Publishing needs the store copy; suspending reaches every token; featuring
// is a switch, whatever the tier.
func TestPublishSuspendAndFeature(t *testing.T) {
	uc, _, audit, db := testReread(t)
	ctx := context.Background()
	if err := db.Exec(`UPDATE games SET listing = ?, tagline = '', icon_key = '' WHERE id = ?`, enum.Unpublished, xo).Error; err != nil {
		t.Fatal(err)
	}

	_, err := uc.Publish(ctx, "adm_1", xo)
	if err == nil || err.Error() != "incomplete" {
		t.Fatalf("publishing with no store copy gave %v", err)
	}
	line := "لعبة الإكس أو الكلاسيكية"
	if _, err := uc.Listing(ctx, "adm_1", xo, model.Listing{Tagline: &line}); err != nil {
		t.Fatal(err)
	}
	uc.Files = biztest.NewFiles()
	if _, err := uc.SetIcon(ctx, "adm_1", xo, Picture{ContentType: "image/png", Data: []byte("png")}); err != nil {
		t.Fatal(err)
	}
	g, err := uc.Publish(ctx, "adm_1", xo)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if g.Listing != enum.Published {
		t.Fatalf("listing = %s", g.Listing)
	}

	wl := &biztest.Whitelist{}
	uc.revoker = &Revoker{Whitelist: wl}
	g, err = uc.Suspend(ctx, "adm_1", xo, "broken on Android")
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if g.Listing != enum.Suspended || len(wl.RevokedGames) != 1 || wl.RevokedGames[0] != xo {
		t.Fatalf("after suspend: %s, revoked %v", g.Listing, wl.RevokedGames)
	}

	g, err = uc.Feature(ctx, "adm_1", xo, true)
	if err != nil || !g.Featured {
		t.Fatalf("feature: %v %v", err, g.Featured)
	}

	category := "brain"
	g, err = uc.Listing(ctx, "adm_1", xo, model.Listing{Category: &category})
	if err != nil || g.Category != enum.CategoryBrain {
		t.Fatalf("listing: %v %s", err, g.Category)
	}
	unknown := "sports"
	if _, err := uc.Listing(ctx, "adm_1", xo, model.Listing{Category: &unknown}); err == nil {
		t.Fatal("an unknown category was accepted")
	}

	actions := []string{}
	for _, e := range audit.Written() {
		actions = append(actions, e.Action)
	}
	want := []string{model.ActionListed, model.ActionListed, model.ActionPublished, model.ActionSuspended, model.ActionFeatured, model.ActionListed}
	if strings.Join(actions, " ") != strings.Join(want, " ") {
		t.Fatalf("actions = %v", actions)
	}
}
