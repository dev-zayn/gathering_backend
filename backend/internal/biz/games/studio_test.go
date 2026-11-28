package games

import (
	"context"
	stderrors "errors"
	"testing"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// ── the fixture ──────────────────────────────────────────────────────────────

const (
	dice     = "studio.dice"
	diceURL  = "https://studio.example/dice/.well-known/gathering-game.json"
	betaURL  = "https://studio.example/dice/beta/.well-known/gathering-game.json"
	alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// diceManifest is a publisher's game, which claims to be ours: the claim is
// not kept.
func diceManifest(version string) *model.Manifest {
	m := model.Manifest{
		ID: dice, Name: "نرد", Version: version, Mode: enum.ModeEmbedded,
		Entry:  "https://studio.example/dice/",
		Scopes: []string{"identity:read"},
	}
	m.Publisher.Tier = enum.TierFirstParty
	m.Publisher.Name = "Whatever the document says"
	m.Room.Players.Min, m.Room.Players.Max = 2, 4
	m.Room.Host, m.Room.Pace = enum.HostAuthoritative, enum.PaceTurn
	return &m
}

type studioKit struct {
	db      *gorm.DB
	studio  *Studio
	review  *Review
	cat     *Catalogue
	fetcher *biztest.Fetcher
	files   *biztest.Files
	audit   *biztest.Audit
	who     Account
	other   Account
}

func newStudioKit(t *testing.T) *studioKit {
	t.Helper()
	db := biztest.DB(t)
	fetcher := biztest.NewFetcher()
	fetcher.Docs[diceURL] = diceManifest("1.0.0")
	audit := &biztest.Audit{}
	reg := NewRegistry(db, fetcher, audit, nil)
	files := biztest.NewFiles()
	reg.Files = files
	studio := NewStudio(reg, alphabet)

	k := &studioKit{
		db: db, studio: studio, review: NewReview(studio), cat: NewCatalogue(db, nil, nil),
		fetcher: fetcher, files: files, audit: audit,
		who:   Account{ID: "pub_aaaaaaaaaaaaaaaa", Name: "Dice Studio"},
		other: Account{ID: "pub_bbbbbbbbbbbbbbbb", Name: "Another Studio"},
	}
	for _, a := range []Account{k.who, k.other} {
		if err := db.Create(&model.Publisher{ID: a.ID, Name: a.Name, Email: a.ID + "@studio.example", CreatedAt: data.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return k
}

func (k *studioKit) create(t *testing.T) *StudioGame {
	t.Helper()
	sg, secret, err := k.studio.Create(context.Background(), k.who, diceURL)
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Fatal("no client secret came back")
	}
	return sg
}

// ready gives the open build what a review needs: its words and its tile.
func (k *studioKit) ready(t *testing.T, buildID string) {
	t.Helper()
	ctx := context.Background()
	tagline := "رمية وحدة تكفي"
	if _, err := k.studio.Edit(ctx, k.who, buildID, BuildCopy{Tagline: &tagline}); err != nil {
		t.Fatal(err)
	}
	png := Picture{ContentType: "image/png", Data: []byte("\x89PNG")}
	if _, err := k.studio.SetIcon(ctx, k.who, buildID, png); err != nil {
		t.Fatal(err)
	}
}

func code(t *testing.T, err error) string {
	t.Helper()
	var e *errors.Error
	if !stderrors.As(err, &e) {
		t.Fatalf("not one of our errors: %v", err)
	}
	return e.Code
}

// ── the tests ────────────────────────────────────────────────────────────────

// A publisher's game is made unpublished with its first build under test,
// as a partner's whatever the document claims, under the account's name; and
// it is nobody else's to see.
func TestAPublisherRegistersAGame(t *testing.T) {
	k := newStudioKit(t)
	ctx := context.Background()
	sg := k.create(t)

	g := gameOf(t, k.db, dice)
	if g.Listing != enum.Unpublished || g.Tier != enum.TierPartner || g.FirstParty {
		t.Fatalf("listing %s tier %s first party %v; want unpublished partner", g.Listing, g.Tier, g.FirstParty)
	}
	if g.Publisher != k.who.Name || g.PublisherID == nil || *g.PublisherID != k.who.ID {
		t.Fatalf("publisher %q (%v); want the account's", g.Publisher, g.PublisherID)
	}
	if sg.Build == nil || sg.Build.Status != enum.BuildTesting || sg.Build.Version != "1.0.0" {
		t.Fatalf("first build %+v; want 1.0.0 in testing", sg.Build)
	}
	if _, _, err := k.studio.Create(ctx, k.other, diceURL); code(t, err) != "already_registered" {
		t.Fatalf("a second registration: %v", err)
	}
	if _, err := k.studio.Game(ctx, k.other.ID, dice); code(t, err) != "not_found" {
		t.Fatalf("another studio read it: %v", err)
	}
	if _, err := k.studio.Submit(ctx, k.other, sg.Build.ID); code(t, err) != "not_found" {
		t.Fatalf("another studio submitted it: %v", err)
	}
}

// Review is the gate: words and a tile first, frozen while in review, and a
// publish only after an approval. Publishing makes the build the live game.
func TestABuildGoesThroughReviewToTheStore(t *testing.T) {
	k := newStudioKit(t)
	ctx := context.Background()
	b := k.create(t).Build

	if _, err := k.studio.Submit(ctx, k.who, b.ID); code(t, err) != "incomplete" {
		t.Fatalf("submitting with nothing: %v", err)
	}
	k.ready(t, b.ID)
	if _, err := k.studio.Submit(ctx, k.who, b.ID); err != nil {
		t.Fatal(err)
	}
	tagline := "changed"
	if _, err := k.studio.Edit(ctx, k.who, b.ID, BuildCopy{Tagline: &tagline}); code(t, err) != "in_review" {
		t.Fatalf("editing in review: %v", err)
	}
	if _, err := k.studio.Publish(ctx, k.who, b.ID); code(t, err) != "not_approved" {
		t.Fatalf("publishing in review: %v", err)
	}
	if _, err := k.review.Reject(ctx, "adm_1", b.ID, " "); code(t, err) != "invalid_fields" {
		t.Fatalf("a rejection with no words: %v", err)
	}
	if _, err := k.review.Approve(ctx, "adm_1", b.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.review.Approve(ctx, "adm_1", b.ID, ""); code(t, err) != "not_in_review" {
		t.Fatalf("approving twice: %v", err)
	}

	sg, err := k.studio.Publish(ctx, k.who, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := gameOf(t, k.db, dice)
	if g.Listing != enum.Published || g.Tagline != "رمية وحدة تكفي" || g.IconKey == "" || g.Version != "1.0.0" {
		t.Fatalf("live game %+v; want the build's copy, published", g)
	}
	if sg.Build != nil {
		t.Fatalf("a build is still open after its publish: %+v", sg.Build)
	}
	if got, _ := BuildByID(ctx, k.db, b.ID); got.Status != enum.BuildPublished || got.ClosedAt == nil {
		t.Fatalf("the build is %s; want published and closed", got.Status)
	}

	// The next version must be another version, and there is one at a time.
	if _, err := k.studio.NewBuild(ctx, k.who, dice, ""); code(t, err) != "invalid_fields" {
		t.Fatalf("a next build at the live version: %v", err)
	}
	k.fetcher.Docs[betaURL] = diceManifest("1.1.0")
	next, err := k.studio.NewBuild(ctx, k.who, dice, betaURL)
	if err != nil {
		t.Fatal(err)
	}
	if next.Tagline != g.Tagline || next.IconKey != g.IconKey {
		t.Fatalf("the next build's copy %q %q; want the live game's", next.Tagline, next.IconKey)
	}
	if _, err := k.studio.NewBuild(ctx, k.who, dice, betaURL); code(t, err) != "build_open" {
		t.Fatalf("a second open build: %v", err)
	}
}

// An approval is for what the reviewer saw: a change to the copy takes it
// back to testing; notes for testers do not.
func TestAChangeTakesAnApprovalBack(t *testing.T) {
	k := newStudioKit(t)
	ctx := context.Background()
	b := k.create(t).Build
	k.ready(t, b.ID)
	if _, err := k.studio.Submit(ctx, k.who, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := k.review.Approve(ctx, "adm_1", b.ID, ""); err != nil {
		t.Fatal(err)
	}

	notes := "جرّب الرمية الثانية"
	got, err := k.studio.Edit(ctx, k.who, b.ID, BuildCopy{Notes: &notes})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != enum.BuildApproved {
		t.Fatalf("notes moved the build to %s", got.Status)
	}
	words := "another line"
	got, err = k.studio.Edit(ctx, k.who, b.ID, BuildCopy{Tagline: &words})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != enum.BuildTesting {
		t.Fatalf("a new tagline left the build %s; want testing", got.Status)
	}
}

// A tester gets the build under test, in the store and at the grant; anybody
// else gets nothing of a game never published, and the live game of one that
// is.
func TestATesterGetsTheBuild(t *testing.T) {
	k := newStudioKit(t)
	ctx := context.Background()
	b := k.create(t).Build
	tester := biztest.SeedPlayer(t, k.db, "+9647700000001", "سارة")
	stranger := biztest.SeedPlayer(t, k.db, "+9647700000002", "علي")

	inv, err := k.studio.Invite(ctx, k.who, dice, InviteRequest{Label: "friends"})
	if err != nil {
		t.Fatal(err)
	}
	typed := inv.Code[:4] + "-" + inv.Code[4:]
	joined, err := k.studio.JoinTest(ctx, tester.ID, " "+typed+" ")
	if err != nil {
		t.Fatal(err)
	}
	if !joined.Testing || joined.Already || joined.Game.Version != "1.0.0" {
		t.Fatalf("joined %+v; want the build, first time", joined)
	}
	again, err := k.studio.JoinTest(ctx, tester.ID, inv.Code)
	if err != nil || !again.Already {
		t.Fatalf("joining again: %+v %v", again, err)
	}

	list, err := k.cat.List(ctx, tester.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Game.ID != dice || !list[0].Testing {
		t.Fatalf("the tester's store %+v; want the build alone", list)
	}
	if list, _ := k.cat.List(ctx, stranger.ID, ""); len(list) != 0 {
		t.Fatalf("a stranger's store has %d games; want none", len(list))
	}
	if _, err := k.cat.Open(ctx, stranger.ID, dice); code(t, err) != "not_found" {
		t.Fatalf("a stranger opened it: %v", err)
	}
	if _, err := k.cat.Open(ctx, tester.ID, dice); err != nil {
		t.Fatal(err)
	}
	grant, err := k.cat.Grant(ctx, tester.ID, dice)
	if err != nil || !grant.InLibrary || grant.Game.Entry != b.Entry {
		t.Fatalf("the tester's grant %+v %v", grant, err)
	}

	// Leaving is the end of it: a game never published is nobody's again.
	if err := k.studio.LeaveTest(ctx, tester.ID, dice); err != nil {
		t.Fatal(err)
	}
	if grant, _ := k.cat.Grant(ctx, tester.ID, dice); grant.InLibrary {
		t.Fatal("a former tester still holds a game never published")
	}
}

// A code is eight symbols, used up by its limit, and dead once revoked; a
// player who tests already does not use it up.
func TestACodeHasItsLimits(t *testing.T) {
	k := newStudioKit(t)
	ctx := context.Background()
	k.create(t)
	one, two, three := biztest.SeedPlayer(t, k.db, "+9647700000011", "أ"),
		biztest.SeedPlayer(t, k.db, "+9647700000012", "ب"),
		biztest.SeedPlayer(t, k.db, "+9647700000013", "ج")

	if _, err := k.studio.JoinTest(ctx, one.ID, "SHORT"); code(t, err) != "invalid_fields" {
		t.Fatalf("a short code: %v", err)
	}
	if _, err := k.studio.JoinTest(ctx, one.ID, "ABCDEFGH"); code(t, err) != "invite_not_found" {
		t.Fatalf("a code nobody made: %v", err)
	}
	limit := 1
	inv, err := k.studio.Invite(ctx, k.who, dice, InviteRequest{MaxUses: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.studio.JoinTest(ctx, one.ID, inv.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := k.studio.JoinTest(ctx, one.ID, inv.Code); err != nil {
		t.Fatalf("the tester again: %v", err)
	}
	if _, err := k.studio.JoinTest(ctx, two.ID, inv.Code); code(t, err) != "invite_not_found" {
		t.Fatalf("a code used up: %v", err)
	}

	open, err := k.studio.Invite(ctx, k.who, dice, InviteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.studio.Revoke(ctx, k.who, dice, open.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := k.studio.JoinTest(ctx, three.ID, open.Code); code(t, err) != "invite_not_found" {
		t.Fatalf("a revoked code: %v", err)
	}
	bad := 0
	if _, err := k.studio.Invite(ctx, k.who, dice, InviteRequest{MaxUses: &bad}); code(t, err) != "invalid_fields" {
		t.Fatalf("a code for nobody: %v", err)
	}
}

// Withdrawing a build hands testers the live game back, and deletes the
// pictures that were the build's alone.
func TestWithdrawingABuild(t *testing.T) {
	k := newStudioKit(t)
	ctx := context.Background()
	b := k.create(t).Build
	k.ready(t, b.ID)
	tester := biztest.SeedPlayer(t, k.db, "+9647700000021", "سارة")
	inv, _ := k.studio.Invite(ctx, k.who, dice, InviteRequest{})
	if _, err := k.studio.JoinTest(ctx, tester.ID, inv.Code); err != nil {
		t.Fatal(err)
	}
	icon := mustBuild(t, k.db, b.ID).IconKey

	if _, err := k.studio.Withdraw(ctx, k.who, b.ID); err != nil {
		t.Fatal(err)
	}
	if seen, _ := SeenBy(ctx, k.db, tester.ID, dice); seen != nil {
		t.Fatalf("a tester still sees a withdrawn build: %+v", seen)
	}
	if !contains(k.files.Deleted, icon) {
		t.Fatalf("the build's own icon %q was not deleted (%v)", icon, k.files.Deleted)
	}
	if _, err := k.studio.Withdraw(ctx, k.who, b.ID); code(t, err) != "not_open" {
		t.Fatalf("withdrawing twice: %v", err)
	}
}

func mustBuild(t *testing.T, db *gorm.DB, id string) *model.GameBuild {
	t.Helper()
	b, err := BuildByID(context.Background(), db, id)
	if err != nil || b == nil {
		t.Fatalf("build %s: %v", id, err)
	}
	return b
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
