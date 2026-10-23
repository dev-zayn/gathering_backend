package relay

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// Socket is one WebSocket as the hub drives it. http/ws adapts the library
// to it, so nothing here knows which library that is, and the tests drive
// the hub with no network at all.
type Socket interface {
	// Next is the client's next frame. A frame over max bytes is read to its
	// end and dropped, and Next answers ErrTooLarge with the socket open.
	Next(ctx context.Context, max int) ([]byte, error)
	// Send writes one frame.
	Send(ctx context.Context, frame []byte) error
	// Close sends a close frame with the code and reason and ends the socket.
	Close(code int, reason string) error
}

// ErrTooLarge is a client frame over the limit, already drained.
var ErrTooLarge = errors.New("relay: frame too large")

// maxRef is the longest correlation string a client may attach.
const maxRef = 128

// why is what ended a socket, which decides what its leaving says: a
// replaced socket says nothing (its successor speaks), a removed or ended
// one leaves the list to the change that removed it, and the rest are a
// member disconnecting.
type why int

const (
	whyGone     why = iota // the client closed, or the network did
	whyIdle                // nothing from the client for Idle
	whySlow                // the outbound queue filled up
	whyLost                // frames were lost on the channel: reconnect and replay
	whyInternal            // something here failed
	whyReplaced            // another socket for the same member took over
	whyRemoved             // left or kicked
	whyEnded               // the room ended
	whyShutdown            // this process is stopping
)

// closing is a close decided for a socket: what to send first, if anything,
// then the code and the reason.
type closing struct {
	frame  []byte
	code   int
	reason string
	why    why
}

// item is one thing in a socket's outbound queue: a frame, or the close that
// follows the frames before it.
type item struct {
	frame []byte
	close *closing
}

// held is a frame that arrived before hello was sent.
type held struct {
	seq   int64
	frame []byte
}

// conn is one socket in one room.
type conn struct {
	hub    *Hub
	sock   Socket
	id     string
	room   string
	player string

	out chan item
	// kills is the close that cannot wait behind the queue: a slow consumer,
	// a replacement, a shutdown. One is enough.
	kills chan closing

	mu      sync.Mutex
	ready   bool
	pending []held
	since   int64
	ended   *closing

	// announced is set when this socket's own notice came back on the
	// channel. Read and written only by the dispatcher, under Hub.mu.
	announced bool

	bucket bucket
}

// Serve runs one socket from its first frame to its last: it registers with
// the room here, sends hello (and the replay when after is given), relays the
// client's frames, and cleans up. It returns when the socket is done.
// Whether the player may open it at all is decided before: http/ws asks the
// rooms (Admit) before upgrading.
func (h *Hub) Serve(ctx context.Context, sock Socket, roomID, playerID string, after int64) {
	// Counted under the lock Close takes, so no socket starts once Close has
	// begun waiting for the last one.
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = sock.Close(rooms.CloseGoingAway, "going_away")
		return
	}
	h.serving.Add(1)
	h.mu.Unlock()
	defer h.serving.Done()

	c := &conn{
		hub: h, sock: sock, id: ids.New("conn"), room: roomID, player: playerID,
		out:    make(chan item, h.cfg.Queue),
		kills:  make(chan closing, 1),
		bucket: bucket{rate: float64(h.cfg.Rate), tokens: float64(h.cfg.Rate), last: time.Now()},
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	written := make(chan struct{})
	go func() { defer close(written); c.write(ctx) }()

	if c.open(ctx, after) == nil {
		c.read(ctx)
	}
	ended := c.finish()

	// A close decided here goes out after the frames queued before it, and
	// the writer stops by itself once it has. A client that went away has
	// nothing more coming to it.
	if ended.why == whyGone {
		cancel()
	}
	<-written
	h.exit(c)
	c.leave(ended)
}

// open is everything between the upgrade and the first live frame. A close
// decided on the way answers why it stopped.
func (c *conn) open(ctx context.Context, after int64) *closing {
	h := c.hub
	r, err := h.enter(ctx, c)
	if err != nil {
		c.kill(closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyShutdown})
		return c.decided()
	}

	// Frames count from the subscription on: S is read after it, so nothing
	// published after S can be missed.
	wait, stop := context.WithTimeout(ctx, h.cfg.Subscribe)
	defer stop()
	select {
	case <-r.ready:
	case <-wait.Done():
		h.log.Warn("room subscription not confirmed", "room", c.room)
		c.kill(closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyInternal})
		return c.decided()
	}

	if err := h.announce(ctx, c); err != nil {
		return c.fail("socket not announced", err)
	}
	if h.presence != nil {
		if err := h.presence.Mark(ctx, c.player, players.InRoom, c.room); err != nil {
			h.log.Warn("presence not set", "player", c.player, "err", err)
		}
	}
	h.touch(ctx, c.room, true)

	seq, err := h.seq(ctx, c.room)
	if err != nil {
		return c.fail("seq not read", err)
	}
	view, err := h.dir.View(ctx, c.room, c.player)
	if err != nil {
		return c.fail("hello not built", err)
	}
	hello := rooms.Hello{T: rooms.FrameHello, Seq: seq, Room: view}
	if view.Config.State != string(enum.StateNone) {
		st, err := h.State(ctx, c.room)
		if err != nil {
			return c.fail("state not read", err)
		}
		hello.State = &st
	}

	first := []any{hello}
	if after > 0 && after < seq {
		catchUp, err := c.replay(ctx, after, seq)
		if err != nil {
			return c.fail("history not read", err)
		}
		first = append(first, catchUp)
	}
	if err := c.start(seq, first); err != nil {
		return c.fail("hello not sent", err)
	}
	// Replaced, or cut off, while hello was being built: nothing to announce.
	if k := c.decided(); k != nil {
		return k
	}

	// The room may have moved on between the door and here, before this
	// socket could hear about it: hello says so, and the socket goes the
	// way the frame would have sent it.
	switch {
	case view.Status == string(enum.RoomEnded):
		c.closeAfter(closing{code: rooms.CloseEnded, reason: "ended", why: whyEnded})
		return c.decided()
	case view.You == nil:
		reason := rooms.ReasonLeft
		if st, err := h.dir.Standing(ctx, c.room, c.player); err == nil && st.Kicked {
			reason = rooms.ReasonKicked
		}
		c.closeAfter(closing{code: rooms.CloseRemoved, reason: reason, why: whyRemoved})
		return c.decided()
	}

	c.members(ctx)
	return nil
}

// replay is the history after the client's last seq, up to S, filtered as
// live delivery would have filtered it; or gap when the log no longer
// reaches back that far.
func (c *conn) replay(ctx context.Context, after, upTo int64) (any, error) {
	logged, err := c.hub.history(ctx, c.room)
	if err != nil {
		return nil, err
	}
	frames := []json.RawMessage{}
	oldest := int64(0)
	for _, f := range logged {
		var env envelope
		if json.Unmarshal([]byte(f), &env) != nil || env.Seq == 0 {
			continue
		}
		if oldest == 0 {
			oldest = env.Seq
		}
		if env.Seq <= after || env.Seq > upTo {
			continue
		}
		if env.T == rooms.FrameMsg && env.To != "" && env.To != c.player {
			continue
		}
		frames = append(frames, json.RawMessage(f))
	}
	// The log has to hold the first frame the client missed.
	if oldest == 0 || oldest > after+1 {
		return rooms.Error{T: rooms.FrameError, Code: rooms.ErrGap}, nil
	}
	return rooms.Replay{T: rooms.FrameReplay, Frames: frames}, nil
}

// start sends hello and the catch-up, then whatever arrived meanwhile with a
// seq past S, in order; from here on frames go straight out.
func (c *conn) start(since int64, first []any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range first {
		b, err := json.Marshal(f)
		if err != nil {
			return err
		}
		c.push(b)
	}
	c.since = since
	for _, p := range c.pending {
		if p.seq == 0 || p.seq > since {
			c.push(p.frame)
		}
	}
	c.pending = nil
	c.ready = true
	return nil
}

// offer is the dispatcher handing this socket a frame. Before hello it is
// held; after, a frame hello already covered is dropped.
func (c *conn) offer(seq int64, frame []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended != nil {
		return
	}
	if !c.ready {
		if len(c.pending) >= c.hub.cfg.Queue {
			c.slow()
			return
		}
		c.pending = append(c.pending, held{seq: seq, frame: frame})
		return
	}
	if seq > 0 && seq <= c.since {
		return
	}
	c.push(frame)
}

// push queues a frame without waiting. A socket that is that far behind is a
// slow consumer, closed with 4008 so the app reconnects and catches up.
// Called with c.mu held.
func (c *conn) push(frame []byte) {
	if c.ended != nil {
		return
	}
	select {
	case c.out <- item{frame: frame}:
	default:
		c.slow()
	}
}

// slow closes a socket whose queue is full: the error first, then 4008, with
// nothing that was waiting. Called with c.mu held.
func (c *conn) slow() {
	b, _ := json.Marshal(rooms.Error{T: rooms.FrameError, Code: rooms.ErrSlowConsumer})
	c.killLocked(closing{frame: b, code: rooms.CloseSlowConsumer, reason: rooms.ErrSlowConsumer, why: whySlow})
}

// closeAfter closes the socket once every frame queued before it has gone
// out: a left, an ended.
func (c *conn) closeAfter(k closing) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended != nil {
		return
	}
	select {
	case c.out <- item{close: &k}:
		c.ended = &k
	default:
		c.killLocked(k)
	}
}

// kill closes the socket now, ahead of anything queued.
func (c *conn) kill(k closing) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killLocked(k)
}

// killLocked decides the close, unless one is decided already: the first
// reason a socket is given is the one it goes with, so a socket on its way
// out with 4003 or 4004 is never turned into one that reconnects.
func (c *conn) killLocked(k closing) {
	if c.ended != nil {
		return
	}
	c.ended = &k
	select {
	case c.kills <- k:
	default:
	}
}

// decided is the close this socket is going for, if one was decided.
func (c *conn) decided() *closing {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended
}

// fail closes the socket with 1011 after something here broke. The app
// reconnects with after, as it would after any close it does not know.
func (c *conn) fail(what string, err error) *closing {
	c.hub.log.Warn(what, "room", c.room, "player", c.player, "err", err)
	c.kill(closing{code: 1011, reason: "internal_error", why: whyInternal})
	return c.decided()
}

// finish marks the socket as done from this side and answers how it ended:
// the close decided for it, or the client gone.
func (c *conn) finish() *closing {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended == nil {
		c.ended = &closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyGone}
	}
	return c.ended
}

// write is the socket's one writer: the queue in order, a kill ahead of it.
func (c *conn) write(ctx context.Context) {
	for {
		select {
		case k := <-c.kills:
			c.closeWith(k)
			return
		case it := <-c.out:
			if it.close != nil {
				c.closeWith(*it.close)
				return
			}
			select {
			case k := <-c.kills:
				c.closeWith(k)
				return
			default:
			}
			wctx, cancel := context.WithTimeout(ctx, c.hub.cfg.WriteTimeout)
			err := c.sock.Send(wctx, it.frame)
			cancel()
			if err != nil {
				c.closeWith(c.orGone())
				return
			}
		case <-ctx.Done():
			// The read side is over; whatever is left is not going anywhere.
			c.closeWith(c.orGone())
			return
		}
	}
}

// orGone is the close already decided for the socket, or the plain one for
// a socket that simply stopped.
func (c *conn) orGone() closing {
	if k := c.decided(); k != nil {
		return *k
	}
	return closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyGone}
}

// closeWith sends the close's frame, if it has one, and closes.
func (c *conn) closeWith(k closing) {
	if k.frame != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.hub.cfg.WriteTimeout)
		_ = c.sock.Send(ctx, k.frame)
		cancel()
	}
	_ = c.sock.Close(k.code, k.reason)
}

// read is the client's side: every frame bounded by size and rate, silence
// bounded by Idle.
func (c *conn) read(ctx context.Context) {
	h := c.hub
	idle := time.AfterFunc(h.cfg.Idle, func() {
		c.kill(closing{code: rooms.CloseGoingAway, reason: "going_away", why: whyIdle})
	})
	defer idle.Stop()

	for {
		frame, err := c.sock.Next(ctx, h.cfg.MaxFrame)
		if err != nil && !errors.Is(err, ErrTooLarge) {
			return
		}
		idle.Reset(h.cfg.Idle)
		if c.decided() != nil {
			continue // on its way out; the read ends when the socket does
		}
		if !c.bucket.take(time.Now()) {
			c.refuse(rooms.ErrRateLimited, refOf(frame), nil)
			continue
		}
		if errors.Is(err, ErrTooLarge) {
			c.refuse(rooms.ErrTooLarge, "", nil)
			continue
		}
		c.handle(ctx, frame)
	}
}

// inbound is a client frame, as far as the relay reads it.
type inbound struct {
	T       string          `json:"t"`
	Data    json.RawMessage `json:"data"`
	To      *string         `json:"to"`
	Version json.RawMessage `json:"version"`
	Ref     string          `json:"ref"`
}

// handle is one client frame: ping, msg or state.
func (c *conn) handle(ctx context.Context, frame []byte) {
	var in inbound
	if err := json.Unmarshal(frame, &in); err != nil || len(in.Ref) > maxRef {
		c.refuse(rooms.ErrBadFrame, "", nil)
		return
	}
	switch in.T {
	case rooms.FramePing:
		c.ping(ctx)
	case rooms.FrameMsg:
		c.msg(ctx, in)
	case rooms.FrameState:
		c.state(ctx, in)
	default:
		c.refuse(rooms.ErrBadFrame, in.Ref, nil)
	}
}

// ping refreshes presence and, at most once a minute, the room's active_at.
func (c *conn) ping(ctx context.Context) {
	h := c.hub
	if h.presence != nil {
		if err := h.presence.Mark(ctx, c.player, players.InRoom, c.room); err != nil {
			h.log.Warn("presence not refreshed", "player", c.player, "err", err)
		}
	}
	h.touch(ctx, c.room, false)
	c.send(rooms.Pong{T: rooms.FramePong, At: rooms.Stamp(time.Now())})
}

// msg relays a member's message: to every member, the sender included, or
// to the one member named in to, who must be a current member.
func (c *conn) msg(ctx context.Context, in inbound) {
	if !present(in.Data) || (in.To != nil && *in.To == "") {
		c.refuse(rooms.ErrBadFrame, in.Ref, nil)
		return
	}
	out := rooms.Msg{T: rooms.FrameMsg, From: c.player, Data: in.Data}
	if in.To != nil {
		st, err := c.hub.dir.Standing(ctx, c.room, *in.To)
		if err != nil {
			c.hub.log.Warn("standing not read", "room", c.room, "err", err)
			return
		}
		if !st.Current {
			c.refuse(rooms.ErrNotMember, in.Ref, nil)
			return
		}
		out.To = *in.To
	}
	if err := c.hub.Publish(ctx, c.room, out); err != nil {
		c.hub.log.Warn("msg not published", "room", c.room, "err", err)
	}
}

// state is a compare-and-set on the shared state. Only the host writes in an
// authoritative room; nobody writes in a room without state.
func (c *conn) state(ctx context.Context, in inbound) {
	version, ok := wholeNumber(in.Version)
	if !present(in.Data) || !ok {
		c.refuse(rooms.ErrBadFrame, in.Ref, nil)
		return
	}
	st, err := c.hub.dir.Standing(ctx, c.room, c.player)
	if err != nil {
		c.hub.log.Warn("standing not read", "room", c.room, "err", err)
		return
	}
	switch {
	case !st.Open:
		return // ended: the socket is about to close with 4004
	case !st.Current:
		c.refuse(rooms.ErrNotMember, in.Ref, nil)
		return
	case st.Config.State == enum.StateNone:
		c.refuse(rooms.ErrNoState, in.Ref, nil)
		return
	case st.Config.Host == enum.HostAuthoritative && !st.Host:
		c.refuse(rooms.ErrHostOnly, in.Ref, nil)
		return
	}

	err = c.hub.writeState(ctx, c.room, c.player, version, in.Data, in.Ref)
	var stale errStale
	switch {
	case errors.As(err, &stale):
		current := stale.current
		c.refuse(rooms.ErrStateStale, in.Ref, &current)
	case err != nil:
		c.hub.log.Warn("state not written", "room", c.room, "err", err)
	}
}

// members publishes the list after this socket connected or disconnected.
func (c *conn) members(ctx context.Context) {
	h := c.hub
	list, err := h.dir.MemberList(ctx, c.room)
	if err != nil {
		h.log.Warn("members not read", "room", c.room, "err", err)
		return
	}
	if err := h.Publish(ctx, c.room, rooms.Members{T: rooms.FrameMembers, List: list}); err != nil {
		h.log.Warn("members not published", "room", c.room, "err", err)
	}
}

// leave is what a socket's end says. A replaced socket says nothing: its
// successor holds the player's presence. A stopping process says nothing
// either: the app is already reconnecting elsewhere. Otherwise the player is
// online rather than in the room, and unless the change that removed them
// already sent the list, the room hears that they disconnected.
func (c *conn) leave(k *closing) {
	h := c.hub
	if k.why == whyReplaced || k.why == whyShutdown {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.Subscribe)
	defer cancel()
	if h.presence != nil {
		if err := h.presence.Mark(ctx, c.player, players.Online, ""); err != nil {
			h.log.Warn("presence not cleared", "player", c.player, "err", err)
		}
	}
	if k.why == whyRemoved || k.why == whyEnded {
		return
	}
	c.members(ctx)
}

// send writes one frame of this connection's own: a pong, an error.
func (c *conn) send(frame any) {
	b, err := json.Marshal(frame)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.push(b)
}

// refuse answers a refused frame with an error naming its ref.
func (c *conn) refuse(code, ref string, version *int) {
	c.send(rooms.Error{T: rooms.FrameError, Code: code, Ref: ref, Version: version})
}

// refOf is the ref of a frame that will not be read further, when it has
// one: a rate-limited state write still resolves on the client's side.
func refOf(frame []byte) string {
	var in struct {
		Ref string `json:"ref"`
	}
	if json.Unmarshal(frame, &in) != nil || len(in.Ref) > maxRef {
		return ""
	}
	return in.Ref
}

// present is a field that was sent, whatever its value: null is data too.
func present(raw json.RawMessage) bool { return len(raw) > 0 }

// wholeNumber reads a version: a JSON integer, zero or more.
func wholeNumber(raw json.RawMessage) (int, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || strings.ContainsAny(s, ".eE+-") {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0
}

// bucket is RELAY_RATE as a token bucket: a burst of Rate frames, refilled
// at Rate a second.
type bucket struct {
	rate   float64
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time) bool {
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.rate {
		b.tokens = b.rate
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
