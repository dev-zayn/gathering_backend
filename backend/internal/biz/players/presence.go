package players

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Presence is who is here right now, in Redis with a short life: the app
// heartbeats while it is in the foreground, the relay refreshes while a
// socket is open, and silence is offline. Nothing is written to Postgres.
type Presence struct {
	rdb *data.Redis
	ttl time.Duration
}

func NewPresence(rdb *data.Redis, ttl time.Duration) *Presence {
	return &Presence{rdb: rdb, ttl: ttl}
}

// State is one player's presence as friends read it.
type State struct {
	State string `json:"state"`
	Room  string `json:"room,omitempty"`
}

const (
	Online  = "online"
	InRoom  = "in_room"
	Offline = "offline"
)

func key(id string) string { return "presence:" + id }

// Heartbeat says the app is open. It never downgrades a player the relay has
// in a room: the room's own heartbeat outranks the app's.
func (p *Presence) Heartbeat(ctx context.Context, id string) error {
	cur, err := p.rdb.Get(ctx, key(id)).Result()
	if err == nil && strings.Contains(cur, `"`+InRoom+`"`) {
		return p.rdb.Expire(ctx, key(id), p.ttl).Err()
	}
	return p.Mark(ctx, id, Online, "")
}

// Mark sets the state outright: the relay's, on connect and every ping.
func (p *Presence) Mark(ctx context.Context, id, state, room string) error {
	b, err := json.Marshal(State{State: state, Room: room})
	if err != nil {
		return errors.Internal(err)
	}
	if err := p.rdb.Set(ctx, key(id), b, p.ttl).Err(); err != nil {
		return errors.Internal(err)
	}
	return nil
}

// Clear is a sign-out or a closed socket: offline at once, not after the TTL.
func (p *Presence) Clear(ctx context.Context, id string) error {
	if err := p.rdb.Del(ctx, key(id)).Err(); err != nil {
		return errors.Internal(err)
	}
	return nil
}

// Of reads many at once — a friends list is one round trip. A player with no
// key is offline.
func (p *Presence) Of(ctx context.Context, ids []string) (map[string]State, error) {
	out := make(map[string]State, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = key(id)
	}
	vals, err := p.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, errors.Internal(err)
	}
	for i, v := range vals {
		s, ok := v.(string)
		if !ok {
			out[ids[i]] = State{State: Offline}
			continue
		}
		var st State
		if json.Unmarshal([]byte(s), &st) != nil || st.State == "" {
			st = State{State: Offline}
		}
		out[ids[i]] = st
	}
	return out, nil
}

// Online counts everybody here right now, for the console's first page. A
// scan, which is fine for a number read by a handful of admins.
func (p *Presence) Online(ctx context.Context) (int, error) {
	var n int
	iter := p.rdb.Scan(ctx, 0, "presence:*", 1000).Iterator()
	for iter.Next(ctx) {
		n++
	}
	if err := iter.Err(); err != nil {
		return 0, errors.Internal(err)
	}
	return n, nil
}
