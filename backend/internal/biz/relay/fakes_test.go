package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// redisForTest is the real Redis: the scripts, the order and the channels
// are what is under test, and no fake reproduces them. Skips without
// TEST_REDIS_URL.
func redisForTest(t *testing.T) *data.Redis {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	rdb, err := data.OpenRedis(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// fakeSocket is a client with no network: frames in, frames out, a close.
type fakeSocket struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}

	// delay slows every send down: a client that reads slowly.
	delay time.Duration

	once   sync.Once
	mu     sync.Mutex
	code   int
	reason string
}

func newSocket() *fakeSocket {
	return &fakeSocket{in: make(chan []byte, 64), out: make(chan []byte, 4096), closed: make(chan struct{})}
}

func (s *fakeSocket) Next(ctx context.Context, max int) ([]byte, error) {
	select {
	case f, ok := <-s.in:
		if !ok {
			return nil, io.EOF
		}
		if len(f) > max {
			return nil, ErrTooLarge
		}
		return f, nil
	case <-s.closed:
		return nil, errors.New("closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeSocket) Send(ctx context.Context, frame []byte) error {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case s.out <- frame:
		return nil
	case <-s.closed:
		return errors.New("closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *fakeSocket) Close(code int, reason string) error {
	s.once.Do(func() {
		s.mu.Lock()
		s.code, s.reason = code, reason
		s.mu.Unlock()
		close(s.closed)
	})
	return nil
}

func (s *fakeSocket) closedWith() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code, s.reason
}

// fakeDirectory is the rooms' rules for rooms that exist only here.
type fakeDirectory struct {
	mu      sync.Mutex
	rooms   map[string]*fakeRoom
	touches map[string]int
}

type fakeRoom struct {
	status enum.RoomStatus
	host   string
	config model.RoomBlock
	seats  map[string]int
	kicked map[string]bool
}

func (d *fakeDirectory) add(roomID, host string, mode enum.HostMode, state enum.StateMode, members ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rooms == nil {
		d.rooms, d.touches = map[string]*fakeRoom{}, map[string]int{}
	}
	r := &fakeRoom{status: enum.RoomLobby, host: host, seats: map[string]int{}, kicked: map[string]bool{}}
	r.config.Players.Min, r.config.Players.Max = 1, 8
	r.config.Host, r.config.State, r.config.Pace = mode, state, enum.PaceTurn
	for i, m := range append([]string{host}, members...) {
		r.seats[m] = i
	}
	d.rooms[roomID] = r
}

func (d *fakeDirectory) remove(roomID, playerID string, kicked bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.rooms[roomID]
	delete(r.seats, playerID)
	r.kicked[playerID] = kicked
}

func (d *fakeDirectory) end(roomID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rooms[roomID].status = enum.RoomEnded
}

func (d *fakeDirectory) list(r *fakeRoom) []rooms.Member {
	out := []rooms.Member{}
	for id, seat := range r.seats {
		role := "player"
		if id == r.host {
			role = "host"
		}
		out = append(out, rooms.Member{ID: id, Nickname: id, Role: role, Seat: seat})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seat < out[j].Seat })
	return out
}

func (d *fakeDirectory) View(_ context.Context, roomID, playerID string) (*rooms.Room, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.rooms[roomID]
	out := &rooms.Room{
		ID: roomID, Code: "K7M2QX", Status: string(r.status), Host: r.host, Members: d.list(r),
		Config: rooms.RoomConfig{Host: string(r.config.Host), State: string(r.config.State)},
	}
	if seat, ok := r.seats[playerID]; ok {
		role := "player"
		if playerID == r.host {
			role = "host"
		}
		out.You = &rooms.You{Role: role, Seat: seat}
	}
	return out, nil
}

func (d *fakeDirectory) MemberList(_ context.Context, roomID string) ([]rooms.Member, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.list(d.rooms[roomID]), nil
}

func (d *fakeDirectory) Standing(_ context.Context, roomID, playerID string) (rooms.Standing, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.rooms[roomID]
	_, current := r.seats[playerID]
	return rooms.Standing{
		Open: r.status.Open(), Current: current, Kicked: r.kicked[playerID],
		Host: current && r.host == playerID, Config: r.config,
	}, nil
}

func (d *fakeDirectory) Touch(_ context.Context, roomID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.touches[roomID]++
	return nil
}

func (d *fakeDirectory) touched(roomID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.touches[roomID]
}

// fakePresence remembers every mark, in order.
type fakePresence struct {
	mu    sync.Mutex
	marks []string
	now   map[string]players.State
}

func (p *fakePresence) Mark(_ context.Context, id, state, room string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.now == nil {
		p.now = map[string]players.State{}
	}
	p.marks = append(p.marks, id+":"+state)
	p.now[id] = players.State{State: state, Room: room}
	return nil
}

func (p *fakePresence) of(id string) players.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now[id]
}

func (p *fakePresence) count(mark string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, m := range p.marks {
		if m == mark {
			n++
		}
	}
	return n
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
	Room    *rooms.Room       `json:"room"`
	State   json.RawMessage   `json:"state"`
	At      string            `json:"at"`
}

// client is one member's socket being served by the hub.
type client struct {
	t      *testing.T
	sock   *fakeSocket
	player string
	done   chan struct{}
}

func dial(t *testing.T, h *Hub, roomID, playerID string, after int64) *client {
	t.Helper()
	return dialWith(t, h, roomID, playerID, after, newSocket())
}

// dialWith serves a socket made beforehand: one that reads slowly.
func dialWith(t *testing.T, h *Hub, roomID, playerID string, after int64, sock *fakeSocket) *client {
	t.Helper()
	c := &client{t: t, sock: sock, player: playerID, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		h.Serve(context.Background(), c.sock, roomID, playerID, after)
	}()
	t.Cleanup(func() {
		_ = c.sock.Close(1000, "")
		<-c.done
	})
	return c
}

func (c *client) send(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	c.sock.in <- b
}

func (c *client) raw(b []byte) { c.sock.in <- b }

// next is the next frame, failing after a second without one.
func (c *client) next() frame {
	c.t.Helper()
	select {
	case b := <-c.sock.out:
		var f frame
		if err := json.Unmarshal(b, &f); err != nil {
			c.t.Fatalf("%s read %s: %v", c.player, b, err)
		}
		return f
	case <-time.After(2 * time.Second):
		c.t.Fatalf("%s: no frame", c.player)
		return frame{}
	}
}

// expect skips frames until one of type t arrives.
func (c *client) expect(t string) frame {
	c.t.Helper()
	for {
		f := c.next()
		if f.T == t {
			return f
		}
	}
}

// quiet says nothing arrives for a moment.
func (c *client) quiet() {
	c.t.Helper()
	select {
	case b := <-c.sock.out:
		c.t.Fatalf("%s was sent %s", c.player, b)
	case <-time.After(150 * time.Millisecond):
	}
}

// closed waits for the socket to be closed and says how.
func (c *client) closed() (int, string) {
	c.t.Helper()
	select {
	case <-c.sock.closed:
		return c.sock.closedWith()
	case <-time.After(3 * time.Second):
		c.t.Fatalf("%s was never closed", c.player)
		return 0, ""
	}
}

// hub is a hub over the real Redis, a fake directory and a fake presence.
func hub(t *testing.T, cfg Config) (*Hub, *fakeDirectory, *fakePresence, *data.Redis) {
	t.Helper()
	rdb := redisForTest(t)
	dir, presence := &fakeDirectory{}, &fakePresence{}
	h := NewHub(rdb, dir, presence, cfg, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.Close(ctx)
	})
	return h, dir, presence, rdb
}

func newRoom() string   { return ids.New(ids.Room) }
func newPlayer() string { return ids.New(ids.Player) }
