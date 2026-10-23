package rooms

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/jamaa/gathering/backend/internal/biz/players"
)

// fakePublisher is the relay as the rules see it: it remembers every frame
// published to every room, in order, every start and every end, and resets
// the state on a start as the relay does.
type fakePublisher struct {
	mu     sync.Mutex
	frames map[string][]any
	starts map[string][]Started
	ends   map[string][]Ended
	state  map[string]StateView
}

func newPublisher() *fakePublisher {
	return &fakePublisher{
		frames: map[string][]any{}, starts: map[string][]Started{}, ends: map[string][]Ended{},
		state: map[string]StateView{},
	}
}

func (p *fakePublisher) Publish(_ context.Context, roomID string, frames ...any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frames[roomID] = append(p.frames[roomID], frames...)
	return nil
}

func (p *fakePublisher) Start(_ context.Context, roomID string, started Started) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.state, roomID)
	p.frames[roomID] = append(p.frames[roomID], started)
	p.starts[roomID] = append(p.starts[roomID], started)
	return nil
}

// started is every start the relay was told of, in order.
func (p *fakePublisher) started(roomID string) []Started {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Started(nil), p.starts[roomID]...)
}

func (p *fakePublisher) End(_ context.Context, roomID string, ended Ended) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frames[roomID] = append(p.frames[roomID], ended)
	p.ends[roomID] = append(p.ends[roomID], ended)
	return nil
}

func (p *fakePublisher) State(_ context.Context, roomID string) (StateView, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state[roomID], nil
}

// published is the frames sent to a room, copied, and forgotten: each check
// reads what happened since the last.
func (p *fakePublisher) published(roomID string) []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.frames[roomID]
	p.frames[roomID] = nil
	return out
}

func (p *fakePublisher) ended(roomID string) []Ended {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Ended(nil), p.ends[roomID]...)
}

func (p *fakePublisher) setState(roomID string, version int, data string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state[roomID] = StateView{Version: version, Data: json.RawMessage(data)}
}

// kinds is each frame's type, in order.
func kinds(frames []any) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		b, _ := json.Marshal(f)
		var t struct {
			T string `json:"t"`
		}
		_ = json.Unmarshal(b, &t)
		out = append(out, t.T)
	}
	return out
}

// fakePresence is who has a socket open.
type fakePresence struct {
	mu    sync.Mutex
	state map[string]players.State
}

func (f *fakePresence) set(id string, st players.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == nil {
		f.state = map[string]players.State{}
	}
	f.state[id] = st
}

func (f *fakePresence) Of(_ context.Context, ids []string) (map[string]players.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]players.State, len(ids))
	for _, id := range ids {
		st, ok := f.state[id]
		if !ok {
			st = players.State{State: players.Offline}
		}
		out[id] = st
	}
	return out, nil
}
