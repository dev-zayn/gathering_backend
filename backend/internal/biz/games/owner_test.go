package games

import (
	"context"
	"testing"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// An admin hands a studio's game to another studio: the name players see
// follows, the open build and the testers stay, and the old studio loses it.
// Taking the publisher away waits for the build to close.
func TestAGameChangesHands(t *testing.T) {
	k := newStudioKit(t)
	ctx := context.Background()
	const admin = "adm_aaaaaaaaaaaaaaaa"
	sg := k.create(t)
	before := len(k.audit.Written())

	if _, err := k.review.SetOwner(ctx, admin, "studio.none", k.other.ID); code(t, err) != "not_found" {
		t.Fatalf("a missing game: %v", err)
	}
	if _, err := k.review.SetOwner(ctx, admin, dice, "pub_nnnnnnnnnnnnnnnn"); fields(t, err)["publisher"] != "unknown" {
		t.Fatalf("a missing publisher: %v", err)
	}
	k.db.Model(&model.Publisher{}).Where("id = ?", k.other.ID).Update("disabled_at", data.Now())
	if _, err := k.review.SetOwner(ctx, admin, dice, k.other.ID); fields(t, err)["publisher"] != "disabled" {
		t.Fatalf("a switched-off publisher: %v", err)
	}
	k.db.Model(&model.Publisher{}).Where("id = ?", k.other.ID).Update("disabled_at", nil)
	if _, err := k.review.SetOwner(ctx, admin, dice, ""); code(t, err) != "build_open" {
		t.Fatalf("taking the publisher away with a build open: %v", err)
	}
	if n := len(k.audit.Written()); n != before {
		t.Fatalf("a refusal was audited: %v", actions(k.audit.Written()[before:]))
	}

	g, err := k.review.SetOwner(ctx, admin, dice, k.other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.PublisherID == nil || *g.PublisherID != k.other.ID || g.Publisher != k.other.Name {
		t.Fatalf("owner %v named %q; want %s", g.PublisherID, g.Publisher, k.other.Name)
	}
	if _, err := k.studio.Game(ctx, k.who.ID, dice); code(t, err) != "not_found" {
		t.Fatalf("the old studio still reads it: %v", err)
	}
	theirs, err := k.studio.Game(ctx, k.other.ID, dice)
	if err != nil || theirs.Build == nil || theirs.Build.ID != sg.Build.ID {
		t.Fatalf("the new studio's game %+v, %v; want it with its open build", theirs, err)
	}

	// The same publisher again writes nothing.
	n := len(k.audit.Written())
	if _, err := k.review.SetOwner(ctx, admin, dice, k.other.ID); err != nil {
		t.Fatal(err)
	}
	if len(k.audit.Written()) != n {
		t.Fatal("the same publisher again was audited")
	}

	// Once the build is closed, the game can be nobody's; its name stays.
	if _, err := k.studio.Withdraw(ctx, k.other, sg.Build.ID); err != nil {
		t.Fatal(err)
	}
	g, err = k.review.SetOwner(ctx, admin, dice, "")
	if err != nil {
		t.Fatal(err)
	}
	if g.PublisherID != nil || g.Publisher != k.other.Name {
		t.Fatalf("owner %v named %q; want none, the name kept", g.PublisherID, g.Publisher)
	}

	changes := []map[string]any{}
	for _, e := range k.audit.Written() {
		if e.Action == model.ActionOwnerChanged {
			changes = append(changes, e.Detail)
		}
	}
	if len(changes) != 2 || changes[0]["from"] != k.who.ID || changes[0]["to"] != k.other.ID || changes[1]["to"] != "" {
		t.Fatalf("audited %v", changes)
	}
}
