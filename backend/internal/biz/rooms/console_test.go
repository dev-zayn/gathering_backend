package rooms

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// docs/CONSOLE.md, "Rooms": the console's list with its filters, its pages
// and the live counts, one room read whole, and the close an admin makes for
// everybody in it.

// list is one page of the console's list, failing the test on a refusal.
func (f *fixture) list(q ConsoleQuery) *ConsoleRooms {
	f.t.Helper()
	if q.Limit == 0 {
		q.Limit = 50
	}
	page, err := f.s.List(f.ctx, q)
	if err != nil {
		f.t.Fatalf("list %+v: %v", q, err)
	}
	return page
}

// roomIDs is a page's rooms by id, in its order.
func roomIDs(page *ConsoleRooms) string {
	out := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, it.ID)
	}
	return strings.Join(out, ",")
}

// The list is every room the filters let through, newest first, a page at a
// time, with how many are in each and how many are connected; the live
// counts are the platform's whatever the filters say.
func TestTheConsoleListsRooms(t *testing.T) {
	f := newFixture(t)
	zayn, sara, ali, omar, huda := f.player("Zayn"), f.player("Sara"), f.player("Ali"), f.player("Omar"), f.player("Huda")

	lobby := f.room(zayn, xo)
	f.join(sara, lobby)
	playing := f.room(ali, tap)
	f.join(omar, playing)
	f.start(ali, playing)
	ended := f.room(huda, xo)
	if _, err := f.s.End(f.ctx, huda.ID, ended.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.presence.set(sara.ID, players.State{State: players.InRoom, Room: lobby.ID})
	// A presence key the end has not yet cleared counts for nothing: an ended
	// room has nobody connected.
	f.presence.set(huda.ID, players.State{State: players.InRoom, Room: ended.ID})

	open := f.list(ConsoleQuery{})
	if got := roomIDs(open); got != playing.ID+","+lobby.ID {
		t.Fatalf("open rooms = %s", got)
	}
	if open.Live != (LiveRooms{Lobby: 1, Playing: 1}) || open.NextCursor != nil {
		t.Fatalf("live = %+v, next = %v", open.Live, open.NextCursor)
	}
	p, l := open.Items[0], open.Items[1]
	if p.Status != "playing" || p.Round != 1 || p.StartedAt == nil || p.Members != 2 || p.Connected != 0 ||
		p.Game.ID != tap || p.Game.Name != "سباق النقر" || p.Host.ID != ali.ID || p.Host.Nickname != "Ali" ||
		p.Host.Handle != ali.Handle || p.EndReason != nil || p.EndedAt != nil {
		t.Fatalf("the playing room = %+v", p)
	}
	if l.Status != "lobby" || l.Round != 0 || l.StartedAt != nil || l.Members != 2 || l.Connected != 1 ||
		l.Code != lobby.Code || l.Visibility != "private" || l.Game.Icon == "" || l.CreatedAt == "" {
		t.Fatalf("the lobby = %+v", l)
	}

	for status, want := range map[string]string{
		"lobby": lobby.ID, "playing": playing.ID, "ended": ended.ID, "open": playing.ID + "," + lobby.ID,
		"all": ended.ID + "," + playing.ID + "," + lobby.ID,
	} {
		page := f.list(ConsoleQuery{Status: status})
		if got := roomIDs(page); got != want {
			t.Errorf("status %s = %s, want %s", status, got, want)
		}
		if page.Live != (LiveRooms{Lobby: 1, Playing: 1}) {
			t.Errorf("status %s: live = %+v", status, page.Live)
		}
	}
	gone := f.list(ConsoleQuery{Status: "ended"}).Items[0]
	if gone.EndReason == nil || *gone.EndReason != "host" || gone.EndedAt == nil || gone.Members != 1 || gone.Connected != 0 {
		t.Fatalf("the ended room = %+v", gone)
	}
	if got := roomIDs(f.list(ConsoleQuery{Status: "all", Game: xo})); got != ended.ID+","+lobby.ID {
		t.Fatalf("xo's rooms = %s", got)
	}
	if got := f.list(ConsoleQuery{Game: "studio.nothing"}); len(got.Items) != 0 || got.Live.Lobby != 1 {
		t.Fatalf("a game with no rooms = %+v", got)
	}

	// A page at a time: the cursor carries on where the last page stopped.
	var seen []string
	cursor := ""
	for range 4 {
		page := f.list(ConsoleQuery{Status: "all", Limit: 1, Cursor: cursor})
		seen = append(seen, roomIDs(page))
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if strings.Join(seen, ",") != ended.ID+","+playing.ID+","+lobby.ID {
		t.Fatalf("paged = %v", seen)
	}

	// Every problem at once.
	_, err := f.s.List(f.ctx, ConsoleQuery{Status: "nope", Limit: 0, Cursor: "nope!"})
	e := refusal(err)
	if e == nil || e.Code != "invalid_fields" || e.Fields["status"] != "unknown" ||
		e.Fields["limit"] != "1_to_100" || e.Fields["cursor"] != "invalid" {
		t.Fatalf("refusals = %+v", e)
	}
	_, err = f.s.List(f.ctx, ConsoleQuery{Limit: 101})
	wantField(t, "limit 101", err, "limit", "1_to_100")
	another := base64.RawURLEncoding.EncodeToString([]byte("1700000000000000." + ids.New(ids.Player)))
	_, err = f.s.List(f.ctx, ConsoleQuery{Limit: 10, Cursor: another})
	wantField(t, "a cursor naming a player", err, "cursor", "invalid")
}

// One room read whole: its current members by seat, who is connected, when
// each sat down, its room block, the latest round's results and its history
// newest first. A room that is gone is not found.
func TestTheConsoleReadsOneRoom(t *testing.T) {
	f := newFixture(t)
	host, guest := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(guest, r)
	f.presence.set(guest.ID, players.State{State: players.InRoom, Room: r.ID})
	f.start(host, r)
	f.finish(host, r, ScoreIn{Player: host.ID, Score: 1}, ScoreIn{Player: guest.ID, Score: 3})

	d, err := f.s.Detail(f.ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != r.ID || d.Code != r.Code || d.Status != "lobby" || d.Round != 1 || d.Game.ID != xo ||
		d.Host.ID != host.ID || d.Connected != 1 || len(d.Members) != 2 || d.Config.Max != 2 ||
		d.Config.Host != "authoritative" || d.EndReason != nil {
		t.Fatalf("detail = %+v", d)
	}
	h, g := d.Members[0], d.Members[1]
	if h.ID != host.ID || h.Role != "host" || h.Seat != 0 || h.Connected || h.JoinedAt == "" || h.Handle != host.Handle ||
		g.ID != guest.ID || g.Role != "player" || g.Seat != 1 || !g.Connected || g.Nickname != "Sara" {
		t.Fatalf("members = %+v", d.Members)
	}
	if len(d.Results) != 2 || d.Results[0].Player != guest.ID || d.Results[0].Rank != 1 {
		t.Fatalf("results = %+v", d.Results)
	}
	var kinds []string
	for _, e := range d.Events {
		kinds = append(kinds, e.Event)
	}
	if strings.Join(kinds, ",") != "results,finished,started,joined,created" {
		t.Fatalf("events = %v", kinds)
	}
	if e := d.Events[3]; e.Player == nil || *e.Player != guest.ID || e.Detail["seat"] != float64(1) || e.At == "" {
		t.Fatalf("the join = %+v", e)
	}

	_, err = f.s.Detail(f.ctx, "room_nothingnothing00")
	wantCode(t, "no such room", err, 404, "not_found")
}

// A close is an end with nobody named and the reason admin: in the lobby it
// carries the latest round's results, the sockets hear ended and close, the
// history says which admin, and the audit log keeps the room and its code. A
// room that has ended cannot be closed again.
func TestTheConsoleClosesALobby(t *testing.T) {
	f := newFixture(t)
	log := &biztest.Audit{}
	f.s.Audit = log
	host, guest := f.player("Zayn"), f.player("Sara")
	r := f.room(host, xo)
	f.join(guest, r)
	f.start(host, r)
	f.finish(host, r, ScoreIn{Player: host.ID, Score: 4}, ScoreIn{Player: guest.ID, Score: 2})
	admin := ids.New(ids.Admin)

	d, err := f.s.Close(f.ctx, admin, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "ended" || d.EndReason == nil || *d.EndReason != "admin" || d.EndedAt == nil || d.Round != 1 ||
		len(d.Results) != 2 || d.Results[0].Player != host.ID {
		t.Fatalf("closed = %+v", d)
	}
	if got := kinds(f.pub.published(r.ID)); strings.Join(got, ",") != "ended" {
		t.Fatalf("published = %v", got)
	}
	ends := f.pub.ended(r.ID)
	if len(ends) != 1 || ends[0].By != nil || ends[0].Reason != "admin" || ends[0].Round != 1 || ends[0].At == "" ||
		len(ends[0].Results) != 2 || ends[0].Results[0].Player != host.ID || ends[0].Results[0].Rank != 1 {
		t.Fatalf("ended = %+v", ends)
	}
	lines := f.eventLines(r.ID)
	last := lines[len(lines)-1]
	var detail map[string]any
	if err := json.Unmarshal([]byte(last.Detail), &detail); err != nil {
		t.Fatal(err)
	}
	if last.Event != "ended" || last.PlayerID != "" || detail["reason"] != "admin" || detail["admin"] != admin ||
		detail["round"] != float64(1) {
		t.Fatalf("the ended event = %+v", last)
	}
	written := log.Written()
	if len(written) != 1 || written[0].Action != model.ActionRoomClosed || written[0].Actor != admin ||
		written[0].Subject != r.ID || written[0].Detail["code"] != r.Code || written[0].Detail["game"] != xo ||
		written[0].Detail["round"] != 1 {
		t.Fatalf("audit = %+v", written)
	}

	// The members read it ended by the console; the code is free again.
	seen, err := f.s.Get(f.ctx, guest.ID, r.ID)
	if err != nil || seen.Status != "ended" || seen.EndReason == nil || *seen.EndReason != string(enum.EndAdmin) {
		t.Fatalf("the guest reads %+v, %v", seen, err)
	}
	if d.Events[0].Event != "ended" || d.Events[0].Player != nil || d.Events[0].Detail["reason"] != "admin" {
		t.Fatalf("the detail's newest event = %+v", d.Events[0])
	}

	_, err = f.s.Close(f.ctx, admin, r.ID)
	wantCode(t, "closing it again", err, 409, "room_closed")
	_, err = f.s.Close(f.ctx, admin, "room_nothingnothing00")
	wantCode(t, "no such room", err, 404, "not_found")
	if len(log.Written()) != 1 || len(f.pub.ended(r.ID)) != 1 {
		t.Fatal("a refused close wrote or published something")
	}
}

// A close during a round finishes it, as every end does: the round's results
// are the game's when its server wrote them, else none, and nothing the
// console sends could be scores. No finished frame: ended alone.
func TestTheConsoleClosesARoundUnderWay(t *testing.T) {
	f := newFixture(t)
	host, guest := f.player("Zayn"), f.player("Sara")
	admin := ids.New(ids.Admin)

	scored := f.room(host, xo)
	f.join(guest, scored)
	f.start(host, scored)
	if _, err := f.submit(xo, host, scored.ID, ScoreIn{Player: guest.ID, Score: 7}, ScoreIn{Player: host.ID, Score: 2}); err != nil {
		t.Fatal(err)
	}
	f.pub.published(scored.ID)
	d, err := f.s.Close(f.ctx, admin, scored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "ended" || d.Round != 1 || len(d.Results) != 2 || d.Results[0].Player != guest.ID ||
		d.Results[0].SubmittedBy != "game" {
		t.Fatalf("closed during round 1 = %+v", d)
	}
	if got := kinds(f.pub.published(scored.ID)); strings.Join(got, ",") != "ended" {
		t.Fatalf("published = %v", got)
	}
	if e := f.pub.ended(scored.ID); len(e) != 1 || e[0].Round != 1 || len(e[0].Results) != 2 ||
		e[0].Results[0].Player != guest.ID || e[0].By != nil {
		t.Fatalf("ended = %+v", e)
	}
	if rows := f.rows(scored.ID); len(rows) != 2 || rows[0].SubmittedBy != enum.SubmitterGame {
		t.Fatalf("the game's rows = %+v", rows)
	}
	if c := f.copyOf(scored.ID); len(c) != 2 || c[0].Player != guest.ID {
		t.Fatalf("the room's copy = %+v", c)
	}

	// The same room shape with nothing from the game: no results at all.
	bare := f.room(host, xo)
	f.join(guest, bare)
	f.start(host, bare)
	f.pub.published(bare.ID)
	if d, err = f.s.Close(f.ctx, admin, bare.ID); err != nil {
		t.Fatal(err)
	}
	if e := f.pub.ended(bare.ID); len(e) != 1 || e[0].Round != 1 || len(e[0].Results) != 0 || len(d.Results) != 0 {
		t.Fatalf("a bare round's end = %+v / %+v", e, d.Results)
	}
	if len(f.rows(bare.ID)) != 0 {
		t.Fatal("the close wrote results nobody sent")
	}
}
