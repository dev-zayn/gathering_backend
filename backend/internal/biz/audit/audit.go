package audit

import (
	"context"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	"github.com/jamaa/gathering/backend/internal/pkg/logstore"
)

// Log is append-only, written before the caller is told the thing happened. An
// interface, because a log that is down must not refuse a player anything.
type Log interface {
	Write(ctx context.Context, e model.Entry) error

	// List reads it back: this area's lines, newest first. Which admin disabled
	// a player, and when, is answered here.
	List(ctx context.Context, q model.Query) ([]model.Entry, error)
}

// Reader is the console's view: every area at once, a page at a time, with a
// count per area so the tabs can say how many lines each holds.
type Reader interface {
	List(ctx context.Context, q model.Query) ([]model.Entry, error)
	Counts(ctx context.Context, q model.Query) (map[string]int, error)
}

// The areas the log is partitioned by. Every write is scoped to one, so a
// game's history and a player's do not mix even sharing a table.
const (
	AreaAuth    = "auth"
	AreaPlayers = "players"
	AreaGames   = "games"
	AreaRooms   = "rooms"
	AreaAdmin   = "admin"
)

var Areas = []string{AreaAuth, AreaPlayers, AreaGames, AreaRooms, AreaAdmin}

// storeLog is where the log lives: the logs schema, on the log store's own pool.
type storeLog struct{ store *logstore.Audit }

// New opens the log for one area.
func New(db *gorm.DB, area string) Log {
	return &storeLog{store: logstore.NewAudit(db, area)}
}

// NewReader opens the whole log, to read.
func NewReader(db *gorm.DB) Reader {
	return &storeLog{store: logstore.NewReader(db)}
}

func (c *storeLog) Write(ctx context.Context, e model.Entry) error {
	return c.store.Write(ctx, logstore.Entry{
		At: e.At, ID: ids.New(ids.Audit), Actor: e.Actor,
		Action: e.Action, Subject: e.Subject, Detail: e.Detail,
	})
}

func (c *storeLog) List(ctx context.Context, q model.Query) ([]model.Entry, error) {
	entries, err := c.store.List(ctx, query(q))
	if err != nil {
		return nil, err
	}

	out := make([]model.Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, model.Entry{
			ID: e.ID, At: e.At, Area: e.Area, Actor: e.Actor,
			Action: e.Action, Subject: e.Subject, Detail: e.Detail,
		})
	}
	return out, nil
}

func (c *storeLog) Counts(ctx context.Context, q model.Query) (map[string]int, error) {
	return c.store.Counts(ctx, query(q))
}

func query(q model.Query) logstore.Query {
	return logstore.Query{
		Actor: q.Actor, Action: q.Action, Actions: q.Actions, Subject: q.Subject, Search: q.Search,
		Area: q.Area, Offset: q.Offset, Limit: q.Limit,
	}
}
