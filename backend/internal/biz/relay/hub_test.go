package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// The hub's rules, with no network: hello first, then the room's frames in
// one order for everybody, delivered by the rules in docs/ROOMS.md.

func TestHelloComesFirstAndCarriesTheRoom(t *testing.T) {
	h, dir, presence, _ := hub(t, DefaultConfig())
	room, host, friend := newRoom(), newPlayer(), newPlayer()
	dir.add(room, host, enum.HostAuthoritative, enum.StateBlob, friend)

	a := dial(t, h, room, host, 0)
	hello := a.next()
	if hello.T != "hello" || hello.Seq != 0 || hello.Room == nil || hello.Room.ID != room ||
		hello.Room.You == nil || hello.Room.You.Role != "host" {
		t.Fatalf("hello = %+v", hello)
	}
	if string(hello.State) != `{"version":0,"data":null}` {
		t.Fatalf("state = %s", hello.State)
	}
	// Connecting is a members frame, and presence says in the room.
	if m := a.expect("members"); m.Seq != 1 || len(m.List) != 2 {
		t.Fatalf("members = %+v", m)
	}
	if st := presence.of(host); st.State != players.InRoom || st.Room != room {
		t.Fatalf("presence = %+v", st)
	}
	if dir.touched(room) == 0 {
		t.Fatal("connecting did not move active_at")
	}

	// A room without state says so with a null.
	other := newRoom()
	dir.add(other, friend, enum.HostPeer, enum.StateNone)
	b := dial(t, h, other, friend, 0)
	if hello := b.next(); hello.T != "hello" || string(hello.State) != "null" {
		t.Fatalf("hello = %+v state %s", hello, hello.State)
	}
}

// A message without to reaches every member, the sender included, with from
// and seq; one with to reaches that member alone; a to that is not a member
// is refused with the frame's ref.
func TestMessagesAndWhoGetsThem(t *testing.T) {
	h, dir, _, _ := hub(t, DefaultConfig())
	room, host, b, c := newRoom(), newPlayer(), newPlayer(), newPlayer()
	dir.add(room, host, enum.HostPeer, enum.StateBlob, b, c)

	ca, cb, cc := dial(t, h, room, host, 0), dial(t, h, room, b, 0), dial(t, h, room, c, 0)
	for _, cl := range []*client{ca, cb, cc} {
		cl.expect("hello")
	}
	// Let each connect's members frame land everywhere.
	time.Sleep(100 * time.Millisecond)
	for _, cl := range []*client{ca, cb, cc} {
		drain(cl)
	}

	cb.send(map[string]any{"t": "msg", "data": map[string]any{"move": 4}})
	for _, cl := range []*client{ca, cb, cc} {
		m := cl.expect("msg")
		if m.From != b || string(m.Data) != `{"move":4}` || m.Seq == 0 || m.To != "" {
			t.Fatalf("%s got %+v", cl.player, m)
		}
	}

	cb.send(map[string]any{"t": "msg", "to": host, "data": "psst"})
	if m := ca.expect("msg"); m.From != b || m.To != host || string(m.Data) != `"psst"` {
		t.Fatalf("the target got %+v", m)
	}
	cb.quiet()
	cc.quiet()

	cb.send(map[string]any{"t": "msg", "to": newPlayer(), "data": 1, "ref": "r9"})
	if e := cb.next(); e.T != "error" || e.Code != "not_member" || e.Ref != "r9" {
		t.Fatalf("a stranger as to: %+v", e)
	}
	// null is data too; no data at all is a bad frame.
	cb.send(map[string]any{"t": "msg", "data": nil})
	if m := ca.expect("msg"); string(m.Data) != "null" {
		t.Fatalf("null data: %+v", m)
	}
	cb.send(map[string]any{"t": "msg", "ref": "r1"})
	if e := cb.expect("error"); e.Code != "bad_frame" || e.Ref != "r1" {
		t.Fatalf("no data: %+v", e)
	}
	cb.send(map[string]any{"t": "dance", "ref": "r2"})
	if e := cb.expect("error"); e.Code != "bad_frame" || e.Ref != "r2" {
		t.Fatalf("unknown type: %+v", e)
	}
	cb.raw([]byte("not json"))
	if e := cb.expect("error"); e.Code != "bad_frame" {
		t.Fatalf("not JSON: %+v", e)
	}
}

// Everybody sees every frame in the same order, whoever sent it, through
// any number of senders at once.
func TestEveryMemberSeesOneOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rate = 1000
	h, dir, _, _ := hub(t, cfg)
	room, a, b, c := newRoom(), newPlayer(), newPlayer(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob, b, c)
	clients := []*client{dial(t, h, room, a, 0), dial(t, h, room, b, 0), dial(t, h, room, c, 0)}
	for _, cl := range clients {
		cl.expect("hello")
	}
	time.Sleep(100 * time.Millisecond)
	for _, cl := range clients {
		drain(cl)
	}

	const each = 40
	var wg sync.WaitGroup
	for _, cl := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				cl.sock.in <- []byte(fmt.Sprintf(`{"t":"msg","data":%d}`, i))
			}
		}()
	}
	wg.Wait()

	var orders [][]string
	for _, cl := range clients {
		var seen []string
		var last int64
		for len(seen) < each*len(clients) {
			m := cl.expect("msg")
			if last != 0 && m.Seq != last+1 {
				t.Fatalf("%s: seq %d after %d", cl.player, m.Seq, last)
			}
			last = m.Seq
			seen = append(seen, fmt.Sprintf("%d:%s:%s", m.Seq, m.From, m.Data))
		}
		orders = append(orders, seen)
	}
	for i := 1; i < len(orders); i++ {
		if strings.Join(orders[i], ",") != strings.Join(orders[0], ",") {
			t.Fatalf("client %d saw another order", i)
		}
	}
}

// The shared state is a compare-and-set: the right version lands as the next
// one, on everybody's socket with the writer's ref; an old one is refused
// with the current version; only the host writes in an authoritative room;
// nobody writes where there is no state.
func TestTheSharedState(t *testing.T) {
	h, dir, _, _ := hub(t, DefaultConfig())
	room, host, friend := newRoom(), newPlayer(), newPlayer()
	dir.add(room, host, enum.HostAuthoritative, enum.StateBlob, friend)
	ca, cb := dial(t, h, room, host, 0), dial(t, h, room, friend, 0)
	ca.expect("hello")
	cb.expect("hello")

	ca.send(map[string]any{"t": "state", "version": 0, "data": map[string]any{"board": []int{0, 0}}, "ref": "s1"})
	for _, cl := range []*client{ca, cb} {
		s := cl.expect("state")
		if s.Version == nil || *s.Version != 1 || s.By == nil || *s.By != host || s.Ref != "s1" || string(s.Data) != `{"board":[0,0]}` {
			t.Fatalf("%s: %+v", cl.player, s)
		}
	}
	ca.send(map[string]any{"t": "state", "version": 0, "data": 1, "ref": "s2"})
	if e := ca.expect("error"); e.Code != "state_stale" || e.Ref != "s2" || e.Version == nil || *e.Version != 1 {
		t.Fatalf("stale: %+v", e)
	}
	cb.send(map[string]any{"t": "state", "version": 1, "data": 1, "ref": "s3"})
	if e := cb.expect("error"); e.Code != "host_only" || e.Ref != "s3" {
		t.Fatalf("a player writing: %+v", e)
	}
	for _, bad := range []string{`{"t":"state","data":1}`, `{"t":"state","version":-1,"data":1}`,
		`{"t":"state","version":1.5,"data":1}`, `{"t":"state","version":"1","data":1}`, `{"t":"state","version":1}`} {
		ca.raw([]byte(bad))
		if e := ca.expect("error"); e.Code != "bad_frame" {
			t.Fatalf("%s: %+v", bad, e)
		}
	}

	// A reconnect reads the state back in hello.
	got, err := h.State(context.Background(), room)
	if err != nil || got.Version != 1 || string(got.Data) != `{"board":[0,0]}` {
		t.Fatalf("state = %+v, %v", got, err)
	}

	// Peers all write; none where there is no state.
	peers, none := newRoom(), newRoom()
	dir.add(peers, host, enum.HostPeer, enum.StateBlob, friend)
	dir.add(none, friend, enum.HostPeer, enum.StateNone, host)
	cp := dial(t, h, peers, friend, 0)
	cp.expect("hello")
	cp.send(map[string]any{"t": "state", "version": 0, "data": true})
	if s := cp.expect("state"); s.Version == nil || *s.Version != 1 {
		t.Fatalf("a peer writing: %+v", s)
	}
	cn := dial(t, h, none, friend, 0)
	cn.expect("hello")
	cn.send(map[string]any{"t": "state", "version": 0, "data": true, "ref": "n1"})
	if e := cn.expect("error"); e.Code != "no_state" || e.Ref != "n1" {
		t.Fatalf("no state: %+v", e)
	}
}

// Every lifecycle frame the rules publish reaches every socket, in order,
// whatever its shape — joined carries a whole member where the others carry
// an id — and none of them is mistaken for a lost frame.
func TestEveryKindOfFrameIsDelivered(t *testing.T) {
	h, dir, _, _ := hub(t, DefaultConfig())
	room, a, b := newRoom(), newPlayer(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob, b)
	ca, cb := dial(t, h, room, a, 0), dial(t, h, room, b, 0)
	ca.expect("hello")
	cb.expect("hello")
	time.Sleep(100 * time.Millisecond)
	drain(ca)
	drain(cb)

	newcomer := newPlayer()
	by := a
	must(t, h.Publish(context.Background(), room,
		rooms.Joined{T: "joined", Player: rooms.Member{ID: newcomer, Nickname: "زين", Role: "player", Seat: 2}},
		rooms.Members{T: "members", List: []rooms.Member{{ID: a}, {ID: b}, {ID: newcomer}}},
		rooms.HostChanged{T: "host", Player: b},
		rooms.HostChanged{T: "host", Player: a, By: &by},
		rooms.Started{T: "started", At: "2026-10-21T10:00:00Z", By: a},
		rooms.Kicked{T: "kicked", Player: newcomer, By: a},
		rooms.Left{T: "left", Player: newcomer, Reason: "kicked"},
	))
	want := []string{"joined", "members", "host", "host", "started", "kicked", "left"}
	for _, cl := range []*client{ca, cb} {
		var got []string
		var last int64
		for range want {
			f := cl.next()
			if last != 0 && f.Seq != last+1 {
				t.Fatalf("%s: seq %d after %d", cl.player, f.Seq, last)
			}
			last = f.Seq
			got = append(got, f.T)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s got %v", cl.player, got)
		}
	}
	// Still open: a message goes through.
	ca.send(map[string]any{"t": "msg", "data": 1})
	cb.expect("msg")
}

// A reconnect with after gets what it missed, filtered as it would have been
// live; when the log no longer reaches back that far it gets gap instead.
func TestCatchingUpAfterAReconnect(t *testing.T) {
	cfg := DefaultConfig()
	cfg.History = 5
	h, dir, _, _ := hub(t, cfg)
	room, a, b, c := newRoom(), newPlayer(), newPlayer(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob, b, c)
	ctx := context.Background()

	// Three frames: one for everybody, one to b, one to c. c sees seq 1 and 3.
	must(t, h.Publish(ctx, room, rooms.Msg{T: "msg", From: a, Data: json.RawMessage(`1`)}))
	must(t, h.Publish(ctx, room, rooms.Msg{T: "msg", From: a, Data: json.RawMessage(`2`), To: b}))
	must(t, h.Publish(ctx, room, rooms.Msg{T: "msg", From: a, Data: json.RawMessage(`3`), To: c}))

	cc := dial(t, h, room, c, 0) // a first connect: hello, and nothing old
	if hello := cc.next(); hello.T != "hello" || hello.Seq != 3 {
		t.Fatalf("hello = %+v", hello)
	}
	if f := cc.next(); f.T != "members" || f.Seq != 4 {
		t.Fatalf("after hello: %+v", f)
	}

	cb := dial(t, h, room, b, 1)
	if hello := cb.next(); hello.T != "hello" || hello.Seq != 4 {
		t.Fatalf("hello = %+v", hello)
	}
	replay := cb.next()
	if replay.T != "replay" || len(replay.Frames) != 2 {
		t.Fatalf("replay = %+v", replay)
	}
	// 2 was to b, 3 to c and not b's to see, 4 is c's connect; 1 b had.
	var seqs []int64
	for _, raw := range replay.Frames {
		var f frame
		_ = json.Unmarshal(raw, &f)
		seqs = append(seqs, f.Seq)
	}
	if fmt.Sprint(seqs) != "[2 4]" {
		t.Fatalf("replayed %v", seqs)
	}
	// Live frames carry on from S: b's own connect.
	if f := cb.next(); f.T != "members" || f.Seq != 5 {
		t.Fatalf("after the replay: %+v", f)
	}

	// Past the log's reach: gap.
	for i := range 8 {
		must(t, h.Publish(ctx, room, rooms.Msg{T: "msg", From: a, Data: json.RawMessage(fmt.Sprint(i))}))
	}
	ca := dial(t, h, room, a, 2)
	if hello := ca.next(); hello.T != "hello" {
		t.Fatalf("hello = %+v", hello)
	}
	if gap := ca.next(); gap.T != "error" || gap.Code != "gap" {
		t.Fatalf("gap = %+v", gap)
	}
}

// Frames that arrive while hello is being built are held and sent after it,
// in order, none twice.
func TestFramesDuringHelloAreHeld(t *testing.T) {
	h, dir, _, _ := hub(t, DefaultConfig())
	room, a, b := newRoom(), newPlayer(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob, b)
	ctx := context.Background()

	ca := dial(t, h, room, a, 0)
	ca.expect("hello")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = h.Publish(ctx, room, rooms.Msg{T: "msg", From: a, Data: json.RawMessage(fmt.Sprint(i))})
			time.Sleep(time.Millisecond)
		}
	}()
	cb := dial(t, h, room, b, 0)
	hello := cb.next()
	if hello.T != "hello" {
		t.Fatalf("hello = %+v", hello)
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	<-done

	last := hello.Seq
	for {
		select {
		case raw := <-cb.sock.out:
			var f frame
			_ = json.Unmarshal(raw, &f)
			if f.Seq != last+1 {
				t.Fatalf("seq %d after %d", f.Seq, last)
			}
			last = f.Seq
		case <-time.After(300 * time.Millisecond):
			if last == hello.Seq {
				t.Fatal("nothing after hello")
			}
			return
		}
	}
}

// A round's start resets the shared state and publishes started in one step:
// every member hears the state's last write, then started, in one order; a
// write based on the last round's version is refused with version 0; the
// first write of the round lands as version 1; and a socket connecting now
// is told version 0 with no data. A finished frame closes nobody's socket.
func TestAStartResetsTheState(t *testing.T) {
	h, dir, _, _ := hub(t, DefaultConfig())
	room, host, friend := newRoom(), newPlayer(), newPlayer()
	dir.add(room, host, enum.HostPeer, enum.StateBlob, friend)
	ctx := context.Background()
	ca, cb := dial(t, h, room, host, 0), dial(t, h, room, friend, 0)
	ca.expect("hello")
	cb.expect("hello")

	ca.send(map[string]any{"t": "state", "version": 0, "data": "a"})
	ca.send(map[string]any{"t": "state", "version": 1, "data": "b"})
	for _, cl := range []*client{ca, cb} {
		if s := cl.expect("state"); *s.Version != 1 {
			t.Fatalf("%s: %+v", cl.player, s)
		}
		if s := cl.expect("state"); *s.Version != 2 {
			t.Fatalf("%s: %+v", cl.player, s)
		}
	}

	by := host
	must(t, h.Publish(ctx, room, rooms.Finished{T: "finished", At: "2026-10-21T10:00:00Z", By: &by, Round: 1, Reason: "host"}))
	must(t, h.Start(ctx, room, rooms.Started{T: "started", At: "2026-10-21T10:01:00Z", By: host, Round: 2}))
	var seqs []int64
	for _, cl := range []*client{ca, cb} {
		fin := cl.expect("finished")
		st := cl.expect("started")
		if st.Seq != fin.Seq+1 || st.By == nil || *st.By != host || st.At != "2026-10-21T10:01:00Z" {
			t.Fatalf("%s: finished %+v, started %+v", cl.player, fin, st)
		}
		seqs = append(seqs, st.Seq)
	}
	if seqs[0] != seqs[1] {
		t.Fatalf("two orders: %v", seqs)
	}
	got, err := h.State(ctx, room)
	if err != nil || got.Version != 0 || got.Data != nil {
		t.Fatalf("the state after the start = %+v, %v", got, err)
	}

	cb.send(map[string]any{"t": "state", "version": 2, "data": "old", "ref": "o1"})
	if e := cb.expect("error"); e.Code != "state_stale" || e.Version == nil || *e.Version != 0 || e.Ref != "o1" {
		t.Fatalf("a write from the last round: %+v", e)
	}
	late := dial(t, h, room, newPlayerIn(dir, room), 0)
	if hello := late.expect("hello"); string(hello.State) != `{"version":0,"data":null}` {
		t.Fatalf("hello after the start: %s", hello.State)
	}
	cb.send(map[string]any{"t": "state", "version": 0, "data": "c"})
	for _, cl := range []*client{ca, cb} {
		if s := cl.expect("state"); *s.Version != 1 || string(s.Data) != `"c"` {
			t.Fatalf("%s: the round's first write = %+v", cl.player, s)
		}
	}
	ca.send(map[string]any{"t": "msg", "data": 1})
	cb.expect("msg")
}

// newPlayerIn seats a new member in a fake room.
func newPlayerIn(dir *fakeDirectory, roomID string) string {
	p := newPlayer()
	dir.mu.Lock()
	defer dir.mu.Unlock()
	r := dir.rooms[roomID]
	r.seats[p] = len(r.seats)
	return p
}

// A frame over the limit is refused and the socket stays; so is one over the
// rate. Ping is answered and keeps the member present.
func TestLimitsAndPings(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxFrame, cfg.Rate = 64, 5
	h, dir, presence, _ := hub(t, cfg)
	room, a := newRoom(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob)
	ca := dial(t, h, room, a, 0)
	ca.expect("hello")
	ca.expect("members")

	ca.raw([]byte(`{"t":"msg","data":"` + strings.Repeat("x", 100) + `"}`))
	if e := ca.next(); e.T != "error" || e.Code != "too_large" {
		t.Fatalf("too large: %+v", e)
	}
	ca.send(map[string]any{"t": "ping"})
	if p := ca.next(); p.T != "pong" || p.At == "" {
		t.Fatalf("pong = %+v", p)
	}
	if presence.count(a+":"+players.InRoom) < 2 {
		t.Fatal("a ping did not refresh presence")
	}

	for i := range 10 {
		ca.send(map[string]any{"t": "state", "version": 99, "data": i, "ref": fmt.Sprint("q", i)})
	}
	limited := 0
	for range 10 {
		if e := ca.expect("error"); e.Code == "rate_limited" {
			limited++
			if !strings.HasPrefix(e.Ref, "q") {
				t.Fatalf("a rate-limited frame lost its ref: %+v", e)
			}
		}
	}
	if limited == 0 {
		t.Fatal("ten frames at once were all let through a rate of five")
	}
}

// Silence closes a socket with 1001, which the app reconnects from.
func TestASilentSocketIsClosed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Idle = 200 * time.Millisecond
	h, dir, presence, _ := hub(t, cfg)
	room, a := newRoom(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob)
	ca := dial(t, h, room, a, 0)
	ca.expect("hello")
	if code, reason := ca.closed(); code != 1001 || reason != "going_away" {
		t.Fatalf("closed %d %q", code, reason)
	}
	<-ca.done
	if st := presence.of(a); st.State != players.Online {
		t.Fatalf("presence after the close = %+v", st)
	}
}

// One socket per member: a second connect replaces the first with 4000, and
// the replaced one does not touch presence on its way out.
func TestASecondSocketReplacesTheFirst(t *testing.T) {
	h, dir, presence, _ := hub(t, DefaultConfig())
	room, a := newRoom(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob)

	first := dial(t, h, room, a, 0)
	first.expect("hello")
	second := dial(t, h, room, a, 0)
	second.expect("hello")
	if code, reason := first.closed(); code != 4000 || reason != "replaced" {
		t.Fatalf("first closed %d %q", code, reason)
	}
	<-first.done
	if st := presence.of(a); st.State != players.InRoom {
		t.Fatalf("the replaced socket touched presence: %+v", st)
	}
	second.send(map[string]any{"t": "msg", "data": 1})
	second.expect("msg")
}

// Two hubs are two replicas: a socket on one replaces the member's socket on
// the other.
func TestReplacementCrossesReplicas(t *testing.T) {
	h1, dir, _, rdb := hub(t, DefaultConfig())
	h2 := NewHub(rdb, dir, &fakePresence{}, DefaultConfig(), nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h2.Close(ctx)
	})
	room, a, b := newRoom(), newPlayer(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob, b)

	first := dial(t, h1, room, a, 0)
	first.expect("hello")
	watcher := dial(t, h1, room, b, 0)
	watcher.expect("hello")
	second := dial(t, h2, room, a, 0)
	second.expect("hello")
	if code, _ := first.closed(); code != 4000 {
		t.Fatalf("first closed %d", code)
	}

	// And frames cross too: what the member says on h2 reaches b on h1.
	drain(watcher)
	second.send(map[string]any{"t": "msg", "data": "hi"})
	if m := watcher.expect("msg"); m.From != a || string(m.Data) != `"hi"` {
		t.Fatalf("across replicas: %+v", m)
	}
}

// The hubs act on the frames they deliver: a left closes that member's socket
// with 4003 and its reason, after the frame itself; an ended closes every
// socket with 4004 and the room's keys go.
func TestLeftAndEndedCloseSockets(t *testing.T) {
	h, dir, presence, rdb := hub(t, DefaultConfig())
	room, a, b, c := newRoom(), newPlayer(), newPlayer(), newPlayer()
	dir.add(room, a, enum.HostAuthoritative, enum.StateBlob, b, c)
	ctx := context.Background()
	ca, cb, cc := dial(t, h, room, a, 0), dial(t, h, room, b, 0), dial(t, h, room, c, 0)
	for _, cl := range []*client{ca, cb, cc} {
		cl.expect("hello")
	}

	dir.remove(room, c, true)
	by := a
	must(t, h.Publish(ctx, room,
		rooms.Kicked{T: "kicked", Player: c, By: a},
		rooms.Left{T: "left", Player: c, Reason: "kicked"}))
	cc.expect("kicked")
	if l := cc.expect("left"); l.Reason != "kicked" {
		t.Fatalf("left = %+v", l)
	}
	if code, reason := cc.closed(); code != 4003 || reason != "kicked" {
		t.Fatalf("kicked closed %d %q", code, reason)
	}
	<-cc.done
	if st := presence.of(c); st.State != players.Online {
		t.Fatalf("presence = %+v", st)
	}

	ca.send(map[string]any{"t": "state", "version": 0, "data": 1})
	ca.expect("state")
	dir.end(room)
	must(t, h.End(ctx, room, rooms.Ended{T: "ended", At: "2026-10-21T10:00:00Z", By: &by, Reason: "host", Results: nil}))
	for _, cl := range []*client{ca, cb} {
		if e := cl.expect("ended"); e.Reason != "host" || e.By == nil || *e.By != a {
			t.Fatalf("ended = %+v", e)
		}
		if code, reason := cl.closed(); code != 4004 || reason != "ended" {
			t.Fatalf("%s closed %d %q", cl.player, code, reason)
		}
	}
	n, err := rdb.Exists(ctx, seqKey(room), logKey(room), verKey(room), dataKey(room)).Result()
	if err != nil || n != 0 {
		t.Fatalf("%d room keys left after the end, %v", n, err)
	}
}

// A socket that cannot keep up is closed with 4008, after the error saying
// so, rather than being allowed to hold the room up.
func TestASlowConsumerIsClosed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Queue, cfg.Rate = 8, 10000
	h, dir, _, _ := hub(t, cfg)
	room, a, b := newRoom(), newPlayer(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob, b)

	fast := dial(t, h, room, a, 0)
	fast.expect("hello")
	sock := newSocket()
	sock.delay = 20 * time.Millisecond
	slow := dialWith(t, h, room, b, 0, sock)
	for i := range 100 {
		fast.raw([]byte(fmt.Sprintf(`{"t":"msg","data":%d}`, i)))
	}
	code, reason := slow.closed()
	if code != 4008 || reason != "slow_consumer" {
		t.Fatalf("closed %d %q", code, reason)
	}
	var lastErr string
	for {
		select {
		case raw := <-slow.sock.out:
			var f frame
			_ = json.Unmarshal(raw, &f)
			if f.T == "error" {
				lastErr = f.Code
			}
			continue
		default:
		}
		break
	}
	if lastErr != "slow_consumer" {
		t.Fatalf("the last error was %q", lastErr)
	}
}

// The keys a room's scripts touch share one hash tag, so a cluster puts them
// in one slot.
func TestARoomsKeysShareASlot(t *testing.T) {
	room := "room_7f3k9d2m8q1x4b6n"
	for _, k := range append(keys(room), touchKey(room), channel(room)) {
		if !strings.Contains(k, "{"+room+"}") {
			t.Fatalf("%s has no hash tag", k)
		}
	}
	if roomOf(channel(room)) != room {
		t.Fatal("a channel does not read back as its room")
	}
	head, tail, err := split(rooms.Started{T: "started", At: "x", By: "y", Round: 2})
	if err != nil || head != `{"t":"started"` || tail != `,"at":"x","by":"y","round":2}` {
		t.Fatalf("split = %q %q %v", head, tail, err)
	}
	if _, _, err := split(map[string]int{"seq": 1}); err == nil {
		t.Fatal("a frame without its type first was split")
	}
}

func drain(c *client) {
	for {
		select {
		case <-c.sock.out:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A stopping process closes every socket with 1001 — the app reconnects to
// another replica — leaves presence alone, and opens no new one.
func TestClosingTheHubClosesItsSockets(t *testing.T) {
	rdb := redisForTest(t)
	dir, presence := &fakeDirectory{}, &fakePresence{}
	h := NewHub(rdb, dir, presence, DefaultConfig(), nil)
	room, a := newRoom(), newPlayer()
	dir.add(room, a, enum.HostPeer, enum.StateBlob)
	ca := dial(t, h, room, a, 0)
	ca.expect("hello")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Close(ctx)
	if code, reason := ca.closed(); code != 1001 || reason != "going_away" {
		t.Fatalf("closed %d %q", code, reason)
	}
	<-ca.done
	if st := presence.of(a); st.State != players.InRoom {
		t.Fatalf("a shutdown touched presence: %+v", st)
	}

	late := dial(t, h, room, a, 0)
	if code, _ := late.closed(); code != 1001 {
		t.Fatalf("a socket after the close got %d", code)
	}
}
