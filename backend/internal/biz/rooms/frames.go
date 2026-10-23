package rooms

import (
	"encoding/json"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/model"
)

// The frames, as docs/ROOMS.md lists them: every one a JSON object whose
// first field is t. A frame with a seq is part of the room's history; the
// relay splices the seq in as it publishes, so Seq is empty on the way out
// of here and set on the way back in. hello, replay, pong and error belong
// to one connection and never carry one.

// Frame types, server to client.
const (
	FrameHello    = "hello"
	FrameReplay   = "replay"
	FrameJoined   = "joined"
	FrameLeft     = "left"
	FrameMembers  = "members"
	FrameMsg      = "msg"
	FrameState    = "state"
	FrameStarted  = "started"
	FrameFinished = "finished"
	FrameEnded    = "ended"
	FrameHost     = "host"
	FrameKicked   = "kicked"
	FramePong     = "pong"
	FrameError    = "error"
)

// Frame types, client to server. msg and state share their names with the
// frames they produce.
const FramePing = "ping"

// Close codes, as the app acts on them.
const (
	CloseReplaced     = 4000 // another connection took over: stop
	CloseRemoved      = 4003 // left or kicked: stop
	CloseEnded        = 4004 // the room is over: stop
	CloseSlowConsumer = 4008 // reconnect with after
	CloseGoingAway    = 1001 // server shutdown or idle: reconnect with after
)

// Error codes on an error frame.
const (
	ErrBadFrame     = "bad_frame"
	ErrTooLarge     = "too_large"
	ErrRateLimited  = "rate_limited"
	ErrNotMember    = "not_member"
	ErrStateStale   = "state_stale"
	ErrHostOnly     = "host_only"
	ErrNoState      = "no_state"
	ErrGap          = "gap"
	ErrSlowConsumer = "slow_consumer"
)

// Reasons a member is no longer in a room, on a left frame and as the
// close's reason text.
const (
	ReasonLeft   = "left"
	ReasonKicked = "kicked"
)

// Hello is the first frame on every connect: the room as this member sees it,
// the room's seq when it was read, and the shared state.
type Hello struct {
	T     string     `json:"t"`
	Seq   int64      `json:"seq"`
	Room  *Room      `json:"room"`
	State *StateView `json:"state"`
}

// StateView is the shared state: a version, and the game's data. Data is null
// until the first write; the whole view is null in a room whose config.state
// is none.
type StateView struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// MarshalJSON writes a state never written as data: null rather than nothing.
func (v StateView) MarshalJSON() ([]byte, error) {
	data := v.Data
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	return json.Marshal(struct {
		Version int             `json:"version"`
		Data    json.RawMessage `json:"data"`
	}{v.Version, data})
}

// Replay is the history this connection missed, right after hello.
type Replay struct {
	T      string            `json:"t"`
	Frames []json.RawMessage `json:"frames"`
}

// Joined is somebody new in the room.
type Joined struct {
	T      string `json:"t"`
	Seq    int64  `json:"seq,omitempty"`
	Player Member `json:"player"`
}

// Left is somebody gone: they left, or were kicked.
type Left struct {
	T      string `json:"t"`
	Seq    int64  `json:"seq,omitempty"`
	Player string `json:"player"`
	Reason string `json:"reason"`
}

// Members is the whole list again, after anything that changes it or who is
// connected. A client replaces its list with it.
type Members struct {
	T    string   `json:"t"`
	Seq  int64    `json:"seq,omitempty"`
	List []Member `json:"list"`
}

// Msg is a member's message. To is set when it was meant for one member.
type Msg struct {
	T    string          `json:"t"`
	Seq  int64           `json:"seq,omitempty"`
	From string          `json:"from"`
	Data json.RawMessage `json:"data"`
	To   string          `json:"to,omitempty"`
}

// State is the shared state after a write. Ref is the writer's own
// correlation string, on every member's copy.
type State struct {
	T       string          `json:"t"`
	Seq     int64           `json:"seq,omitempty"`
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
	By      string          `json:"by"`
	Ref     string          `json:"ref,omitempty"`
}

// Started is the host starting a round, the first or the next. The relay
// resets the shared state in the same step as it publishes this.
type Started struct {
	T     string `json:"t"`
	Seq   int64  `json:"seq,omitempty"`
	At    string `json:"at"`
	By    string `json:"by"`
	Round int    `json:"round"`
}

// Finished is a round over and the room back in its lobby, with the round's
// results. By is null when the clock finished it. No socket closes.
type Finished struct {
	T       string        `json:"t"`
	Seq     int64         `json:"seq,omitempty"`
	At      string        `json:"at"`
	By      *string       `json:"by"`
	Round   int           `json:"round"`
	Reason  string        `json:"reason"`
	Results []model.Score `json:"results"`
}

// Ended is the room over. By is null when no member ended it: the sweeper,
// the last member leaving, or an admin from the console (reason admin).
// Round is the latest round started, 0 when none; Results are that round's.
type Ended struct {
	T       string        `json:"t"`
	Seq     int64         `json:"seq,omitempty"`
	At      string        `json:"at"`
	By      *string       `json:"by"`
	Reason  string        `json:"reason"`
	Round   int           `json:"round"`
	Results []model.Score `json:"results"`
}

// HostChanged is a new host. By is null when the old host left.
type HostChanged struct {
	T      string  `json:"t"`
	Seq    int64   `json:"seq,omitempty"`
	Player string  `json:"player"`
	By     *string `json:"by"`
}

// Kicked is the host removing somebody. A left frame follows it.
type Kicked struct {
	T      string `json:"t"`
	Seq    int64  `json:"seq,omitempty"`
	Player string `json:"player"`
	By     string `json:"by"`
}

// Pong answers a ping.
type Pong struct {
	T  string `json:"t"`
	At string `json:"at"`
}

// Error refuses one frame of this connection's. Ref is the refused frame's
// own, when it had one; Version is the current one, on state_stale.
type Error struct {
	T       string `json:"t"`
	Code    string `json:"code"`
	Ref     string `json:"ref,omitempty"`
	Version *int   `json:"version,omitempty"`
}

// stamp is the one timestamp format the API answers in: UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// Stamp is stamp for the relay, whose pongs say what time it is.
func Stamp(t time.Time) string { return stamp(t) }
