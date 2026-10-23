package rooms

import (
	"strings"
	"testing"
)

// docs/JOINING.md, "The invite link": the page behind a room's link asks for
// the open room holding a code, read like a typed code, and is told the game
// and the code alone.

// card is the invite link's card for what was typed; a failure fails the test.
func (f *fixture) card(typed string) *LinkCard {
	f.t.Helper()
	c, err := f.s.LinkCard(f.ctx, typed)
	if err != nil {
		f.t.Fatalf("link card %q: %v", typed, err)
	}
	return c
}

// An open room answers in its lobby, while a round plays and between rounds,
// however the code was typed; once it ends, nothing does.
func TestLinkCardFollowsTheRoomWhileItIsOpen(t *testing.T) {
	f := newFixture(t)
	host, guest := f.player("زين"), f.player("مريم")
	r := f.room(host, xo)

	c := f.card(r.Code)
	if c == nil || c.Code != r.Code || c.Game.ID != xo || c.Game.Name != "إكس أو" ||
		c.Game.Icon != "https://signed.test/games/fixture.xo/icon.png" {
		t.Fatalf("the lobby's card: %+v", c)
	}

	// As a friend reads it out: lower case, a dash, spaces around.
	typed := " " + strings.ToLower(r.Code[:3]+"-"+r.Code[3:]) + " "
	if c := f.card(typed); c == nil || c.Code != r.Code {
		t.Fatalf("typed %q: %+v", typed, c)
	}

	f.join(guest, r)
	if _, err := f.s.Start(f.ctx, host.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if c := f.card(r.Code); c == nil || c.Code != r.Code {
		t.Fatalf("while round 1 plays: %+v", c)
	}
	f.finish(host, r)
	if c := f.card(r.Code); c == nil || c.Code != r.Code {
		t.Fatalf("between rounds: %+v", c)
	}

	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if c := f.card(r.Code); c != nil {
		t.Fatalf("an ended room still answers: %+v", c)
	}
}

// A friends-only room's card is the same as anybody's: the join is where
// the door is.
func TestLinkCardIsTheSameForAFriendsOnlyRoom(t *testing.T) {
	f := newFixture(t)
	r, err := f.s.Create(f.ctx, f.player("زين").ID, CreateRequest{Game: tap, Visibility: "friends"})
	if err != nil {
		t.Fatal(err)
	}
	if c := f.card(r.Code); c == nil || c.Code != r.Code || c.Game.ID != tap || c.Game.Name != "سباق النقر" {
		t.Fatalf("a friends-only room's card: %+v", c)
	}
}

// Nothing that cannot be a code, and no code no open room holds, finds a card.
func TestLinkCardFindsNothingElse(t *testing.T) {
	f := newFixture(t)
	r := f.room(f.player("زين"), xo)

	for _, typed := range []string{
		"",
		"K7M",
		"K7M2QXZ",
		"O0I1OO",                        // none of these is in the alphabet
		"K7M2Q!",                        // nor is this
		strings.Repeat("-", 70) + "ABC", // longer than anything worth reading
		"<script>alert(1)</script>",
	} {
		if c := f.card(typed); c != nil {
			t.Errorf("%q found %+v", typed, c)
		}
	}

	// A code in the right shape that no room holds.
	free := "ZZZZZZ"
	if r.Code == free {
		free = "YYYYYY"
	}
	if c := f.card(free); c != nil {
		t.Fatalf("%s found %+v", free, c)
	}
}
