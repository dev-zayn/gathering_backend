package relay

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Directory is what the relay asks of the rooms' rules. *rooms.Service is
// one; the tests bring their own.
type Directory interface {
	// View is the room as one member sees it: hello's room.
	View(ctx context.Context, roomID, playerID string) (*rooms.Room, error)
	// MemberList is the members frame's list.
	MemberList(ctx context.Context, roomID string) ([]rooms.Member, error)
	// Standing is one player's place in the room as a frame arrives.
	Standing(ctx context.Context, roomID, playerID string) (rooms.Standing, error)
	// Touch moves the room's active_at.
	Touch(ctx context.Context, roomID string) error
}

// Presence is the player's presence key, as the relay sets it.
type Presence interface {
	Mark(ctx context.Context, id, state, room string) error
}

// Config bounds what one socket may do. The relay never reads inside a
// frame's data, so these are the only rules it has of its own.
type Config struct {
	// MaxFrame is RELAY_MAX_FRAME: the most bytes a client frame may hold.
	MaxFrame int
	// Rate is RELAY_RATE: frames a second per socket, a token bucket.
	Rate int
	// Queue is how many frames a socket may fall behind by before it is
	// closed as a slow consumer.
	Queue int
	// Idle is how long a socket may stay silent before it is closed. The app
	// pings every 20 s.
	Idle time.Duration
	// History is how many frames a room's log keeps for a reconnect.
	History int
	// KeyTTL is how long a room's keys outlive the last thing said in it.
	KeyTTL time.Duration
	// TouchEvery is how often, at most, a ping moves the room's active_at.
	TouchEvery time.Duration
	// WriteTimeout bounds one frame's write to a socket.
	WriteTimeout time.Duration
	// Subscribe bounds the wait for Redis to confirm a room's subscription.
	Subscribe time.Duration
}

// DefaultConfig is docs/ROOMS.md's numbers.
func DefaultConfig() Config {
	return Config{
		MaxFrame: 16 << 10, Rate: 30, Queue: 256, Idle: 45 * time.Second,
		History: 200, KeyTTL: 2 * time.Hour, TouchEvery: time.Minute,
		WriteTimeout: 10 * time.Second, Subscribe: 5 * time.Second,
	}
}

// Hub is the process's side of every room it has a socket for. One per
// process: one Redis connection subscribes to every room with at least one
// local socket, and a dispatcher hands each frame to the sockets here.
type Hub struct {
	rdb      *data.Redis
	dir      Directory
	presence Presence
	cfg      Config
	log      *applog.Logger

	ps *redis.PubSub

	mu     sync.Mutex
	rooms  map[string]*room
	closed bool

	// serving counts the sockets inside Serve, so Close can wait for them.
	serving sync.WaitGroup
	done    chan struct{}
}

// room is one room's local side: its sockets, by player, and the state of
// its subscription. Guarded by Hub.mu.
type room struct {
	id    string
	conns map[string]*conn

	// subscribed is set by Redis's confirmation; ready is closed with it.
	// Anything arriving on the channel before it is a leftover of an earlier
	// subscription and is dropped.
	subscribed bool
	ready      chan struct{}

	// last is the seq last delivered here. The channel carries every seq in
	// order, so a frame that skips one means frames were lost.
	last int64

	// ended is set by the ended frame: nothing more is delivered, and every
	// socket is on its way out.
	ended bool
}

// NewHub starts the hub's subscription and its dispatcher.
func NewHub(rdb *data.Redis, dir Directory, presence Presence, cfg Config, log *applog.Logger) *Hub {
	def := DefaultConfig()
	if cfg.MaxFrame <= 0 {
		cfg.MaxFrame = def.MaxFrame
	}
	if cfg.Rate <= 0 {
		cfg.Rate = def.Rate
	}
	if cfg.Queue <= 0 {
		cfg.Queue = def.Queue
	}
	if cfg.Idle <= 0 {
		cfg.Idle = def.Idle
	}
	if cfg.History <= 0 {
		cfg.History = def.History
	}
	if cfg.KeyTTL <= 0 {
		cfg.KeyTTL = def.KeyTTL
	}
	if cfg.TouchEvery <= 0 {
		cfg.TouchEvery = def.TouchEvery
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = def.WriteTimeout
	}
	if cfg.Subscribe <= 0 {
		cfg.Subscribe = def.Subscribe
	}
	if log == nil {
		log = applog.New("relay")
	}

	h := &Hub{
		rdb: rdb, dir: dir, presence: presence, cfg: cfg, log: log,
		rooms: map[string]*room{}, done: make(chan struct{}),
	}
	// No channel yet: rooms are added as their first socket arrives. The
	// health check pings an idle connection and resubscribes after a drop.
	h.ps = rdb.Subscribe(context.Background())
	go h.dispatch(h.ps.ChannelWithSubscriptions(redis.WithChannelSize(1024)))
	return h
}

// Close ends every socket with 1001 — the app reconnects, to another replica
// — and waits for them, up to the context's deadline.
func (h *Hub) Close(ctx context.Context) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	for _, r := range h.rooms {
		for _, c := range r.conns {
			c.kill(closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyShutdown})
		}
	}
	h.mu.Unlock()

	waited := make(chan struct{})
	go func() { h.serving.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-ctx.Done():
	}
	_ = h.ps.Close()
	<-h.done
}

// enter registers a socket with its room, subscribing to the room's channel
// when it is the first one here. A socket already here for the same player
// is replaced. The room's ready channel says when frames start to count.
func (h *Hub) enter(ctx context.Context, c *conn) (*room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errClosed
	}
	r := h.rooms[c.room]
	if r == nil {
		r = &room{id: c.room, conns: map[string]*conn{}, ready: make(chan struct{})}
		h.rooms[c.room] = r
		// Under the lock, so a subscribe and the unsubscribe of the room's
		// last socket leaving can never cross on the wire.
		sctx, cancel := context.WithTimeout(ctx, h.cfg.Subscribe)
		err := h.ps.Subscribe(sctx, channel(c.room))
		cancel()
		if err != nil {
			// go-redis keeps the channel on its list and subscribes again when
			// the connection comes back; this socket waits on ready like any.
			h.log.Warn("room not subscribed yet", "room", c.room, "err", err)
		}
	}
	if old := r.conns[c.player]; old != nil {
		old.kill(closing{code: rooms.CloseReplaced, reason: "replaced", why: whyReplaced})
	}
	r.conns[c.player] = c
	return r, nil
}

// exit takes a socket off its room, unless another has taken its place, and
// drops the room's subscription with its last socket.
func (h *Hub) exit(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rooms[c.room]
	if r == nil {
		return
	}
	if r.conns[c.player] == c {
		delete(r.conns, c.player)
	}
	if len(r.conns) > 0 {
		return
	}
	delete(h.rooms, r.id)
	if h.closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.Subscribe)
	defer cancel()
	if err := h.ps.Unsubscribe(ctx, channel(r.id)); err != nil {
		h.log.Warn("room not unsubscribed", "room", r.id, "err", err)
	}
}

// dispatch hands every message on every room channel to the sockets here,
// in the order Redis sent them. It never blocks on a socket: a socket that
// cannot take a frame is closed as a slow consumer instead.
func (h *Hub) dispatch(in <-chan any) {
	defer close(h.done)
	for msg := range in {
		switch m := msg.(type) {
		case *redis.Subscription:
			if m.Kind == "subscribe" {
				h.subscribed(roomOf(m.Channel))
			}
		case *redis.Message:
			h.deliver(roomOf(m.Channel), []byte(m.Payload))
		}
	}
}

// subscribed is Redis confirming a room's subscription. The first opens the
// room for frames. Another is go-redis subscribing again after a dropped
// connection: what was published meanwhile never reached us, so every socket
// here is closed with 1001 and reconnects, replaying it from the log.
func (h *Hub) subscribed(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rooms[roomID]
	if r == nil {
		return
	}
	if !r.subscribed {
		r.subscribed = true
		close(r.ready)
		return
	}
	h.log.Warn("room resubscribed; its sockets reconnect to catch up", "room", roomID)
	r.last = 0
	for _, c := range r.conns {
		c.kill(closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyLost})
	}
}

// envelope is what the hub reads of a frame: enough to route it, never data.
// Player is an id on most frames and a whole Member on joined, so it is kept
// raw and read as an id only where one is meant.
type envelope struct {
	T      string          `json:"t"`
	Seq    int64           `json:"seq"`
	To     string          `json:"to"`
	Player json.RawMessage `json:"player"`
	Reason string          `json:"reason"`
	Conn   string          `json:"conn"`
}

// player is the frame's player as an id, or "" when it is not one.
func (e envelope) player() string {
	var id string
	if json.Unmarshal(e.Player, &id) != nil {
		return ""
	}
	return id
}

// deliver routes one frame to the room's sockets here. msg with a to goes to
// that member only; everything else to everybody, the sender included. The
// hubs act on what they deliver: a left closes that member's socket with
// 4003, an ended closes every socket with 4004, each once the frame itself
// has gone out.
func (h *Hub) deliver(roomID string, frame []byte) {
	var env envelope
	if err := json.Unmarshal(frame, &env); err != nil {
		h.log.Warn("unreadable frame on a room channel", "room", roomID, "err", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rooms[roomID]
	if r == nil || !r.subscribed || r.ended {
		return
	}
	if env.T == noticeType {
		h.noticed(r, env.player(), env.Conn)
		return
	}
	if env.Seq > 0 {
		if r.last != 0 && env.Seq != r.last+1 {
			h.log.Warn("frames lost on a room channel; its sockets reconnect to catch up",
				"room", roomID, "after", r.last, "got", env.Seq)
			for _, c := range r.conns {
				c.kill(closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyLost})
			}
		}
		r.last = env.Seq
	}

	for _, c := range r.conns {
		if env.T == rooms.FrameMsg && env.To != "" && env.To != c.player {
			continue
		}
		c.offer(env.Seq, frame)
	}

	switch env.T {
	case rooms.FrameLeft:
		if c := r.conns[env.player()]; c != nil {
			reason := env.Reason
			if reason != rooms.ReasonKicked {
				reason = rooms.ReasonLeft
			}
			c.closeAfter(closing{code: rooms.CloseRemoved, reason: reason, why: whyRemoved})
		}
	case rooms.FrameEnded:
		r.ended = true
		for _, c := range r.conns {
			c.closeAfter(closing{code: rooms.CloseEnded, reason: "ended", why: whyEnded})
		}
	}
}

// noticeType is the relay's own message on a room's channel: a socket
// opened for a player, somewhere. It is never part of the history and never
// reaches a client.
const noticeType = "_conn"

// notice is that message.
type notice struct {
	T      string `json:"t"`
	Player string `json:"player"`
	Conn   string `json:"conn"`
}

// noticed applies a notice. One socket per member: a socket here for the
// same player that has already seen its own notice is older than this one
// and is replaced. One that has not is newer — its notice is still on the
// way — and stays; every hub sees the notices in the same order, so exactly
// the last socket opened survives.
func (h *Hub) noticed(r *room, player, connID string) {
	c := r.conns[player]
	if c == nil {
		return
	}
	if c.id == connID {
		c.announced = true
		return
	}
	if !c.announced {
		return
	}
	delete(r.conns, player)
	c.kill(closing{code: rooms.CloseReplaced, reason: "replaced", why: whyReplaced})
}

// announce tells every hub a socket opened for this player in this room.
func (h *Hub) announce(ctx context.Context, c *conn) error {
	b, err := json.Marshal(notice{T: noticeType, Player: c.player, Conn: c.id})
	if err != nil {
		return err
	}
	return h.rdb.Publish(ctx, channel(c.room), b).Err()
}

// Publish appends frames to a room's history and sends them, in order. It is
// the rooms' Publisher, and the relay's own way to say anything.
func (h *Hub) Publish(ctx context.Context, roomID string, frames ...any) error {
	for _, f := range frames {
		head, tail, err := split(f)
		if err != nil {
			return err
		}
		err = publishScript.Run(ctx, h.rdb, keys(roomID),
			channel(roomID), head, tail, h.ttl(), h.cfg.History).Err()
		if err != nil {
			return err
		}
	}
	return nil
}

// Start resets the room's shared state to version 0 with no data and
// publishes the started frame, in one script: every round begins afresh,
// and a write based on the last round's state can only be refused. It is
// the rooms' Publisher.Start.
func (h *Hub) Start(ctx context.Context, roomID string, started rooms.Started) error {
	head, tail, err := split(started)
	if err != nil {
		return err
	}
	return startScript.Run(ctx, h.rdb, keys(roomID),
		channel(roomID), head, tail, h.ttl(), h.cfg.History).Err()
}

// End publishes the ended frame, then drops the room's live keys: nothing
// said after the end belongs to the room.
func (h *Hub) End(ctx context.Context, roomID string, ended rooms.Ended) error {
	if err := h.Publish(ctx, roomID, ended); err != nil {
		return err
	}
	return h.rdb.Del(ctx, seqKey(roomID), logKey(roomID), verKey(roomID), dataKey(roomID), touchKey(roomID)).Err()
}

// State is the room's live shared state: version 0 and no data until the
// first write of the round.
func (h *Hub) State(ctx context.Context, roomID string) (rooms.StateView, error) {
	vals, err := h.rdb.MGet(ctx, verKey(roomID), dataKey(roomID)).Result()
	if err != nil {
		return rooms.StateView{}, err
	}
	var out rooms.StateView
	if s, ok := vals[0].(string); ok {
		if out.Version, err = strconv.Atoi(s); err != nil {
			return rooms.StateView{}, err
		}
	}
	if s, ok := vals[1].(string); ok && out.Version > 0 {
		out.Data = json.RawMessage(s)
	}
	return out, nil
}

// errStale is a state write based on an old version; current says which is
// the latest.
type errStale struct{ current int }

func (e errStale) Error() string { return "relay: state is at version " + strconv.Itoa(e.current) }

// writeState is the compare-and-set: data becomes version+1 when version is
// the current one, published as a state frame by the same script.
func (h *Hub) writeState(ctx context.Context, roomID, by string, version int, data json.RawMessage, ref string) error {
	tail, err := stateTail(data, by, ref)
	if err != nil {
		return err
	}
	res, err := stateScript.Run(ctx, h.rdb, keys(roomID),
		channel(roomID), version, string(data), typePrefix+rooms.FrameState+`"`, tail, h.ttl(), h.cfg.History).Int64Slice()
	if err != nil {
		return err
	}
	if len(res) < 2 {
		return errors.New("relay: the state script answered nothing")
	}
	if res[0] != 1 {
		return errStale{current: int(res[1])}
	}
	return nil
}

// stateTail is a state frame after its version: the data as sent, who wrote
// it, and the writer's ref when there was one.
func stateTail(data json.RawMessage, by, ref string) (string, error) {
	who, err := json.Marshal(by)
	if err != nil {
		return "", err
	}
	tail := `,"data":` + string(data) + `,"by":` + string(who)
	if ref != "" {
		r, err := json.Marshal(ref)
		if err != nil {
			return "", err
		}
		tail += `,"ref":` + string(r)
	}
	return tail + "}", nil
}

// seq is the room's frame counter as it stands: 0 for a room nothing has
// been said in.
func (h *Hub) seq(ctx context.Context, roomID string) (int64, error) {
	n, err := h.rdb.Get(ctx, seqKey(roomID)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

// history is the room's log.
func (h *Hub) history(ctx context.Context, roomID string) ([]string, error) {
	return h.rdb.LRange(ctx, logKey(roomID), 0, -1).Result()
}

// touch says the room is in use: its active_at moves, at most once per
// TouchEvery unless forced (a socket connecting), and its keys live on.
func (h *Hub) touch(ctx context.Context, roomID string, force bool) {
	key := touchKey(roomID)
	if force {
		if err := h.rdb.Set(ctx, key, 1, h.cfg.TouchEvery).Err(); err != nil {
			h.log.Warn("room not touched", "room", roomID, "err", err)
		}
	} else {
		first, err := h.rdb.SetNX(ctx, key, 1, h.cfg.TouchEvery).Result()
		if err != nil || !first {
			return
		}
	}
	pipe := h.rdb.Pipeline()
	for _, k := range keys(roomID) {
		pipe.Expire(ctx, k, h.cfg.KeyTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		h.log.Warn("room keys not refreshed", "room", roomID, "err", err)
	}
	if err := h.dir.Touch(ctx, roomID); err != nil {
		h.log.Warn("room active_at not moved", "room", roomID, "err", err)
	}
}

func (h *Hub) ttl() int { return int(h.cfg.KeyTTL / time.Second) }

var errClosed = errors.New("relay: the hub is closed")
