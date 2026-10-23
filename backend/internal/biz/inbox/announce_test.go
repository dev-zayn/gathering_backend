package inbox

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// docs/CONSOLE.md, "Announcements": who each audience reaches, every refusal
// at once, the dry run that writes nothing, the send with one system line per
// player in one transaction and its audit line, and the list of what was sent.

// switchOff is an admin switching a player off.
func (f *fixture) switchOff(p *model.Player) {
	f.t.Helper()
	if err := f.db.Exec(`UPDATE players SET disabled = true WHERE id = ?`, p.ID).Error; err != nil {
		f.t.Fatal(err)
	}
}

// count is how many rows a table holds.
func (f *fixture) count(table string) int64 {
	f.t.Helper()
	var n int64
	if err := f.db.Table(table).Count(&n).Error; err != nil {
		f.t.Fatal(err)
	}
	return n
}

// dry is a dry run's reach for an audience.
func (f *fixture) dry(admin string, a AnnouncementAudience) int {
	f.t.Helper()
	got, err := f.in.Announce(f.ctx, admin, Announce{Audience: &a, Title: "تجربة", DryRun: true})
	if err != nil {
		f.t.Fatalf("dry run %+v: %v", a, err)
	}
	if got.ID != "" {
		f.t.Fatalf("a dry run answered an id: %+v", got)
	}
	return got.Reach
}

// Everybody is every player not switched off; a game's players are those
// with it on their shelf; named players are read by id or by @handle in any
// case, once each, the unknown and the switched off skipped. A dry run counts
// them and writes nothing.
func TestEveryAudienceReachesItsPlayers(t *testing.T) {
	f := newFixture(t)
	admin := biztest.SeedAdmin(t, f.db, "dev:sweep", "Sweep")
	a, b, _, off := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Omar")
	f.switchOff(off)
	biztest.SeedLibrary(t, f.db, a.ID, xo)
	biztest.SeedLibrary(t, f.db, off.ID, xo)

	if n := f.dry(admin.ID, AnnouncementAudience{Kind: AudienceAll}); n != 3 {
		t.Fatalf("everybody = %d", n)
	}
	if n := f.dry(admin.ID, AnnouncementAudience{Kind: AudienceGame, Game: xo}); n != 1 {
		t.Fatalf("xo's players = %d", n)
	}
	named := []string{a.ID, "@" + strings.ToUpper(b.Handle), " " + b.Handle + " ", off.ID, "@nobody_0000", ids.New(ids.Player)}
	if n := f.dry(admin.ID, AnnouncementAudience{Kind: AudiencePlayers, Players: named}); n != 2 {
		t.Fatalf("named players = %d", n)
	}
	if f.count("announcements") != 0 || f.count("notifications") != 0 {
		t.Fatal("a dry run wrote something")
	}
}

// Every problem comes back at once, a dry run's as a send's, and nothing is
// written.
func TestAnAnnouncementRefusesEverythingAtOnce(t *testing.T) {
	f := newFixture(t)
	admin := biztest.SeedAdmin(t, f.db, "dev:sweep", "Sweep")
	off := f.player("Omar")
	f.switchOff(off)
	everybody := &AnnouncementAudience{Kind: AudienceAll}

	for _, dry := range []bool{false, true} {
		_, err := f.in.Announce(f.ctx, admin.ID, Announce{
			Title: "   ", Body: strings.Repeat("ب", maxBody+1), Game: "studio.nothing", DryRun: dry,
		})
		var e *apperr.Error
		if !errors.As(err, &e) || e.Status != 422 || e.Fields["title"] != "required" || e.Fields["body"] != "too_long" ||
			e.Fields["audience"] != "required" || e.Fields["game"] != "not_found" || len(e.Fields) != 4 {
			t.Fatalf("dry %v: %+v", dry, err)
		}
	}

	cases := []struct {
		what     string
		in       Announce
		field    string
		problem  string
		accepted bool
	}{
		{"a title of 61", Announce{Audience: everybody, Title: strings.Repeat("ع", maxTitle+1)}, "title", "too_long", false},
		{"a title of 60 and a body of 240", Announce{Audience: everybody, Title: strings.Repeat("ع", maxTitle), Body: strings.Repeat("ب", maxBody)}, "", "", true},
		{"no kind", Announce{Audience: &AnnouncementAudience{}, Title: "x"}, "audience", "required", false},
		{"a kind nobody knows", Announce{Audience: &AnnouncementAudience{Kind: "everyone"}, Title: "x"}, "audience.kind", "unknown", false},
		{"a game audience naming none", Announce{Audience: &AnnouncementAudience{Kind: AudienceGame}, Title: "x"}, "audience.game", "not_found", false},
		{"a game audience naming nothing", Announce{Audience: &AnnouncementAudience{Kind: AudienceGame, Game: "studio.nothing"}, Title: "x"}, "audience.game", "not_found", false},
		{"no players", Announce{Audience: &AnnouncementAudience{Kind: AudiencePlayers, Players: []string{" "}}, Title: "x"}, "audience.players", "required", false},
		{"501 players", Announce{Audience: &AnnouncementAudience{Kind: AudiencePlayers, Players: make501()}, Title: "x"}, "audience.players", "up_to_500", false},
		{"nobody who may hear it", Announce{Audience: &AnnouncementAudience{Kind: AudiencePlayers, Players: []string{"@nobody_0000", off.ID}}, Title: "x"}, "audience.players", "nobody", false},
	}
	for _, c := range cases {
		c.in.DryRun = true
		_, err := f.in.Announce(f.ctx, admin.ID, c.in)
		if c.accepted {
			if err != nil {
				t.Fatalf("%s: %v", c.what, err)
			}
			continue
		}
		wantField(t, c.what, err, c.field, c.problem)
	}
	if f.count("announcements") != 0 || f.count("notifications") != 0 {
		t.Fatal("a refusal wrote something")
	}
}

func make501() []string {
	out := make([]string, maxRecipients+1)
	for i := range out {
		out[i] = ids.New(ids.Player)
	}
	return out
}

// A send writes the announcement as it was sent and one system line for
// each player it reaches, none for a player switched off, with the game it
// opens in its data and no actor or game of its own; the audit log keeps who
// sent it, to which kind of audience, and how many it reached.
func TestASendWritesOneSystemLinePerPlayer(t *testing.T) {
	f := newFixture(t)
	log := &biztest.Audit{}
	f.in.Audit = log
	admin := biztest.SeedAdmin(t, f.db, "dev:sweep", "Sweep")
	a, b, off := f.player("Zayn"), f.player("Sara"), f.player("Omar")
	f.switchOff(off)

	sent, err := f.in.Announce(f.ctx, admin.ID, Announce{
		Audience: &AnnouncementAudience{Kind: AudiencePlayers, Players: []string{a.ID, "@" + b.Handle, off.ID}},
		Title:    "  صيانة الليلة  ", Body: " نرجع بعد ساعة ", Game: xo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ids.Is(ids.Announcement, sent.ID) || sent.Reach != 2 {
		t.Fatalf("sent = %+v", sent)
	}

	var ann model.Announcement
	if err := f.db.Take(&ann, "id = ?", sent.ID).Error; err != nil {
		t.Fatal(err)
	}
	var aud AnnouncementAudience
	_ = json.Unmarshal(ann.Audience, &aud)
	if ann.AdminID != admin.ID || ann.Title != "صيانة الليلة" || ann.Body != "نرجع بعد ساعة" || ann.GameID == nil ||
		*ann.GameID != xo || ann.Reach != 2 || aud.Kind != AudiencePlayers || len(aud.Players) != 3 || aud.Players[1] != "@"+b.Handle {
		t.Fatalf("the announcement = %+v (%+v)", ann, aud)
	}

	var rows []model.Notification
	if err := f.db.Where("key = ?", "announcement:"+sent.ID).Order("player_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, n := range rows {
		var data map[string]any
		_ = json.Unmarshal(n.Data, &data)
		if (n.PlayerID != a.ID && n.PlayerID != b.ID) || n.Kind != enum.NoticeSystem || n.Title != "صيانة الليلة" ||
			n.Body != "نرجع بعد ساعة" || n.DeepLink != "" || n.ActorID != nil || n.GameID != nil || n.ReadAt != nil ||
			data["announcement"] != sent.ID || data["game"] != xo || len(data) != 2 {
			t.Fatalf("a row = %+v (%v)", n, data)
		}
	}

	// The app reads it like any line: no actor, no game of its own, its data
	// naming the store page to open.
	feed := f.feed(a.ID, "", 20, false)
	if feed.Unread != 1 || len(feed.Items) != 1 {
		t.Fatalf("A's inbox = %+v", feed)
	}
	if n := feed.Items[0]; n.Kind != "system" || n.Actor != nil || n.Game != nil || n.Data["game"] != xo ||
		n.Data["announcement"] != sent.ID || n.Read {
		t.Fatalf("A's line = %+v", n)
	}
	if feed := f.feed(off.ID, "", 20, false); len(feed.Items) != 0 {
		t.Fatalf("the switched-off player heard it: %+v", feed)
	}

	written := log.Written()
	if len(written) != 1 || written[0].Action != model.ActionNotificationSent || written[0].Actor != admin.ID ||
		written[0].Subject != sent.ID || written[0].Detail["audience"] != AudiencePlayers || written[0].Detail["reach"] != 2 {
		t.Fatalf("audit = %+v", written)
	}

	// Everybody, with no game to open: data names the announcement alone.
	all, err := f.in.Announce(f.ctx, admin.ID, Announce{Audience: &AnnouncementAudience{Kind: AudienceAll}, Title: "أهلاً"})
	if err != nil || all.Reach != 2 {
		t.Fatalf("to everybody = %+v, %v", all, err)
	}
	if n := f.feed(b.ID, "", 20, false).Items[0]; n.Title != "أهلاً" || n.Data["announcement"] != all.ID || len(n.Data) != 1 {
		t.Fatalf("B's newest line = %+v", n)
	}
}

// The list is what was sent, newest first, a page at a time, with the game
// and the admin by name and the audience as it was sent.
func TestTheAnnouncementsSent(t *testing.T) {
	f := newFixture(t)
	admin := biztest.SeedAdmin(t, f.db, "dev:sweep", "Sweep")
	p := f.player("Zayn")
	biztest.SeedLibrary(t, f.db, p.ID, xo)

	var sent []string
	for i, in := range []Announce{
		{Audience: &AnnouncementAudience{Kind: AudienceAll}, Title: "الأولى"},
		{Audience: &AnnouncementAudience{Kind: AudienceGame, Game: xo}, Title: "الثانية", Game: xo},
		{Audience: &AnnouncementAudience{Kind: AudiencePlayers, Players: []string{"@" + p.Handle}}, Title: "الثالثة", Body: "نص"},
	} {
		got, err := f.in.Announce(f.ctx, admin.ID, in)
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		sent = append(sent, got.ID)
	}

	first, err := f.in.Announcements(f.ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil || first.Items[0].ID != sent[2] || first.Items[1].ID != sent[1] {
		t.Fatalf("the first page = %+v", first)
	}
	newest, second := first.Items[0], first.Items[1]
	if newest.Title != "الثالثة" || newest.Body != "نص" || newest.Game != nil || newest.Reach != 1 ||
		newest.Admin.ID != admin.ID || newest.Admin.Name != "Sweep" || newest.Audience.Kind != AudiencePlayers ||
		len(newest.Audience.Players) != 1 || newest.CreatedAt == "" {
		t.Fatalf("the newest = %+v", newest)
	}
	if second.Game == nil || second.Game.ID != xo || second.Game.Name != "إكس أو" || second.Audience.Game != xo {
		t.Fatalf("the second = %+v", second)
	}
	rest, err := f.in.Announcements(f.ctx, *first.NextCursor, 2)
	if err != nil || len(rest.Items) != 1 || rest.Items[0].ID != sent[0] || rest.NextCursor != nil {
		t.Fatalf("the last page = %+v, %v", rest, err)
	}

	_, err = f.in.Announcements(f.ctx, "nope!", 0)
	var e *apperr.Error
	if !errors.As(err, &e) || e.Fields["limit"] != "1_to_100" || e.Fields["cursor"] != "invalid" {
		t.Fatalf("refusals = %+v", err)
	}
	_, err = f.in.Announcements(f.ctx, "", 101)
	wantField(t, "limit 101", err, "limit", "1_to_100")
	_, err = f.in.Announcements(f.ctx, cursorOf(time.Now(), "ntf_aaaaaaaaaaaaaaaa"), 10)
	wantField(t, "an inbox cursor", err, "cursor", "invalid")
}
