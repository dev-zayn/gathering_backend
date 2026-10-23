package rooms

import (
	"strings"
	"testing"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// A code is six symbols of the alphabet, every symbol reachable, none
// favoured enough to see in ten thousand draws.
func TestCodesAreDrawnFromTheAlphabet(t *testing.T) {
	s := New(nil, Config{Alphabet: strings.ToLower(alphabet)})
	seen := map[byte]int{}
	for range 10000 {
		c := s.newCode()
		if len(c) != model.RoomCodeLength || !s.validCode(c) {
			t.Fatalf("drew %q", c)
		}
		for i := 0; i < len(c); i++ {
			seen[c[i]]++
		}
	}
	if len(seen) != len(alphabet) {
		t.Fatalf("drew %d of %d symbols", len(seen), len(alphabet))
	}
	for sym, n := range seen {
		// 60000 draws over 32 symbols is 1875 each; a fold-over bias would
		// show as far more than this.
		if n < 1500 || n > 2250 {
			t.Errorf("%c drawn %d times", sym, n)
		}
	}
}

func TestACodeIsReadTheWayItIsReadOut(t *testing.T) {
	s := New(nil, Config{Alphabet: alphabet})
	for typed, want := range map[string]string{
		"K7M2QX":      "K7M2QX",
		"k7m-2qx":     "K7M2QX",
		" k7m 2qx ":   "K7M2QX",
		"K-7-M-2-Q-X": "K7M2QX",
		"k7m\t2qx":    "K7M2QX",
	} {
		if got := normalizeCode(typed); got != want || !s.validCode(got) {
			t.Errorf("%q read as %q", typed, got)
		}
	}
	for _, bad := range []string{"K7M2Q", "K7M2QXX", "K7M2Q0", "K7M2QO", "K7M2Q1", "K7M2QI", "ك7M2QX"} {
		if s.validCode(normalizeCode(bad)) {
			t.Errorf("%q passed", bad)
		}
	}
}

// The code is unique among open rooms only: the insert steps aside for an
// open room holding it, and an ended room frees it.
func TestACodeIsHeldByOneOpenRoom(t *testing.T) {
	f := newFixture(t)
	host, other := f.player("Zayn"), f.player("Sara")
	r := f.room(host, tap)

	now := data.Now()
	clash := &model.Room{
		ID: ids.New(ids.Room), Code: r.Code, GameID: tap, HostID: other.ID, Status: enum.RoomLobby,
		Visibility: enum.VisibilityPrivate, CreatedAt: now, ActiveAt: now,
	}
	placed, err := insertRoom(f.ctx, f.db, clash)
	if err != nil || placed {
		t.Fatalf("placed=%v err=%v: an open room's code was taken twice", placed, err)
	}
	if _, err := f.s.End(f.ctx, host.ID, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	placed, err = insertRoom(f.ctx, f.db, clash)
	if err != nil || !placed {
		t.Fatalf("placed=%v err=%v: an ended room kept its code", placed, err)
	}
}

// Many rooms at once each get a code of their own.
func TestManyRoomsManyCodes(t *testing.T) {
	f := newFixture(t)
	codes := map[string]bool{}
	for range 40 {
		r := f.room(f.player("Zayn"), tap)
		if codes[r.Code] {
			t.Fatalf("%s twice", r.Code)
		}
		codes[r.Code] = true
	}
}

func TestRanks(t *testing.T) {
	three := 3
	got := ranked([]ScoreIn{
		{Player: "a", Score: 5},
		{Player: "b", Score: 9},
		{Player: "c", Score: 5},
		{Player: "d", Score: 1},
		{Player: "e", Score: 0, Rank: &three},
		{Player: "f", Score: 9},
	})
	want := map[string]int{"a": 3, "b": 1, "c": 3, "d": 5, "e": 3, "f": 1}
	for i, s := range got {
		if s.Rank != want[s.Player] {
			t.Errorf("%s ranked %d, want %d", s.Player, s.Rank, want[s.Player])
		}
		if s.Player != string(rune('a'+i)) {
			t.Errorf("the order moved: %v", got)
		}
	}
	if out := ranked(nil); out == nil || len(out) != 0 {
		t.Fatalf("no scores = %v", out)
	}
}
