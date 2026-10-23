package ws_test

// The relay through a real socket: the router the api serves, the rooms'
// rules over a real database, the hub over the real Redis, and clients that
// are coder/websocket dialling an httptest server. Skips without
// TEST_DATABASE_URL and TEST_REDIS_URL.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/relay"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	apphttp "github.com/jamaa/gathering/backend/internal/http"
	"github.com/jamaa/gathering/backend/internal/http/ws"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

const (
	xo  = "fixture.xo"  // two seats, the host referees, a blob of state
	tap = "fixture.tap" // up to eight, peers, no state
)

type env struct {
	t        *testing.T
	db       *gorm.DB
	rdb      *data.Redis
	srv      *httptest.Server
	rules    *rooms.Service
	hub      *relay.Hub
	minter   identity.Minter
	list     identity.Whitelist
	presence *players.Presence
}

func newEnv(t *testing.T, cfg relay.Config) *env {
	t.Helper()
	if os.Getenv("TEST_REDIS_URL") == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	db := biztest.DB(t)
	rdb, err := data.OpenRedis(context.Background(), os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	gameOf := func(name string, max int, host enum.HostMode, state enum.StateMode) model.Manifest {
		m := model.Manifest{Name: name, Version: "1.0.0", Entry: "https://games.yourstudio.example/" + name + "/"}
		m.Publisher.Tier = enum.TierPartner
		m.Room.Players.Min, m.Room.Players.Max = 2, max
		m.Room.Host, m.Room.Pace, m.Room.State, m.Room.Rejoin, m.Room.LAN = host, enum.PaceTurn, state, true, true
		return m
	}
	biztest.SeedGame(t, db, xo, gameOf("إكس أو", 2, enum.HostAuthoritative, enum.StateBlob))
	biztest.SeedGame(t, db, tap, gameOf("سباق النقر", 8, enum.HostPeer, enum.StateNone))

	signer := biztest.Signer(t)
	e := &env{
		t: t, db: db, rdb: rdb, minter: identity.NewMinter(signer), list: identity.NewWhitelist(rdb),
		presence: players.NewPresence(rdb, time.Minute),
	}
	e.rules = rooms.New(db, rooms.Config{
		Alphabet: "ABCDEFGHJKLMNPQRSTUVWXYZ23456789", MaxMembers: 64,
		TicketTTL: 10 * time.Minute, LinkURL: "http://gathering.test", AvatarTTL: time.Hour,
	})
	e.rules.Presence, e.rules.Minter = e.presence, e.minter
	e.hub = relay.NewHub(rdb, e.rules, e.presence, cfg, nil)
	e.rules.Publisher = e.hub

	r, api := apphttp.New("gathering", "test", applog.New("gathering"))
	apphttp.Mount(api, apphttp.Deps{
		Resolve:   (&identity.Identities{Signer: signer, Whitelist: e.list}).Resolve,
		Rooms:     e.rules,
		Relay:     e.hub,
		PublicURL: "http://gathering.test",
	})
	e.srv = httptest.NewServer(r)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.hub.Close(ctx)
		e.srv.Close()
	})
	return e
}

// member is a signed-in player: a session token listed like any other.
type member struct {
	id    string
	token string
}

func (e *env) player(nickname string) *member {
	e.t.Helper()
	p := biztest.SeedPlayer(e.t, e.db, "", nickname)
	sid := ids.New(ids.Session)
	tok, jti, err := e.minter.MintSession(p.ID, sid, true, time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.list.Allow(context.Background(), jti, sid, time.Hour); err != nil {
		e.t.Fatal(err)
	}
	return &member{id: p.ID, token: tok}
}

// call is one HTTP call as m; a status other than want fails the test.
func (e *env) call(m *member, method, path string, body any, want int, out any) {
	e.t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		e.t.Fatalf("%s %s: %d, want %d: %s", method, path, res.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
}

type roomJSON struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// room is a room for game made by host, with the others joined by code.
func (e *env) room(game string, host *member, others ...*member) roomJSON {
	e.t.Helper()
	var r roomJSON
	e.call(host, "POST", "/v1/rooms", map[string]any{"game": game}, 201, &r)
	for _, o := range others {
		e.call(o, "POST", "/v1/rooms/join", map[string]any{"code": r.Code}, 200, nil)
	}
	return r
}

func (e *env) socketURL(roomID string, after int64) string {
	u := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/v1/rooms/" + roomID + "/socket"
	if after > 0 {
		u += fmt.Sprintf("?after=%d", after)
	}
	return u
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
	List    []rooms.Member    `json:"list"`
	Frames  []json.RawMessage `json:"frames"`
	Room    json.RawMessage   `json:"room"`
	State   json.RawMessage   `json:"state"`
	Results []model.Score     `json:"results"`
	At      string            `json:"at"`
	Round   int               `json:"round"`
	raw     []byte
}

// sock is one client's socket, read by a goroutine of its own.
type sock struct {
	t      *testing.T
	who    string
	c      *websocket.Conn
	frames chan frame
	done   chan struct{}

	mu     sync.Mutex
	code   websocket.StatusCode
	reason string
}

// connect opens a socket as m: the bearer in the header, as the app does, or
// as a subprotocol, as a browser does.
func (e *env) connect(m *member, roomID string, after int64, asBrowser bool) *sock {
	e.t.Helper()
	opts := &websocket.DialOptions{Subprotocols: []string{ws.Subprotocol}}
	if asBrowser {
		opts.Subprotocols = append(opts.Subprotocols, "bearer."+m.token)
	} else {
		opts.HTTPHeader = http.Header{"Authorization": {"Bearer " + m.token}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, res, err := websocket.Dial(ctx, e.socketURL(roomID, after), opts)
	if err != nil {
		e.t.Fatalf("dial: %v (%v)", err, res)
	}
	if c.Subprotocol() != ws.Subprotocol {
		e.t.Fatalf("selected %q", c.Subprotocol())
	}
	c.SetReadLimit(1 << 20)
	s := &sock{t: e.t, who: m.id, c: c, frames: make(chan frame, 4096), done: make(chan struct{})}
	go s.read()
	e.t.Cleanup(func() {
		_ = c.CloseNow()
		<-s.done
	})
	return s
}

func (s *sock) read() {
	defer close(s.done)
	defer close(s.frames)
	for {
		_, b, err := s.c.Read(context.Background())
		if err != nil {
			var ce websocket.CloseError
			s.mu.Lock()
			if errors.As(err, &ce) {
				s.code, s.reason = ce.Code, ce.Reason
			} else {
				s.code = -1
			}
			s.mu.Unlock()
			return
		}
		var f frame
		if json.Unmarshal(b, &f) == nil {
			f.raw = b
			s.frames <- f
		}
	}
}

func (s *sock) send(v any) {
	s.t.Helper()
	b, _ := json.Marshal(v)
	if err := s.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		s.t.Fatalf("%s send: %v", s.who, err)
	}
}

// next is the next frame, failing after a while without one.
func (s *sock) next() frame {
	s.t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			s.t.Fatalf("%s: closed while waiting for a frame", s.who)
		}
		return f
	case <-time.After(3 * time.Second):
		s.t.Fatalf("%s: no frame", s.who)
		return frame{}
	}
}

// expect skips frames until one of type t.
func (s *sock) expect(t string) frame {
	s.t.Helper()
	for {
		if f := s.next(); f.T == t {
			return f
		}
	}
}

// quiet is nothing more of type t for a moment.
func (s *sock) quiet(t string) {
	s.t.Helper()
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case f, ok := <-s.frames:
			if ok && f.T == t {
				s.t.Fatalf("%s was sent %s", s.who, f.raw)
			}
			if !ok {
				return
			}
		case <-deadline:
			return
		}
	}
}

// closed waits for the server's close and answers it.
func (s *sock) closed() (websocket.StatusCode, string) {
	s.t.Helper()
	for {
		select {
		case _, ok := <-s.frames:
			if ok {
				continue
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.code, s.reason
		case <-time.After(5 * time.Second):
			s.t.Fatalf("%s was never closed", s.who)
			return 0, ""
		}
	}
}

// refusal dials and expects the plain HTTP answer before any upgrade.
func (e *env) refusal(u string, opts *websocket.DialOptions, want int, code string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, res, err := websocket.Dial(ctx, u, opts)
	if err == nil {
		_ = c.CloseNow()
		e.t.Fatalf("%s upgraded, want %d %s", u, want, code)
	}
	if res == nil || res.StatusCode != want {
		e.t.Fatalf("%s: %v, want %d %s", u, res, want, code)
	}
	body, _ := io.ReadAll(res.Body)
	var answer struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &answer) != nil || answer.Error != code {
		e.t.Fatalf("%s: %d %s, want %s", u, res.StatusCode, body, code)
	}
}

// Every refusal is a plain HTTP answer, before anything is upgraded.
func TestTheDoorAnswersInPlainHTTP(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	host, friend, stranger, kicked := e.player("Zayn"), e.player("Sara"), e.player("Ali"), e.player("Huda")
	r := e.room(tap, host, friend, kicked)
	u := e.socketURL(r.ID, 0)
	bearer := func(m *member) *websocket.DialOptions {
		return &websocket.DialOptions{
			Subprotocols: []string{ws.Subprotocol},
			HTTPHeader:   http.Header{"Authorization": {"Bearer " + m.token}},
		}
	}

	// Not a WebSocket at all.
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/rooms/"+r.ID+"/socket", nil)
	req.Header.Set("Authorization", "Bearer "+host.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("a plain GET: %v %v", res, err)
	}
	res.Body.Close()

	e.refusal(u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + host.token}}}, 400, "subprotocol_required")
	e.refusal(u, &websocket.DialOptions{Subprotocols: []string{"chat"}, HTTPHeader: bearer(host).HTTPHeader}, 400, "subprotocol_required")
	e.refusal(u, &websocket.DialOptions{Subprotocols: []string{ws.Subprotocol}}, 401, "missing_token")
	e.refusal(u, &websocket.DialOptions{Subprotocols: []string{ws.Subprotocol, "bearer.nonsense"}}, 401, "invalid_token")
	e.refusal(u, bearer(stranger), 404, "not_found")
	e.refusal(e.socketURL("room_aaaaaaaaaaaaaaaa", 0), bearer(host), 404, "not_found")
	e.refusal(e.socketURL("nonsense", 0), bearer(host), 404, "not_found")
	e.refusal(u+"?after=-3", bearer(host), 422, "invalid_fields")

	e.call(host, "POST", "/v1/rooms/"+r.ID+"/kick", map[string]any{"player": kicked.id}, 204, nil)
	e.refusal(u, bearer(kicked), 403, "kicked")
	e.call(friend, "POST", "/v1/rooms/"+r.ID+"/leave", nil, 204, nil)
	e.refusal(u, bearer(friend), 404, "not_found")
	e.call(host, "POST", "/v1/rooms/"+r.ID+"/end", nil, 200, nil)
	e.refusal(u, bearer(host), 409, "room_closed")

	// A room ticket is a token of ours that opens nothing here.
	r2 := e.room(xo, stranger)
	var ticket struct {
		Token string `json:"token"`
	}
	e.call(stranger, "GET", "/v1/rooms/"+r2.ID+"/ticket", nil, 200, &ticket)
	e.refusal(e.socketURL(r2.ID, 0), &websocket.DialOptions{
		Subprotocols: []string{ws.Subprotocol}, HTTPHeader: http.Header{"Authorization": {"Bearer " + ticket.Token}},
	}, 403, "wrong_token")
}

// hello is the first frame, the Room exactly as HTTP answers it with you
// filled, the seq, and the state. A browser's bearer rides as a subprotocol.
func TestHelloOverTheSocket(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	host, friend := e.player("Zayn"), e.player("Sara")
	var r roomJSON
	e.call(host, "POST", "/v1/rooms", map[string]any{"game": xo}, 201, &r)
	typed := strings.ToLower(r.Code[:3] + "-" + r.Code[3:])
	e.call(friend, "POST", "/v1/rooms/join", map[string]any{"code": typed}, 200, nil)

	a := e.connect(host, r.ID, 0, false)
	hello := a.next()
	if hello.T != "hello" || hello.Seq < 2 {
		t.Fatalf("hello = %s", hello.raw)
	}
	var room map[string]any
	if err := json.Unmarshal(hello.Room, &room); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "code", "game", "status", "round", "visibility", "lan", "host", "config", "members",
		"you", "state_version", "link", "end_reason", "results", "created_at", "started_at", "ended_at"} {
		if _, ok := room[key]; !ok {
			t.Errorf("hello's room has no %s: %s", key, hello.Room)
		}
	}
	if room["id"] != r.ID || room["status"] != "lobby" || room["link"] != "http://gathering.test/r/"+r.Code {
		t.Fatalf("room = %v", room)
	}
	if you := room["you"].(map[string]any); you["role"] != "host" || you["seat"] != float64(0) {
		t.Fatalf("you = %v", you)
	}
	members := room["members"].([]any)
	if len(members) != 2 || members[0].(map[string]any)["connected"] != true {
		t.Fatalf("members = %v", members)
	}
	cfg := room["config"].(map[string]any)
	if cfg["min"] != float64(2) || cfg["host"] != "authoritative" || cfg["state"] != "blob" {
		t.Fatalf("config = %v", cfg)
	}
	if string(hello.State) != `{"version":0,"data":null}` {
		t.Fatalf("state = %s", hello.State)
	}

	// The same room over HTTP, as the same member.
	var viaHTTP map[string]any
	e.call(host, "GET", "/v1/rooms/"+r.ID, nil, 200, &viaHTTP)
	if viaHTTP["code"] != room["code"] || len(viaHTTP) != len(room) {
		t.Fatalf("HTTP and hello disagree:\n%v\n%v", viaHTTP, room)
	}

	b := e.connect(friend, r.ID, 0, true)
	if h := b.next(); h.T != "hello" {
		t.Fatalf("the browser's hello = %s", h.raw)
	}
	// Both hear the browser connect.
	for _, s := range []*sock{a, b} {
		m := s.expect("members")
		for len(m.List) == 2 && (!m.List[0].Connected || !m.List[1].Connected) {
			m = s.expect("members")
		}
	}
}

// Every client sees every frame in one order, whoever sent it.
func TestOneOrderForThreeClients(t *testing.T) {
	cfg := relay.DefaultConfig()
	cfg.Rate = 1000
	e := newEnv(t, cfg)
	a, b, c := e.player("Zayn"), e.player("Sara"), e.player("Ali")
	r := e.room(tap, a, b, c)
	socks := []*sock{e.connect(a, r.ID, 0, false), e.connect(b, r.ID, 0, false), e.connect(c, r.ID, 0, false)}
	for _, s := range socks {
		s.expect("hello")
	}

	const each = 30
	var wg sync.WaitGroup
	for i, s := range socks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range each {
				b, _ := json.Marshal(map[string]any{"t": "msg", "data": fmt.Sprintf("%d-%d", i, n)})
				_ = s.c.Write(context.Background(), websocket.MessageText, b)
			}
		}()
	}
	wg.Wait()

	var orders []string
	for _, s := range socks {
		var seen []string
		var last int64
		for len(seen) < each*len(socks) {
			f := s.next()
			if f.T == "hello" || f.T == "members" {
				last = max(last, f.Seq)
				continue
			}
			if f.T != "msg" {
				t.Fatalf("%s got %s", s.who, f.raw)
			}
			if f.Seq <= last {
				t.Fatalf("%s: seq %d after %d", s.who, f.Seq, last)
			}
			last = f.Seq
			seen = append(seen, fmt.Sprintf("%d %s %s", f.Seq, f.From, f.Data))
		}
		orders = append(orders, strings.Join(seen, ","))
	}
	if orders[0] != orders[1] || orders[1] != orders[2] {
		t.Fatal("the clients saw different orders")
	}
}

// A message without to comes back to its sender too; one with to reaches its
// target alone; a to who is not a member is refused.
func TestBroadcastsEchoAndToIsPrivate(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	a, b, c := e.player("Zayn"), e.player("Sara"), e.player("Ali")
	r := e.room(tap, a, b, c)
	sa, sb, sc := e.connect(a, r.ID, 0, false), e.connect(b, r.ID, 0, false), e.connect(c, r.ID, 0, false)
	for _, s := range []*sock{sa, sb, sc} {
		s.expect("hello")
	}

	sb.send(map[string]any{"t": "msg", "data": map[string]any{"cell": 4}})
	for _, s := range []*sock{sa, sb, sc} {
		if m := s.expect("msg"); m.From != b.id || string(m.Data) != `{"cell":4}` || m.Seq == 0 {
			t.Fatalf("%s got %s", s.who, m.raw)
		}
	}
	sb.send(map[string]any{"t": "msg", "to": a.id, "data": "psst"})
	if m := sa.expect("msg"); m.To != a.id || m.From != b.id {
		t.Fatalf("the target got %s", m.raw)
	}
	sb.quiet("msg")
	sc.quiet("msg")
	sb.send(map[string]any{"t": "msg", "to": "ply_aaaaaaaaaaaaaaaa", "data": 1, "ref": "x"})
	if f := sb.expect("error"); f.Code != "not_member" || f.Ref != "x" {
		t.Fatalf("to a stranger: %s", f.raw)
	}
}

// A reconnect with after gets what it missed, as it would have seen it
// live; past the log's reach it gets gap.
func TestReplayAndGapAfterAReconnect(t *testing.T) {
	cfg := relay.DefaultConfig()
	cfg.History = 6
	e := newEnv(t, cfg)
	a, b, c := e.player("Zayn"), e.player("Sara"), e.player("Ali")
	r := e.room(tap, a, b, c)
	sa := e.connect(a, r.ID, 0, false)
	sa.expect("hello")
	sb := e.connect(b, r.ID, 0, false)
	sb.expect("hello")

	sa.send(map[string]any{"t": "msg", "data": "one"})
	last := sb.expect("msg").Seq
	_ = sb.c.Close(websocket.StatusNormalClosure, "")
	<-sb.done

	sa.send(map[string]any{"t": "msg", "data": "two"})
	sa.send(map[string]any{"t": "msg", "to": c.id, "data": "not for b"})
	sa.send(map[string]any{"t": "msg", "to": b.id, "data": "for b"})
	// A socket's frames are handled in order: the pong says the three
	// before it are in the history.
	sa.send(map[string]any{"t": "ping"})
	sa.expect("pong")

	back := e.connect(b, r.ID, last, false)
	hello := back.next()
	replay := back.next()
	if hello.T != "hello" || replay.T != "replay" {
		t.Fatalf("got %s then %s", hello.raw, replay.raw)
	}
	var got []string
	for _, raw := range replay.Frames {
		var f frame
		_ = json.Unmarshal(raw, &f)
		if f.Seq <= last || f.Seq > hello.Seq {
			t.Fatalf("replayed seq %d outside (%d, %d]", f.Seq, last, hello.Seq)
		}
		if f.T == "msg" {
			got = append(got, string(f.Data))
		}
	}
	if strings.Join(got, ",") != `"two","for b"` {
		t.Fatalf("replayed %v", got)
	}

	// Far behind: the log holds six, so after=1 no longer reaches.
	for i := range 8 {
		sa.send(map[string]any{"t": "msg", "data": i})
	}
	for range 8 {
		sa.expect("msg")
	}
	_ = back.c.Close(websocket.StatusNormalClosure, "")
	<-back.done
	far := e.connect(b, r.ID, 1, false)
	far.expect("hello")
	if f := far.next(); f.T != "error" || f.Code != "gap" {
		t.Fatalf("far behind: %s", f.raw)
	}
}

// The state over the socket: the ack is the state frame with the writer's
// ref; an old version is state_stale with the current one; a player cannot
// write in an authoritative room; nobody can where there is no state.
func TestStateOverTheSocket(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	host, friend := e.player("Zayn"), e.player("Sara")
	r := e.room(xo, host, friend)
	sa, sb := e.connect(host, r.ID, 0, false), e.connect(friend, r.ID, 0, false)
	sa.expect("hello")
	sb.expect("hello")

	sa.send(map[string]any{"t": "state", "version": 0, "data": map[string]any{"board": "x--------"}, "ref": "w1"})
	for _, s := range []*sock{sa, sb} {
		st := s.expect("state")
		if st.Version == nil || *st.Version != 1 || st.Ref != "w1" || st.By == nil || *st.By != host.id {
			t.Fatalf("%s: %s", s.who, st.raw)
		}
	}
	sa.send(map[string]any{"t": "state", "version": 0, "data": 2, "ref": "w2"})
	if f := sa.expect("error"); f.Code != "state_stale" || f.Version == nil || *f.Version != 1 || f.Ref != "w2" {
		t.Fatalf("stale: %s", f.raw)
	}
	sb.send(map[string]any{"t": "state", "version": 1, "data": 3, "ref": "w3"})
	if f := sb.expect("error"); f.Code != "host_only" || f.Ref != "w3" {
		t.Fatalf("a player writing: %s", f.raw)
	}
	var room struct {
		StateVersion int `json:"state_version"`
	}
	e.call(friend, "GET", "/v1/rooms/"+r.ID, nil, 200, &room)
	if room.StateVersion != 1 {
		t.Fatalf("state_version = %d", room.StateVersion)
	}

	other, third := e.player("Ali"), e.player("Huda")
	r2 := e.room(tap, other, third)
	sc := e.connect(other, r2.ID, 0, false)
	sc.expect("hello")
	sc.send(map[string]any{"t": "state", "version": 0, "data": 1, "ref": "w4"})
	if f := sc.expect("error"); f.Code != "no_state" || f.Ref != "w4" {
		t.Fatalf("no state: %s", f.raw)
	}
}

// Over the limit is refused with the socket kept; over the rate is refused
// frame by frame.
func TestTooLargeAndTooFast(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	host, friend := e.player("Zayn"), e.player("Sara")
	r := e.room(tap, host, friend)
	sa := e.connect(host, r.ID, 0, false)
	sa.expect("hello")

	sa.send(map[string]any{"t": "msg", "data": strings.Repeat("x", 17<<10)})
	if f := sa.expect("error"); f.Code != "too_large" {
		t.Fatalf("too large: %s", f.raw)
	}
	sa.send(map[string]any{"t": "ping"})
	if p := sa.expect("pong"); p.At == "" {
		t.Fatalf("pong = %s", p.raw)
	}

	for i := range 60 {
		sa.send(map[string]any{"t": "ping", "ref": fmt.Sprint(i)})
	}
	limited := 0
	deadline := time.After(2 * time.Second)
	for limited == 0 {
		select {
		case f := <-sa.frames:
			if f.T == "error" && f.Code == "rate_limited" {
				limited++
			}
		case <-deadline:
			t.Fatal("sixty frames at once were all let through")
		}
	}
}

// A second socket for the same member replaces the first with 4000.
func TestASecondSocketReplacesTheFirst(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	host, friend := e.player("Zayn"), e.player("Sara")
	r := e.room(tap, host, friend)
	first := e.connect(host, r.ID, 0, false)
	first.expect("hello")
	second := e.connect(host, r.ID, 0, true)
	second.expect("hello")
	if code, reason := first.closed(); code != 4000 || reason != "replaced" {
		t.Fatalf("first closed %d %q", code, reason)
	}
	second.send(map[string]any{"t": "ping"})
	second.expect("pong")
}

// A kick closes the kicked member's socket with 4003 after kicked and left;
// a leave closes the leaver's, after the new host; an end closes every
// socket with 4004 after the ranked results.
func TestKickLeaveAndEndCloseSockets(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	a, b, c, d := e.player("Zayn"), e.player("Sara"), e.player("Ali"), e.player("Huda")
	r := e.room(tap, a, b, c, d)
	sa, sb, sc, sd := e.connect(a, r.ID, 0, false), e.connect(b, r.ID, 0, false), e.connect(c, r.ID, 0, false), e.connect(d, r.ID, 0, false)
	for _, s := range []*sock{sa, sb, sc, sd} {
		s.expect("hello")
	}

	// A join while everybody is connected: the whole member, then the list.
	late := e.player("Omar")
	e.call(late, "POST", "/v1/rooms/join", map[string]any{"code": r.Code}, 200, nil)
	for _, s := range []*sock{sa, sb, sc, sd} {
		j := s.expect("joined")
		var who rooms.Member
		if err := json.Unmarshal(j.Player, &who); err != nil || who.ID != late.id || who.Nickname != "Omar" || who.Seat != 4 {
			t.Fatalf("%s joined: %s", s.who, j.raw)
		}
		if m := s.expect("members"); len(m.List) != 5 {
			t.Fatalf("%s members: %s", s.who, m.raw)
		}
	}

	e.call(a, "POST", "/v1/rooms/"+r.ID+"/kick", map[string]any{"player": c.id}, 204, nil)
	if k := sc.expect("kicked"); string(k.Player) != `"`+c.id+`"` || k.By == nil || *k.By != a.id {
		t.Fatalf("kicked = %s", k.raw)
	}
	if l := sc.expect("left"); l.Reason != "kicked" {
		t.Fatalf("left = %s", l.raw)
	}
	if code, reason := sc.closed(); code != 4003 || reason != "kicked" {
		t.Fatalf("the kicked socket closed %d %q", code, reason)
	}
	sa.expect("kicked")
	sa.expect("left")
	if m := sa.expect("members"); len(m.List) != 4 {
		t.Fatalf("members after the kick = %s", m.raw)
	}

	e.call(a, "POST", "/v1/rooms/"+r.ID+"/leave", nil, 204, nil)
	if h := sb.expect("host"); string(h.Player) != `"`+b.id+`"` || h.By != nil {
		t.Fatalf("host = %s", h.raw)
	}
	if code, reason := sa.closed(); code != 4003 || reason != "left" {
		t.Fatalf("the leaver's socket closed %d %q", code, reason)
	}

	var ended struct {
		Status  string        `json:"status"`
		Round   int           `json:"round"`
		Results []model.Score `json:"results"`
	}
	e.call(b, "POST", "/v1/rooms/"+r.ID+"/start", nil, 200, nil)
	e.call(b, "POST", "/v1/rooms/"+r.ID+"/end", map[string]any{"scores": []map[string]any{
		{"player": b.id, "score": 3}, {"player": d.id, "score": 7}, {"player": c.id, "score": 3},
	}}, 200, &ended)
	if ended.Status != "ended" || ended.Round != 1 || len(ended.Results) != 3 {
		t.Fatalf("ended = %+v", ended)
	}
	for _, s := range []*sock{sb, sd} {
		f := s.expect("ended")
		if f.Reason != "host" || f.By == nil || *f.By != b.id || f.Round != 1 || len(f.Results) != 3 ||
			f.Results[0].Rank != 2 || f.Results[1].Rank != 1 || f.Results[2].Rank != 2 {
			t.Fatalf("%s: %s", s.who, f.raw)
		}
		if code, reason := s.closed(); code != 4004 || reason != "ended" {
			t.Fatalf("%s closed %d %q", s.who, code, reason)
		}
	}
	var mine struct {
		Rooms []any `json:"rooms"`
	}
	e.call(b, "GET", "/v1/rooms/mine", nil, 200, &mine)
	if len(mine.Rooms) != 0 {
		t.Fatalf("mine = %v", mine.Rooms)
	}
}

// An admin closing a room from the console, during a round: every member's
// socket hears ended with nobody named and the reason admin, then closes with
// 4004, presence goes back to online, and the door answers room_closed.
func TestAConsoleCloseClosesEverySocket(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	a, b, c := e.player("Zayn"), e.player("Sara"), e.player("Ali")
	r := e.room(tap, a, b, c)
	e.call(a, "POST", "/v1/rooms/"+r.ID+"/start", nil, 200, nil)
	socks := []*sock{e.connect(a, r.ID, 0, false), e.connect(b, r.ID, 0, true), e.connect(c, r.ID, 0, false)}
	for _, s := range socks {
		s.expect("hello")
	}

	closed, err := e.rules.Close(context.Background(), ids.New(ids.Admin), r.ID)
	if err != nil || closed.Status != "ended" || closed.EndReason == nil || *closed.EndReason != "admin" {
		t.Fatalf("close = %+v, %v", closed, err)
	}
	for _, s := range socks {
		f := s.expect("ended")
		if f.Reason != "admin" || f.By != nil || f.Round != 1 || f.At == "" || f.Seq == 0 {
			t.Fatalf("%s: %s", s.who, f.raw)
		}
		if code, reason := s.closed(); code != 4004 || reason != "ended" {
			t.Fatalf("%s closed %d %q", s.who, code, reason)
		}
	}

	// Nobody is in a room any more: presence says online once each socket's
	// close has been handled.
	deadline := time.Now().Add(3 * time.Second)
	for {
		states, err := e.presence.Of(context.Background(), []string{a.id, b.id, c.id})
		if err != nil {
			t.Fatal(err)
		}
		online := 0
		for _, st := range states {
			if st.State == players.Online {
				online++
			}
		}
		if online == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("presence after the close = %+v", states)
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.refusal(e.socketURL(r.ID, 0), &websocket.DialOptions{
		Subprotocols: []string{ws.Subprotocol},
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + a.token}},
	}, http.StatusConflict, "room_closed")
}

// Two rounds in one room over real sockets: a finish says finished with the
// round's results and closes nothing, the room waits in its lobby with the
// same code, and the next start says started with round 2 after the state
// went back to version 0, as a fresh hello shows. Only the end closes them.
func TestTwoRoundsOverTheSocket(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	host, friend := e.player("Zayn"), e.player("Sara")
	r := e.room(xo, host, friend)
	sa, sb := e.connect(host, r.ID, 0, false), e.connect(friend, r.ID, 0, false)
	sa.expect("hello")
	sb.expect("hello")

	e.call(host, "POST", "/v1/rooms/"+r.ID+"/start", nil, 200, nil)
	for _, s := range []*sock{sa, sb} {
		if f := s.expect("started"); f.Round != 1 || f.By == nil || *f.By != host.id {
			t.Fatalf("%s: %s", s.who, f.raw)
		}
	}
	sa.send(map[string]any{"t": "state", "version": 0, "data": map[string]any{"board": "x"}, "ref": "w1"})
	sb.expect("state")

	var room struct {
		Status       string        `json:"status"`
		Round        int           `json:"round"`
		Code         string        `json:"code"`
		StateVersion int           `json:"state_version"`
		Results      []model.Score `json:"results"`
	}
	e.call(host, "POST", "/v1/rooms/"+r.ID+"/finish", map[string]any{"scores": []map[string]any{
		{"player": host.id, "score": 1}, {"player": friend.id, "score": 2},
	}}, 200, &room)
	if room.Status != "lobby" || room.Round != 1 || room.Code != r.Code || len(room.Results) != 2 || room.StateVersion != 1 {
		t.Fatalf("finished = %+v", room)
	}
	for _, s := range []*sock{sa, sb} {
		f := s.expect("finished")
		if f.Round != 1 || f.Reason != "host" || f.By == nil || *f.By != host.id || len(f.Results) != 2 ||
			f.Results[0].Rank != 2 || f.Results[1].Rank != 1 {
			t.Fatalf("%s: %s", s.who, f.raw)
		}
	}
	// Nothing closed: the sockets still carry the room.
	sb.send(map[string]any{"t": "msg", "data": "again?"})
	if m := sa.expect("msg"); m.From != friend.id {
		t.Fatalf("after the finish: %s", m.raw)
	}

	e.call(host, "POST", "/v1/rooms/"+r.ID+"/start", nil, 200, &room)
	if room.Status != "playing" || room.Round != 2 || room.StateVersion != 0 || len(room.Results) != 0 {
		t.Fatalf("round 2 = %+v", room)
	}
	for _, s := range []*sock{sa, sb} {
		if f := s.expect("started"); f.Round != 2 {
			t.Fatalf("%s: %s", s.who, f.raw)
		}
	}
	sa.send(map[string]any{"t": "state", "version": 1, "data": "old", "ref": "w2"})
	if f := sa.expect("error"); f.Code != "state_stale" || f.Version == nil || *f.Version != 0 || f.Ref != "w2" {
		t.Fatalf("a write from round 1: %s", f.raw)
	}
	fresh := e.connect(friend, r.ID, 0, true)
	if h := fresh.expect("hello"); string(h.State) != `{"version":0,"data":null}` || !strings.Contains(string(h.Room), `"round":2`) {
		t.Fatalf("a fresh hello in round 2: %s", h.raw)
	}
	if code, _ := sb.closed(); code != 4000 {
		t.Fatalf("the replaced socket closed %d", code)
	}

	e.call(host, "POST", "/v1/rooms/"+r.ID+"/end", nil, 200, nil)
	for _, s := range []*sock{sa, fresh} {
		if f := s.expect("ended"); f.Round != 2 || len(f.Results) != 0 {
			t.Fatalf("%s: %s", s.who, f.raw)
		}
		if code, reason := s.closed(); code != 4004 || reason != "ended" {
			t.Fatalf("%s closed %d %q", s.who, code, reason)
		}
	}
}

// Presence: in the room while the socket is open, online once it closes.
func TestPresenceFollowsTheSocket(t *testing.T) {
	e := newEnv(t, relay.DefaultConfig())
	host, friend := e.player("Zayn"), e.player("Sara")
	r := e.room(tap, host, friend)
	ctx := context.Background()

	sa := e.connect(host, r.ID, 0, false)
	sa.expect("hello")
	st, err := e.presence.Of(ctx, []string{host.id})
	if err != nil || st[host.id].State != players.InRoom || st[host.id].Room != r.ID {
		t.Fatalf("presence while connected = %+v, %v", st, err)
	}
	var room struct {
		Members []rooms.Member `json:"members"`
	}
	e.call(friend, "GET", "/v1/rooms/"+r.ID, nil, 200, &room)
	if !room.Members[0].Connected || room.Members[1].Connected {
		t.Fatalf("members = %+v", room.Members)
	}

	sb := e.connect(friend, r.ID, 0, false)
	sb.expect("hello")
	_ = sa.c.Close(websocket.StatusNormalClosure, "")
	<-sa.done
	// The room hears the disconnect.
	for {
		m := sb.expect("members")
		if !m.List[0].Connected {
			break
		}
	}
	st, err = e.presence.Of(ctx, []string{host.id})
	if err != nil || st[host.id].State != players.Online {
		t.Fatalf("presence after the close = %+v, %v", st, err)
	}
}
