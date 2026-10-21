package model

import (
	"testing"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

func TestManifestURL(t *testing.T) {
	cases := map[string]string{
		"https://games.yourstudio.example":        "https://games.yourstudio.example" + WellKnown,
		"https://games.yourstudio.example/":       "https://games.yourstudio.example" + WellKnown,
		"  https://games.yourstudio.example  ":    "https://games.yourstudio.example" + WellKnown,
		"https://yourstudio.example/games/xo/":    "https://yourstudio.example/games/xo" + WellKnown,
		"https://yourstudio.example/games/xo":     "https://yourstudio.example/games/xo",
		"https://yourstudio.example/custom.json":  "https://yourstudio.example/custom.json",
		"http://fixtures:8091/xo":                 "http://fixtures:8091/xo",
		"http://fixtures:8091/xo/":                "http://fixtures:8091/xo" + WellKnown,
		"https://www.jsonkeeper.example/b/VFF1P":  "https://www.jsonkeeper.example/b/VFF1P",
		"https://games.yourstudio.example/a/b/c/": "https://games.yourstudio.example/a/b/c" + WellKnown,
	}
	for in, want := range cases {
		got, ok := ManifestURL(in)
		if !ok || got != want {
			t.Fatalf("ManifestURL(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}

	for _, in := range []string{"", "yourstudio.example", "ftp://yourstudio.example", "not a url at all"} {
		if _, ok := ManifestURL(in); ok {
			t.Fatalf("%q was accepted", in)
		}
	}
}

func good() *Manifest {
	m := &Manifest{
		ID: "yourstudio.xo", Name: "إكس أو", Version: "1.4.0",
		Mode: enum.ModeEmbedded, Entry: "https://games.yourstudio.example/xo/",
		Scopes: []string{"identity:read"},
	}
	m.Publisher.Tier = enum.TierPartner
	m.Room.Players.Min, m.Room.Players.Max = 2, 2
	m.Room.Host, m.Room.Pace = enum.HostAuthoritative, enum.PaceTurn
	return m
}

var known = map[string]bool{"identity:read": true, "friends:read": true, "notify:send": true, "results:write": true}

func TestManifestCheckAcceptsAGoodOne(t *testing.T) {
	m := good()
	if bad := m.Check(known); len(bad) > 0 {
		t.Fatalf("a valid manifest was refused: %v", bad)
	}
	// The two defaults are filled in, so the row and the document agree.
	if m.Orientation != enum.Portrait || m.Room.State != enum.StateBlob {
		t.Fatalf("defaults not filled: orientation=%q state=%q", m.Orientation, m.Room.State)
	}
}

// Registering is a thing an admin does with somebody else's file: they should
// see the whole list, not the first problem.
func TestManifestCheckGathersEverything(t *testing.T) {
	m := &Manifest{Scopes: []string{"orders:write"}, Permissions: []string{"bluetooth"}}
	m.Orientation = "sideways"
	m.Room.Host, m.Room.Pace, m.Room.State = "nobody", "slow", "cloud"

	bad := m.Check(known)
	for _, field := range []string{
		"id", "name", "version", "mode", "entry", "orientation", "publisher.tier",
		"scopes", "permissions", "room.players.min", "room.players.max",
		"room.host", "room.pace", "room.state",
	} {
		if _, ok := bad[field]; !ok {
			t.Fatalf("%q was not reported: %v", field, bad)
		}
	}
}

// There is one mode. A document asking for another is refused rather than
// quietly opened in a frame it did not design for.
func TestManifestCheckRefusesAnotherMode(t *testing.T) {
	m := good()
	m.Mode = "external"
	if bad := m.Check(known); bad["mode"] == "" {
		t.Fatalf("an external game was accepted: %v", bad)
	}
}

// The room block is what the platform runs the game under, so every bound is
// checked at registration and not when the first room is made.
func TestManifestCheckOnTheRoomBlock(t *testing.T) {
	for _, c := range []struct {
		min, max int
		field    string
	}{
		{0, 2, "room.players.min"},
		{65, 65, "room.players.min"},
		{2, 1, "room.players.max"},
		{2, 65, "room.players.max"},
	} {
		m := good()
		m.Room.Players.Min, m.Room.Players.Max = c.min, c.max
		if bad := m.Check(known); bad[c.field] == "" {
			t.Errorf("min=%d max=%d: %q was not reported: %v", c.min, c.max, c.field, bad)
		}
	}

	// The widest room there is, and a solo game, are both fine.
	for _, c := range [][2]int{{1, 1}, {1, 64}, {64, 64}} {
		m := good()
		m.Room.Players.Min, m.Room.Players.Max = c[0], c[1]
		if bad := m.Check(known); len(bad) > 0 {
			t.Errorf("min=%d max=%d was refused: %v", c[0], c[1], bad)
		}
	}
}

// We cannot grant access to data we do not hold, so a scope that is not ours
// is not a scope. The same for a permission the app cannot ask the phone for.
func TestManifestCheckRefusesWhatIsNotOurs(t *testing.T) {
	m := good()
	m.Scopes = []string{"identity:read", "orders:write"}
	m.Permissions = []string{"camera", "bluetooth"}

	bad := m.Check(known)
	if _, ok := bad["scopes"]; !ok {
		t.Fatalf("an invented scope was accepted: %v", bad)
	}
	if _, ok := bad["permissions"]; !ok {
		t.Fatalf("an invented permission was accepted: %v", bad)
	}

	m = good()
	m.Permissions = []string{"camera", "microphone", "location", "notification", "storage", "contacts"}
	if bad := m.Check(known); len(bad) > 0 {
		t.Fatalf("every known permission was refused: %v", bad)
	}
}

func TestManifestCheckOnIDs(t *testing.T) {
	for _, id := range []string{"yourstudio.xo", "fixture.tap", "a.b-c", "x.y.z"} {
		m := good()
		m.ID = id
		if bad := m.Check(known); bad["id"] != "" {
			t.Fatalf("%q was refused", id)
		}
	}
	for _, id := range []string{"", "yourstudio", "YourStudio.XO", "your_studio.xo", ".xo", "yourstudio."} {
		m := good()
		m.ID = id
		if bad := m.Check(known); bad["id"] == "" {
			t.Fatalf("%q was accepted", id)
		}
	}
}

// A manifest must carry a name; blank is a tile with nothing on it.
func TestManifestCheckRequiresAName(t *testing.T) {
	m := good()
	m.Name = "  "

	if bad := m.Check(known); bad["name"] == "" {
		t.Fatalf("a manifest with a blank name was accepted: %v", bad)
	}
}

// A redirect that is not an address is one a code could never be delivered to.
func TestManifestCheckOnRedirects(t *testing.T) {
	m := good()
	m.RedirectURIs = []string{"https://games.yourstudio.example/callback", "callback"}
	if bad := m.Check(known); bad["redirect_uris"] == "" {
		t.Fatalf("a bare path was accepted as a redirect: %v", bad)
	}
}

func TestFirstPartyIsTheTierNotAnybodysWord(t *testing.T) {
	m := good()
	if m.FirstParty() {
		t.Fatal("a partner claimed to be first-party")
	}
	m.Publisher.Tier = enum.TierFirstParty
	if !m.FirstParty() {
		t.Fatal("a first-party manifest read as a partner's")
	}
}

// The room block travels as one column, so it has to survive the round trip
// through the driver whole.
func TestRoomRoundTripsThroughTheColumn(t *testing.T) {
	r := good().Room
	r.LAN, r.Rejoin, r.State = true, true, enum.StateNone
	v, err := r.Value()
	if err != nil {
		t.Fatal(err)
	}
	var back Room
	if err := back.Scan(v); err != nil {
		t.Fatal(err)
	}
	if back != r {
		t.Fatalf("room came back as %+v, want %+v", back, r)
	}
}

// The frame may be sent anywhere on the entry's origin unless the manifest
// says where; and never to somebody else's.
func TestRedirectAllowed(t *testing.T) {
	g := &Game{Entry: "https://games.yourstudio.example/xo/"}
	if !g.RedirectAllowed("https://games.yourstudio.example/callback") {
		t.Fatal("the entry's own origin was refused")
	}
	if g.RedirectAllowed("https://evil.example/callback") || g.RedirectAllowed("callback") {
		t.Fatal("another origin was allowed")
	}

	g.RedirectURIs = Strings{"https://app.yourstudio.example/callback"}
	if !g.RedirectAllowed("https://app.yourstudio.example/callback") {
		t.Fatal("a listed redirect was refused")
	}
	if g.RedirectAllowed("https://games.yourstudio.example/callback") {
		t.Fatal("with a list, the entry's origin no longer counts")
	}
}
