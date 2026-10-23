package inbox

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// Retention is how long a notification is kept after it was last raised.
const Retention = 90 * 24 * time.Hour

// Inbox is every player's notifications, in Postgres. Every query joins the
// transaction its context carries, so a friend's request and the
// notification it raises are one write.
type Inbox struct {
	db *gorm.DB

	// Files signs the actors' avatars and the games' icons on the way out;
	// AvatarTTL is how long an avatar's link lives. Nil Files is a
	// deployment with bundled avatars only and no icons.
	Files     files.Files
	AvatarTTL time.Duration

	// Audit is the players area of the audit log: the console's
	// announcements are written to it. Nil writes nothing.
	Audit audit.Log

	// Pusher carries every notification on to its recipient's phones once
	// the write has committed, whatever raised it. Nil pushes nothing.
	Pusher Pusher
}

func New(db *gorm.DB) *Inbox { return &Inbox{db: db} }

// Pusher takes notifications whose write has committed to their recipients'
// phones (docs/PUSH.md). It is handed ids, never waits, and never fails the
// write: a push missed is a row still in the inbox. *push.Service is one.
type Pusher interface {
	Push(ids ...string)
}

// push hands notifications to the Pusher once the transaction ctx carries
// commits, or at once outside one; never if it rolls back.
func (i *Inbox) push(ctx context.Context, ids ...string) {
	if i.Pusher == nil || len(ids) == 0 {
		return
	}
	data.AfterCommit(ctx, func() { i.Pusher.Push(ids...) })
}

// Notice is one notification to raise. An empty ActorID, GameID or Key is
// none: the platform's own notifications have no game, and one without a
// key is always a new row.
type Notice struct {
	PlayerID string
	Kind     enum.NoticeKind
	ActorID  string
	GameID   string
	Title    string
	Body     string
	DeepLink string
	Data     map[string]any
	Key      string
}

// Raised is what a raise did: the notification's id, and whether it is a new
// row or one already there with the key, updated in place.
type Raised struct {
	ID      string
	Created bool
}

// Raise puts a notification in the recipient's inbox. A key already there
// for the recipient and the game updates that row instead — its words, link,
// data and actor — and brings it back to the top, unread, keeping its id.
// Either way it is pushed to the recipient's phones once the write commits.
func (i *Inbox) Raise(ctx context.Context, n Notice) (*Raised, error) {
	raw := []byte("{}")
	if len(n.Data) > 0 {
		b, err := json.Marshal(n.Data)
		if err != nil {
			return nil, errors.Internal(err)
		}
		raw = b
	}
	id := ids.New(ids.Notification)
	var got []string
	err := data.Conn(ctx, i.db).Raw(`
		INSERT INTO notifications (id, player_id, kind, actor_id, game_id, title, body, deep_link, data, key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (player_id, (COALESCE(game_id, '')), key) WHERE key IS NOT NULL
		DO UPDATE SET title = EXCLUDED.title, body = EXCLUDED.body, deep_link = EXCLUDED.deep_link,
		              data = EXCLUDED.data, actor_id = EXCLUDED.actor_id,
		              created_at = EXCLUDED.created_at, read_at = NULL
		RETURNING id`,
		id, n.PlayerID, n.Kind, nullable(n.ActorID), nullable(n.GameID), n.Title, n.Body, n.DeepLink,
		string(raw), nullable(n.Key), data.Now()).Scan(&got).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if len(got) != 1 {
		return nil, errors.Internal(errNoRow)
	}
	i.push(ctx, got[0])
	return &Raised{ID: got[0], Created: got[0] == id}, nil
}

// Withdraw deletes the notification a key names, if it is there: a request
// cancelled or declined takes its notification with it. An empty game is the
// platform's.
func (i *Inbox) Withdraw(ctx context.Context, playerID, gameID, key string) error {
	err := data.Conn(ctx, i.db).Exec(`
		DELETE FROM notifications WHERE player_id = ? AND COALESCE(game_id, '') = ? AND key = ?`,
		playerID, gameID, key).Error
	if err != nil {
		return errors.Internal(err)
	}
	return nil
}

// Settle marks the notification a key names read, if it is there and
// unread: the player answered what it asked, wherever they answered it.
func (i *Inbox) Settle(ctx context.Context, playerID, gameID, key string) error {
	err := data.Conn(ctx, i.db).Exec(`
		UPDATE notifications SET read_at = ?
		WHERE player_id = ? AND COALESCE(game_id, '') = ? AND key = ? AND read_at IS NULL`,
		data.Now(), playerID, gameID, key).Error
	if err != nil {
		return errors.Internal(err)
	}
	return nil
}

// Sweep deletes what was last raised before the cutoff, and says how many.
func (i *Inbox) Sweep(ctx context.Context, before time.Time) (int64, error) {
	res := data.Conn(ctx, i.db).Exec(`DELETE FROM notifications WHERE created_at < ?`, before)
	if res.Error != nil {
		return 0, errors.Internal(res.Error)
	}
	return res.RowsAffected, nil
}

// nullable is SQL NULL for an empty string: no actor, no game, no key.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// errNoRow is an upsert that answered nothing, which RETURNING never does.
var errNoRow = stderrors.New("inbox: the raise returned no row")
