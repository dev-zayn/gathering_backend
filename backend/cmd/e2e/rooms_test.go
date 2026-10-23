//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
)

// The rooms: a room for the sweep's own game, joined by code as a friend
// reads it out while the host's socket is open (joined, timed), the join
// naming its game as a game's page outside a room does (another game's is
// refused another_game, a member too; without one, as before), both sockets
// on the relay, the shared state, messages to all and to one, then two rounds
// in that one room: the start, a reconnect that catches up, the ticket
// checked the way a game's server checks it, the finish with ranked scores
// that puts the room back in its lobby with the same code and every socket
// open, the second start with the state back at version 0, the second
// finish, and the end closing every socket. Each round's scores are its
// results: one history item per round for each player, and the room's sheet
// for each round. Then a second room: a join heard by the host (timed), the
// host leaving heard by the next seat (left, timed) and handing it over, a
// join heard by the new host (timed), and a kick closing the kicked member's
// socket and heard by the host (left, timed). joined and left must reach a
// socket within a second of the call returning (prompt §9).
func TestRooms(t *testing.T) {
	id := "sweep." + strconv.FormatInt(time.Now().UnixNano(), 36)
	studio := serveStudio(t, id)

	admin := newClient(t)
	admin.origin = console
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, nil)
	admin.call("PATCH", "/v1/admin/games/"+id, map[string]any{"tagline": "غرفة السويب", "category": "brain"}, 200, nil)
	admin.upload("PUT", "/v1/admin/games/"+id+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, nil)
	t.Cleanup(func() {
		admin.call("POST", "/v1/admin/games/"+id+"/suspend", map[string]any{"reason": "the rooms sweep is over"}, 200, nil)
	})

	a, pa := player(t, "زين")
	b, pb := player(t, "Mariam")
	c, pc := player(t, "Ali")
	aID, bID, cID := pa["id"].(string), pb["id"].(string), pc["id"].(string)

	// A makes a room; B joins it by the code as a friend reads it out.
	var room roomJSON
	a.call("POST", "/v1/rooms", map[string]any{"game": id}, 201, &room)
	if room.Status != "lobby" || room.You == nil || room.You.Role != "host" || len(room.Code) != 6 ||
		room.Game.ID != id || room.Config.Max != 2 || room.Config.Host != "authoritative" {
		t.Fatalf("room: %+v", room)
	}

	// Its invite link: the page behind it shows the code and nothing about
	// who is in the room; a code no room could hold is the page's not-found
	// form; and the two documents the phones check before they let the app
	// catch the link.
	inviteLink(t, room, "زين", aID, bID)

	// A's socket is open before B joins, so it hears B arrive.
	sa := connect(t, a, room.ID, 0, false)
	if h := sa.next(); h.T != "hello" || h.Room == nil || h.Room.ID != room.ID || h.Room.You == nil ||
		string(h.State) != `{"version":0,"data":null}` {
		t.Fatalf("A's hello: %s", h.raw)
	}

	// B joins from a game's own page, outside any room, so the join names the
	// game the page expects. A page of another game is refused another_game
	// and nobody joins; the room's own game joins.
	typed := strings.ToLower(room.Code[:3] + "-" + room.Code[3:])
	const elsewhere = "fixture.xo" // any game but the room's: the join reads no registry
	var wrong errorJSON
	b.call("POST", "/v1/rooms/join", map[string]any{"code": typed, "game": elsewhere}, 409, &wrong)
	if wrong.Error != "another_game" {
		t.Fatalf("another game's page: %+v", wrong)
	}
	sa.quiet("joined")
	var joined roomJSON
	b.call("POST", "/v1/rooms/join", map[string]any{"code": typed, "game": id}, 200, &joined)
	returned := time.Now()
	if joined.ID != room.ID || joined.You == nil || joined.You.Seat != 1 || len(joined.Members) != 2 {
		t.Fatalf("joined: %+v", joined)
	}
	t.Logf("ok   B joined %s by typing %q from its game's page; another game's page was refused another_game", room.Code, typed)
	heard(t, sa.expect("joined"), returned, "B's join on A's socket", bID)

	// Another game's page refuses even a member, who stays in; without game
	// the code may be any game's, as before, and B gets the room back.
	b.call("POST", "/v1/rooms/join", map[string]any{"code": room.Code, "game": elsewhere}, 409, &wrong)
	if wrong.Error != "another_game" {
		t.Fatalf("a member on another game's page: %+v", wrong)
	}
	var back roomJSON
	b.call("POST", "/v1/rooms/join", map[string]any{"code": room.Code}, 200, &back)
	if back.ID != room.ID || back.You == nil || back.You.Seat != 1 || len(back.Members) != 2 {
		t.Fatalf("B without game: %+v", back)
	}
	t.Logf("ok   a member on another game's page was refused another_game; without game B got the room back")
	var full map[string]any
	c.call("POST", "/v1/rooms/join", map[string]any{"code": room.Code}, 409, &full)
	if full["error"] != "room_full" {
		t.Fatalf("a third player: %v", full)
	}
	var other map[string]any
	a.call("POST", "/v1/rooms", map[string]any{"game": id}, 409, &other)
	if other["error"] != "in_another_room" || other["room"] != room.ID {
		t.Fatalf("a second room: %v", other)
	}

	// Both sockets: the app's bearer in the header (A's, above), a browser's
	// as a subprotocol. hello first, each.
	sb := connect(t, b, room.ID, 0, true)
	if h := sb.next(); h.T != "hello" || h.Room == nil || h.Room.ID != room.ID || h.Room.You == nil ||
		string(h.State) != `{"version":0,"data":null}` {
		t.Fatalf("B's hello: %s", h.raw)
	}
	t.Logf("ok   both sockets said hello")

	// The shared state: the host writes v0 → v1, everybody sees it; a player
	// may not write in an authoritative room.
	sa.send(map[string]any{"t": "state", "version": 0, "data": map[string]any{"board": "x--------"}, "ref": "s1"})
	for _, s := range []*socket{sa, sb} {
		st := s.expect("state")
		if st.Version == nil || *st.Version != 1 || st.Ref != "s1" || st.By == nil || *st.By != aID {
			t.Fatalf("%s state: %s", s.who, st.raw)
		}
	}
	t.Logf("ok   the host wrote the state v0 → v1 and B saw it")
	sb.send(map[string]any{"t": "state", "version": 1, "data": 1, "ref": "s2"})
	if f := sb.expect("error"); f.Code != "host_only" || f.Ref != "s2" {
		t.Fatalf("B's write: %s", f.raw)
	}
	t.Logf("ok   B's state write refused host_only")

	// Messages: to everybody, the sender included; to one, that one alone.
	sb.send(map[string]any{"t": "msg", "data": map[string]any{"cell": 4}})
	ma, mb := sa.expect("msg"), sb.expect("msg")
	if ma.From != bID || ma.Seq == 0 || mb.Seq != ma.Seq || string(ma.Data) != `{"cell":4}` {
		t.Fatalf("msg: %s / %s", ma.raw, mb.raw)
	}
	t.Logf("ok   B's msg reached A (from %s, seq %d) and came back to B", ma.From, ma.Seq)
	sa.send(map[string]any{"t": "msg", "to": bID, "data": "psst"})
	if m := sb.expect("msg"); m.To != bID || m.From != aID {
		t.Fatalf("to B: %s", m.raw)
	}
	sa.quiet("msg")
	t.Logf("ok   a msg to B reached B alone")

	// Round 1: both hear it start, the room is playing, and the state the
	// lobby wrote is gone: a write based on it is refused with version 0.
	var playing roomJSON
	a.call("POST", "/v1/rooms/"+room.ID+"/start", nil, 200, &playing)
	if playing.Status != "playing" || playing.Round != 1 || playing.StartedAt == nil || playing.StateVersion != 0 {
		t.Fatalf("after the start: %+v", playing)
	}
	for _, s := range []*socket{sa, sb} {
		if st := s.expect("started"); st.By == nil || *st.By != aID || st.At == "" || st.Round != 1 {
			t.Fatalf("%s started: %s", s.who, st.raw)
		}
	}
	sa.send(map[string]any{"t": "state", "version": 1, "data": map[string]any{"board": "stale"}, "ref": "s3"})
	if f := sa.expect("error"); f.Code != "state_stale" || f.Version == nil || *f.Version != 0 || f.Ref != "s3" {
		t.Fatalf("a write from the lobby's state: %s", f.raw)
	}
	sa.send(map[string]any{"t": "state", "version": 0, "data": map[string]any{"board": "o--------"}, "ref": "s4"})
	for _, s := range []*socket{sa, sb} {
		if st := s.expect("state"); st.Version == nil || *st.Version != 1 || st.Ref != "s4" {
			t.Fatalf("%s state: %s", s.who, st.raw)
		}
	}
	t.Logf("ok   round 1 started; the state began again at version 0")

	// B drops and comes back with after: the replay is what it missed.
	last := sb.lastSeq
	sb.close()
	sa.send(map[string]any{"t": "msg", "data": "while you were away"})
	sa.expect("msg")
	sb = connect(t, b, room.ID, last, false)
	if h := sb.next(); h.T != "hello" {
		t.Fatalf("hello again: %s", h.raw)
	}
	replay := sb.next()
	if replay.T != "replay" || len(replay.Frames) == 0 || !strings.Contains(string(replay.raw), "while you were away") {
		t.Fatalf("replay: %s", replay.raw)
	}
	t.Logf("ok   a reconnect with after=%d got a replay of %d frames", last, len(replay.Frames))

	// The ticket, checked the way a game's server checks it: the signature
	// against the published JWKS, then the claims, the round among them.
	var ticket struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	a.call("GET", "/v1/rooms/"+room.ID+"/ticket", nil, 200, &ticket)
	claims := verifyTicket(t, ticket.Token, id)
	if claims["sub"] != aID || claims["room"] != room.ID || claims["role"] != "host" || claims["seat"] != float64(0) ||
		claims["round"] != float64(1) || claims["scope"] != "room" || claims["kind"] != "room" || ticket.ExpiresIn <= 0 {
		t.Fatalf("ticket claims: %v", claims)
	}
	t.Logf("ok   the ticket verifies against the JWKS: sub, aud, room, role, seat, round 1")

	// A finish is the host's, from a round: B is refused.
	var refused errorJSON
	b.call("POST", "/v1/rooms/"+room.ID+"/finish", nil, 403, &refused)
	if refused.Error != "host_only" {
		t.Fatalf("B finishing: %+v", refused)
	}

	// The finish: scores ranked, both sockets told, nobody closed, and the
	// room back in its lobby with the same code.
	var lobby roomJSON
	a.call("POST", "/v1/rooms/"+room.ID+"/finish", map[string]any{"scores": []map[string]any{
		{"player": aID, "score": 3}, {"player": bID, "score": 5},
	}}, 200, &lobby)
	if lobby.Status != "lobby" || lobby.Round != 1 || lobby.Code != room.Code || lobby.EndReason != nil ||
		len(lobby.Results) != 2 || lobby.Results[0].Player != bID || lobby.Results[0].Rank != 1 {
		t.Fatalf("finished: %+v", lobby)
	}
	for _, s := range []*socket{sa, sb} {
		f := s.expect("finished")
		if f.Reason != "host" || f.Round != 1 || f.By == nil || *f.By != aID || len(f.Results) != 2 ||
			f.Results[0].Rank != 2 || f.Results[1].Rank != 1 {
			t.Fatalf("%s finished: %s", s.who, f.raw)
		}
	}
	sb.send(map[string]any{"t": "msg", "data": "again?"})
	if m := sa.expect("msg"); m.From != bID {
		t.Fatalf("after the finish: %s", m.raw)
	}
	a.call("POST", "/v1/rooms/"+room.ID+"/finish", nil, 409, &refused)
	if refused.Error != "room_not_started" {
		t.Fatalf("finishing the lobby: %+v", refused)
	}
	for _, cl := range []*client{a, b} {
		var mine struct {
			Rooms []roomJSON `json:"rooms"`
		}
		cl.call("GET", "/v1/rooms/mine", nil, 200, &mine)
		if len(mine.Rooms) != 1 || mine.Rooms[0].ID != room.ID || mine.Rooms[0].Code != room.Code ||
			mine.Rooms[0].Status != "lobby" || mine.Rooms[0].Round != 1 {
			t.Fatalf("rooms/mine after round 1: %+v", mine.Rooms)
		}
	}
	t.Logf("ok   round 1 finished with ranks; both sockets open; the room waits in its lobby as %s", room.Code)
	if page := linkPage(t, "/r/"+room.Code, http.StatusOK); !strings.Contains(page, ">"+room.Code+"<") {
		t.Fatalf("the link's page between rounds: %s", page)
	}

	// Round 2, in the same room: started says so, and the state is back at
	// version 0, as the room and a fresh hello both say.
	var second2 roomJSON
	a.call("POST", "/v1/rooms/"+room.ID+"/start", nil, 200, &second2)
	if second2.Status != "playing" || second2.Round != 2 || second2.Code != room.Code || second2.StateVersion != 0 ||
		len(second2.Results) != 0 {
		t.Fatalf("round 2: %+v", second2)
	}
	for _, s := range []*socket{sa, sb} {
		if st := s.expect("started"); st.Round != 2 || st.By == nil || *st.By != aID {
			t.Fatalf("%s started: %s", s.who, st.raw)
		}
	}
	sb.close()
	sb = connect(t, b, room.ID, 0, true)
	if h := sb.next(); h.T != "hello" || string(h.State) != `{"version":0,"data":null}` || h.Room == nil ||
		h.Room.Round != 2 || h.Room.Status != "playing" {
		t.Fatalf("a fresh hello in round 2: %s", h.raw)
	}
	t.Logf("ok   round 2 started in the same room; a fresh hello reads the state at version 0")

	// Round 2's finish: A wins this one.
	a.call("POST", "/v1/rooms/"+room.ID+"/finish", map[string]any{"scores": []map[string]any{
		{"player": aID, "score": 9}, {"player": bID, "score": 1},
	}}, 200, &lobby)
	if lobby.Status != "lobby" || lobby.Round != 2 || len(lobby.Results) != 2 || lobby.Results[0].Player != aID {
		t.Fatalf("round 2 finished: %+v", lobby)
	}
	for _, s := range []*socket{sa, sb} {
		if f := s.expect("finished"); f.Round != 2 || len(f.Results) != 2 || f.Results[0].Rank != 1 {
			t.Fatalf("%s finished: %s", s.who, f.raw)
		}
	}
	t.Logf("ok   round 2 finished: A first")

	// The history: one item per round, newest first, a page at a time.
	for _, p := range []struct {
		cl     *client
		ranks  [2]int
		scores [2]float64
	}{{a, [2]int{1, 2}, [2]float64{9, 3}}, {b, [2]int{2, 1}, [2]float64{1, 5}}} {
		var history historyJSON
		p.cl.call("GET", "/v1/me/history", nil, 200, &history)
		if len(history.Items) != 2 || history.NextCursor != nil {
			t.Fatalf("history: %+v", history)
		}
		for i, it := range history.Items {
			if it.Room != room.ID || it.Code != room.Code || it.Game.ID != id || it.Round != 2-i || it.Rank != p.ranks[i] ||
				it.Score != p.scores[i] || it.Players != 2 || it.SubmittedBy != "host" || it.FinishedAt == "" {
				t.Fatalf("history item %d: %+v", i, it)
			}
		}
		var page historyJSON
		p.cl.call("GET", "/v1/me/history?limit=1", nil, 200, &page)
		if len(page.Items) != 1 || page.Items[0].Round != 2 || page.NextCursor == nil {
			t.Fatalf("the first page: %+v", page)
		}
		p.cl.call("GET", "/v1/me/history?limit=1&cursor="+*page.NextCursor, nil, 200, &page)
		if len(page.Items) != 1 || page.Items[0].Round != 1 || page.Items[0].Room != room.ID || page.NextCursor != nil {
			t.Fatalf("the second page: %+v", page)
		}
	}
	t.Logf("ok   one history item per round for each player, paged by a cursor that carries the round")

	// The sheet, round by round, for the room's members alone.
	var sheet sheetJSON
	a.call("GET", "/v1/rooms/"+room.ID+"/results?round=1", nil, 200, &sheet)
	if sheet.Room != room.ID || sheet.Code != room.Code || sheet.Game.ID != id || sheet.Round != 1 ||
		fmt.Sprint(sheet.Rounds) != "[1 2]" || sheet.FinishedAt == nil || len(sheet.Results) != 2 ||
		sheet.Results[0].Player != bID || sheet.Results[0].Rank != 1 || sheet.Results[0].Nickname != "Mariam" ||
		sheet.Results[0].SubmittedBy != "host" || sheet.Results[1].Player != aID || sheet.Results[1].Rank != 2 {
		t.Fatalf("round 1's results: %+v", sheet)
	}
	b.call("GET", "/v1/rooms/"+room.ID+"/results?round=2", nil, 200, &sheet)
	if sheet.Round != 2 || fmt.Sprint(sheet.Rounds) != "[1 2]" || len(sheet.Results) != 2 ||
		sheet.Results[0].Player != aID || sheet.Results[0].Rank != 1 || sheet.Results[1].Player != bID {
		t.Fatalf("round 2's results: %+v", sheet)
	}
	b.call("GET", "/v1/rooms/"+room.ID+"/results", nil, 200, &sheet)
	if sheet.Round != 2 || len(sheet.Results) != 2 {
		t.Fatalf("the latest round's results: %+v", sheet)
	}
	var bad errorJSON
	a.call("GET", "/v1/rooms/"+room.ID+"/results?round=0", nil, 422, &bad)
	if bad.Fields["round"] != "must_be_positive" {
		t.Fatalf("round 0: %+v", bad)
	}
	c.call("GET", "/v1/rooms/"+room.ID+"/results?round=1", nil, 404, nil)
	t.Logf("ok   the room's results per round (rounds [1 2]), for its members alone")

	// Closing the room: no scores in the lobby; without them, ended carries
	// the latest round's results and every socket closes with 4004.
	a.call("POST", "/v1/rooms/"+room.ID+"/end", map[string]any{"scores": []map[string]any{
		{"player": aID, "score": 1},
	}}, 422, &bad)
	if bad.Fields["scores"] != "not_playing" {
		t.Fatalf("scores in the lobby: %+v", bad)
	}
	var ended roomJSON
	a.call("POST", "/v1/rooms/"+room.ID+"/end", nil, 200, &ended)
	if ended.Status != "ended" || ended.Round != 2 || ended.EndReason == nil || *ended.EndReason != "host" || len(ended.Results) != 2 {
		t.Fatalf("ended: %+v", ended)
	}
	for _, s := range []*socket{sa, sb} {
		f := s.expect("ended")
		if f.Reason != "host" || f.Round != 2 || len(f.Results) != 2 || f.Results[0].Player != aID || f.Results[0].Rank != 1 {
			t.Fatalf("%s ended: %s", s.who, f.raw)
		}
		if code, reason := s.closed(); code != 4004 || reason != "ended" {
			t.Fatalf("%s closed %d %q", s.who, code, reason)
		}
	}
	t.Logf("ok   the host closed the room; ended carried round 2's results; both sockets closed 4004")
	if page := linkPage(t, "/r/"+room.Code, http.StatusNotFound); strings.Contains(page, room.Code) {
		t.Fatalf("the ended room's link still shows its code: %s", page)
	}
	t.Logf("ok   the ended room's link is the page's not-found form")
	for _, cl := range []*client{a, b} {
		var mine struct {
			Rooms []roomJSON `json:"rooms"`
		}
		cl.call("GET", "/v1/rooms/mine", nil, 200, &mine)
		if len(mine.Rooms) != 0 {
			t.Fatalf("rooms/mine after the end: %+v", mine.Rooms)
		}
	}
	a.call("GET", "/v1/me/history?limit=0", nil, 422, &bad)
	if bad.Fields["limit"] != "1_to_50" {
		t.Fatalf("limit 0: %+v", bad)
	}
	a.call("GET", "/v1/me/history?cursor=nope", nil, 422, &bad)
	if bad.Fields["cursor"] != "invalid" {
		t.Fatalf("a cursor never handed out: %+v", bad)
	}
	t.Logf("ok   the history refuses limit 0 and a cursor it never handed out")

	// A second room: the host hears B join; B hears the host leave and hands
	// the room to B; B hears C join; a kick closes the kicked member's socket
	// and B hears C go.
	var second roomJSON
	a.call("POST", "/v1/rooms", map[string]any{"game": id}, 201, &second)
	sa = connect(t, a, second.ID, 0, false)
	sa.expect("hello")
	b.call("POST", "/v1/rooms/join", map[string]any{"code": second.Code}, 200, nil)
	returned = time.Now()
	heard(t, sa.expect("joined"), returned, "B's join on A's socket, second room", bID)
	sb = connect(t, b, second.ID, 0, false)
	sb.expect("hello")
	a.call("POST", "/v1/rooms/"+second.ID+"/leave", nil, 204, nil)
	returned = time.Now()
	if h := sb.expect("host"); h.By != nil || !strings.Contains(string(h.Player), bID) {
		t.Fatalf("host: %s", h.raw)
	}
	if l := heard(t, sb.expect("left"), returned, "A's leave on B's socket", aID); l.Reason != "left" {
		t.Fatalf("left: %s", l.raw)
	}
	var handed roomJSON
	b.call("GET", "/v1/rooms/"+second.ID, nil, 200, &handed)
	if handed.Host != bID || handed.You == nil || handed.You.Role != "host" {
		t.Fatalf("after the host left: %+v", handed)
	}
	t.Logf("ok   the host left; B is the host")

	c.call("POST", "/v1/rooms/join", map[string]any{"code": second.Code}, 200, nil)
	returned = time.Now()
	heard(t, sb.expect("joined"), returned, "C's join on B's socket", cID)
	sc := connect(t, c, second.ID, 0, false)
	sc.expect("hello")
	b.call("POST", "/v1/rooms/"+second.ID+"/kick", map[string]any{"player": cID}, 204, nil)
	returned = time.Now()
	if l := heard(t, sb.expect("left"), returned, "C's kick on B's socket", cID); l.Reason != "kicked" {
		t.Fatalf("B heard C go: %s", l.raw)
	}
	sc.expect("kicked")
	if l := sc.expect("left"); l.Reason != "kicked" {
		t.Fatalf("left: %s", l.raw)
	}
	if code, reason := sc.closed(); code != 4003 || reason != "kicked" {
		t.Fatalf("the kicked socket closed %d %q", code, reason)
	}
	var again map[string]any
	c.call("POST", "/v1/rooms/join", map[string]any{"code": second.Code}, 403, &again)
	if again["error"] != "kicked" {
		t.Fatalf("the kicked player rejoining: %v", again)
	}
	t.Logf("ok   the kick closed the socket 4003 and the player stays out")

	b.call("POST", "/v1/rooms/"+second.ID+"/leave", nil, 204, nil)
	if code, reason := sb.closed(); code != 4003 || reason != "left" {
		t.Fatalf("the leaver's socket closed %d %q", code, reason)
	}
	var gone roomJSON
	b.call("GET", "/v1/rooms/"+second.ID, nil, 200, &gone)
	if gone.Status != "ended" || gone.EndReason == nil || *gone.EndReason != "empty" {
		t.Fatalf("the empty room: %+v", gone)
	}
	t.Logf("ok   the last member left; the room ended empty")
}

// roomJSON is as much of a Room as the sweep reads.
type roomJSON struct {
	ID           string `json:"id"`
	Code         string `json:"code"`
	Status       string `json:"status"`
	Round        int    `json:"round"`
	Visibility   string `json:"visibility"`
	Host         string `json:"host"`
	Link         string `json:"link"`
	StateVersion int    `json:"state_version"`
	Game         struct {
		ID string `json:"id"`
	} `json:"game"`
	Config struct {
		Max  int    `json:"max"`
		Host string `json:"host"`
	} `json:"config"`
	Members []map[string]any `json:"members"`
	You     *struct {
		Role string `json:"role"`
		Seat int    `json:"seat"`
	} `json:"you"`
	EndReason *string     `json:"end_reason"`
	Results   []scoreJSON `json:"results"`
	StartedAt *string     `json:"started_at"`
}

type scoreJSON struct {
	Player string  `json:"player"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank"`
}

// errorJSON is the API's one error body.
type errorJSON struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields"`
	Scope  string            `json:"scope"`
}

// resultJSON is one line of a room's results.
type resultJSON struct {
	Player      string  `json:"player"`
	Nickname    string  `json:"nickname"`
	Score       float64 `json:"score"`
	Rank        int     `json:"rank"`
	SubmittedBy string  `json:"submitted_by"`
}

// sheetJSON is a round's result sheet.
type sheetJSON struct {
	Room string `json:"room"`
	Code string `json:"code"`
	Game struct {
		ID string `json:"id"`
	} `json:"game"`
	Round      int          `json:"round"`
	Rounds     []int        `json:"rounds"`
	FinishedAt *string      `json:"finished_at"`
	Results    []resultJSON `json:"results"`
}

// historyJSON is a page of a player's history.
type historyJSON struct {
	Items []struct {
		Room string `json:"room"`
		Code string `json:"code"`
		Game struct {
			ID string `json:"id"`
		} `json:"game"`
		Round       int     `json:"round"`
		Score       float64 `json:"score"`
		Rank        int     `json:"rank"`
		Players     int     `json:"players"`
		FinishedAt  string  `json:"finished_at"`
		SubmittedBy string  `json:"submitted_by"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// frame is a frame as a client reads it.
type frame struct {
	T       string            `json:"t"`
	Seq     int64             `json:"seq"`
	From    string            `json:"from"`
	To      string            `json:"to"`
	Data    json.RawMessage   `json:"data"`
	Version *int              `json:"version"`
	By      *string           `json:"by"`
	Ref     string            `json:"ref"`
	Code    string            `json:"code"`
	Player  json.RawMessage   `json:"player"`
	Reason  string            `json:"reason"`
	Frames  []json.RawMessage `json:"frames"`
	Room    *roomJSON         `json:"room"`
	State   json.RawMessage   `json:"state"`
	Results []scoreJSON       `json:"results"`
	At      string            `json:"at"`
	Round   int               `json:"round"`
	raw     []byte

	// arrived is when the socket read it, for the timed frames.
	arrived time.Time
}

// socket is one client's socket on the relay.
type socket struct {
	t       *testing.T
	who     string
	c       *websocket.Conn
	frames  chan frame
	done    chan struct{}
	lastSeq int64
	code    websocket.StatusCode
	reason  string
}

// connect opens the room's socket with the gathering.v1 subprotocol: the
// bearer in a header as the app sends it, or as a second subprotocol as a
// browser must.
func connect(t *testing.T, cl *client, roomID string, after int64, asBrowser bool) *socket {
	t.Helper()
	u := "ws" + strings.TrimPrefix(apiURL, "http") + "/v1/rooms/" + roomID + "/socket"
	if after > 0 {
		u += fmt.Sprintf("?after=%d", after)
	}
	opts := &websocket.DialOptions{Subprotocols: []string{"gathering.v1"}}
	if asBrowser {
		opts.Subprotocols = append(opts.Subprotocols, "bearer."+cl.bearer)
	} else {
		opts.HTTPHeader = http.Header{"Authorization": {"Bearer " + cl.bearer}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, u, opts)
	if err != nil {
		t.Fatalf("socket %s: %v (%v)", roomID, err, res)
	}
	if conn.Subprotocol() != "gathering.v1" {
		t.Fatalf("the server selected %q", conn.Subprotocol())
	}
	conn.SetReadLimit(1 << 20)
	s := &socket{t: t, who: cl.bearer[len(cl.bearer)-8:], c: conn, frames: make(chan frame, 1024), done: make(chan struct{})}
	go s.read()
	t.Cleanup(func() { _ = conn.CloseNow(); <-s.done })
	t.Logf("ok   socket open to %s (after=%d)", roomID, after)
	return s
}

func (s *socket) read() {
	defer close(s.done)
	defer close(s.frames)
	for {
		_, b, err := s.c.Read(context.Background())
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				s.code, s.reason = ce.Code, ce.Reason
			}
			return
		}
		var f frame
		if json.Unmarshal(b, &f) == nil {
			f.raw, f.arrived = b, time.Now()
			s.frames <- f
		}
	}
}

func (s *socket) send(v any) {
	s.t.Helper()
	b, _ := json.Marshal(v)
	if err := s.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		s.t.Fatalf("send: %v", err)
	}
}

func (s *socket) next() frame {
	s.t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			s.t.Fatalf("%s: the socket closed (%d %q) while waiting", s.who, s.code, s.reason)
		}
		if f.Seq > s.lastSeq && f.T != "hello" {
			s.lastSeq = f.Seq
		}
		if f.T == "hello" && f.Seq > s.lastSeq {
			s.lastSeq = f.Seq
		}
		return f
	case <-time.After(10 * time.Second):
		s.t.Fatalf("%s: no frame", s.who)
		return frame{}
	}
}

func (s *socket) expect(t string) frame {
	s.t.Helper()
	for {
		if f := s.next(); f.T == t {
			return f
		}
	}
}

// quiet is no frame of type t for a moment.
func (s *socket) quiet(t string) {
	s.t.Helper()
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				return
			}
			if f.T == t {
				s.t.Fatalf("%s was sent %s", s.who, f.raw)
			}
		case <-deadline:
			return
		}
	}
}

// soon is how long after the call that caused it a joined or left may reach
// another member's socket (prompt §9: within a second).
const soon = time.Second

// heard checks that a joined or left frame names the player and reached the
// socket within soon of the call returning, logs the time it took, and
// answers the frame. A frame read before the call returned took no time at
// all: the relay was quicker than the HTTP answer.
func heard(t *testing.T, f frame, returned time.Time, what, player string) frame {
	t.Helper()
	if !strings.Contains(string(f.Player), player) {
		t.Fatalf("%s: %s does not name %s", what, f.raw, player)
	}
	took := f.arrived.Sub(returned)
	if took > soon {
		t.Fatalf("time %s: %s reached the socket %v after the call returned; the limit is %v", f.T, what, took, soon)
	}
	if took < 0 {
		t.Logf("time %s: %s reached the socket %v before the call returned (limit %v)",
			f.T, what, (-took).Round(time.Microsecond), soon)
	} else {
		t.Logf("time %s: %s reached the socket %v after the call returned (limit %v)",
			f.T, what, took.Round(time.Microsecond), soon)
	}
	return f
}

// close is the client hanging up.
func (s *socket) close() {
	_ = s.c.Close(websocket.StatusNormalClosure, "")
	<-s.done
}

// closed waits for the server to close the socket.
func (s *socket) closed() (websocket.StatusCode, string) {
	s.t.Helper()
	for {
		select {
		case _, ok := <-s.frames:
			if !ok {
				return s.code, s.reason
			}
		case <-time.After(10 * time.Second):
			s.t.Fatalf("%s was never closed", s.who)
			return 0, ""
		}
	}
}

// verifyTicket checks a room ticket the way a game's server does: the key
// from /.well-known/jwks.json by its kid, ES256, the issuer, and the game's
// own id as the audience; then answers the claims.
func verifyTicket(t *testing.T, raw, game string) jwt.MapClaims {
	t.Helper()
	var jwks struct {
		Keys []struct {
			Kty, Crv, X, Y, Kid, Alg string
		} `json:"keys"`
	}
	newClient(t).call("GET", "/.well-known/jwks.json", nil, 200, &jwks)
	var discovery map[string]any
	newClient(t).call("GET", "/.well-known/openid-configuration", nil, 200, &discovery)

	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(tok *jwt.Token) (any, error) {
		for _, k := range jwks.Keys {
			if k.Kid != tok.Header["kid"] {
				continue
			}
			x, err := base64.RawURLEncoding.DecodeString(k.X)
			if err != nil {
				return nil, err
			}
			y, err := base64.RawURLEncoding.DecodeString(k.Y)
			if err != nil {
				return nil, err
			}
			// The uncompressed point: 0x04, then X and Y at the curve's width.
			return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		}
		return nil, fmt.Errorf("no key %v in the JWKS", tok.Header["kid"])
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer(fmt.Sprint(discovery["issuer"])), jwt.WithAudience(game))
	if err != nil {
		t.Fatalf("the ticket does not verify: %v", err)
	}
	return claims
}
