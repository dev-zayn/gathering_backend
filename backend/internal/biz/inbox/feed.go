package inbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// Notification is one line of the inbox as the app draws it.
type Notification struct {
	ID        string         `json:"id" example:"ntf_7f3k9d2m8q1x4b6n"`
	Kind      string         `json:"kind" enum:"friend_request,friend_accepted,room_invite,game,system"`
	Title     string         `json:"title" example:"دعوة للعب"`
	Body      string         `json:"body"`
	DeepLink  string         `json:"deep_link" doc:"/r/{code} for an invite, a path inside the game for a game's; empty for a request or an accept (the app opens the actor's card)."`
	Data      map[string]any `json:"data" doc:"{room, code, game} for an invite, {player} for a request or an accept, {} otherwise."`
	Actor     *players.Card  `json:"actor" doc:"Who caused it, when a player did."`
	Game      *games.Tile    `json:"game" doc:"The game it is from, or the room's game for an invite."`
	CreatedAt string         `json:"created_at" doc:"When it was raised, or raised again."`
	Read      bool           `json:"read"`
}

// Feed is one page of a player's inbox.
type Feed struct {
	Items      []Notification `json:"items" doc:"Newest first."`
	NextCursor *string        `json:"next_cursor" nullable:"true" doc:"Hand it back as cursor for the next page; null on the last."`
	Unread     int            `json:"unread" doc:"The player's whole unread count, whatever the page."`
}

// The longest page a client may ask for, and the most ids one mark may
// name. The default page, 20, is the route's.
const (
	maxLimit   = 50
	maxReadIDs = 100
)

// Feed is the player's notifications, newest first: created_at descending,
// then id, the order of the notifications_feed index. cursor is the
// next_cursor of the page before, or empty for the first page; unreadOnly
// leaves out what was read.
func (i *Inbox) Feed(ctx context.Context, playerID, cursor string, limit int, unreadOnly bool) (*Feed, error) {
	bad := map[string]string{}
	if limit < 1 || limit > maxLimit {
		bad["limit"] = "1_to_50"
	}
	after, ok := readCursor(cursor)
	if !ok {
		bad["cursor"] = "invalid"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	q := `
		SELECT n.id, n.kind, n.title, n.body, n.deep_link, n.data, n.actor_id, n.game_id, n.created_at, n.read_at,
		       a.nickname AS actor_nickname, a.handle AS actor_handle, a.avatar_key AS actor_avatar_key,
		       g.name AS game_name, g.icon_key AS game_icon_key
		FROM notifications n
		LEFT JOIN players a ON a.id = n.actor_id
		LEFT JOIN games g ON g.id = n.game_id
		WHERE n.player_id = ?`
	args := []any{playerID}
	if unreadOnly {
		q += ` AND n.read_at IS NULL`
	}
	if after != nil {
		q += ` AND (n.created_at < ? OR (n.created_at = ? AND n.id > ?))`
		args = append(args, after.at, after.at, after.id)
	}
	q += ` ORDER BY n.created_at DESC, n.id LIMIT ?`
	args = append(args, limit+1)

	var rows []feedRow
	if err := data.Conn(ctx, i.db).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, errors.Internal(err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}

	out := &Feed{Items: make([]Notification, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, i.draw(ctx, r))
	}
	if more {
		last := rows[len(rows)-1]
		next := cursorOf(last.CreatedAt, last.ID)
		out.NextCursor = &next
	}
	unread, err := i.Unread(ctx, playerID)
	if err != nil {
		return nil, err
	}
	out.Unread = unread
	return out, nil
}

// Unread is how many of the player's notifications are unread: the badge.
func (i *Inbox) Unread(ctx context.Context, playerID string) (int, error) {
	var n int64
	err := data.Conn(ctx, i.db).Model(&model.Notification{}).
		Where("player_id = ? AND read_at IS NULL", playerID).Count(&n).Error
	if err != nil {
		return 0, errors.Internal(err)
	}
	return int(n), nil
}

// Read marks the player's notifications read: the ones named, or every one.
// An id that is not the player's is passed over; one already read keeps the
// time it was read at.
func (i *Inbox) Read(ctx context.Context, playerID string, which []string, all bool) error {
	q := data.Conn(ctx, i.db).Model(&model.Notification{}).
		Where("player_id = ? AND read_at IS NULL", playerID)
	if !all {
		switch {
		case len(which) == 0:
			return errors.Invalid(map[string]string{"ids": "required"})
		case len(which) > maxReadIDs:
			return errors.Invalid(map[string]string{"ids": "up_to_100"})
		}
		q = q.Where("id IN ?", which)
	}
	if err := q.Update("read_at", data.Now()).Error; err != nil {
		return errors.Internal(err)
	}
	return nil
}

// feedRow is a notification with its actor's card and its game's, joined.
type feedRow struct {
	ID        string
	Kind      string
	Title     string
	Body      string
	DeepLink  string
	Data      model.Raw
	ActorID   *string
	GameID    *string
	CreatedAt time.Time
	ReadAt    *time.Time

	ActorNickname  *string
	ActorHandle    *string
	ActorAvatarKey *string
	GameName       *string
	GameIconKey    *string
}

// draw is a row as the app reads it, the pictures signed on the way out.
func (i *Inbox) draw(ctx context.Context, r feedRow) Notification {
	n := Notification{
		ID: r.ID, Kind: r.Kind, Title: r.Title, Body: r.Body, DeepLink: r.DeepLink,
		Data: map[string]any{}, CreatedAt: stamp(r.CreatedAt), Read: r.ReadAt != nil,
	}
	if len(r.Data) > 0 {
		_ = json.Unmarshal(r.Data, &n.Data)
		if n.Data == nil {
			n.Data = map[string]any{}
		}
	}
	if r.ActorID != nil && r.ActorNickname != nil {
		c := players.NewCard(ctx, i.Files, i.AvatarTTL, *r.ActorID, *r.ActorNickname, deref(r.ActorHandle), deref(r.ActorAvatarKey))
		n.Actor = &c
	}
	if r.GameID != nil {
		var g *model.Game
		if r.GameName != nil {
			g = &model.Game{ID: *r.GameID, Name: *r.GameName, IconKey: deref(r.GameIconKey)}
		}
		c := games.TileOf(ctx, i.Files, *r.GameID, g)
		n.Game = &c
	}
	return n
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// stamp is the one timestamp format this API answers in: UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// position is where a page starts: after this time, to the microsecond, and
// this notification.
type position struct {
	at time.Time
	id string
}

// maxCursor is longer than any cursor this package writes.
const maxCursor = 128

// cursorOf is the cursor after a row: opaque to the client, and nothing but
// the row's time and id underneath.
func cursorOf(at time.Time, id string) string {
	raw := strconv.FormatInt(at.UnixMicro(), 10) + "." + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// readCursor is a cursor this package wrote, or not one at all. Empty is the
// first page.
func readCursor(s string) (*position, bool) {
	if s == "" {
		return nil, true
	}
	if len(s) > maxCursor {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	micros, id, found := strings.Cut(string(raw), ".")
	if !found || !ids.Is(ids.Notification, id) {
		return nil, false
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || n < 0 {
		return nil, false
	}
	return &position{at: time.UnixMicro(n).UTC(), id: id}, true
}
