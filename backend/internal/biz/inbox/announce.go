package inbox

// Announcements: one message from the platform to many players at once, sent
// from the console. Each recipient gets a system notification of their own,
// written in the same transaction as the announcement; the audit log names
// the admin. docs/CONSOLE.md, "Announcements", is the contract.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// The kinds of audience: everybody, a game's players, or players by name.
const (
	AudienceAll     = "all"
	AudienceGame    = "game"
	AudiencePlayers = "players"
)

// The limits an announcement is held to.
const (
	maxTitle        = 60
	maxBody         = 240
	maxRecipients   = 500 // players named in one players audience
	announcePage    = 100 // the longest page of the console's list
	announceBatches = 500 // notification rows per insert
)

// AnnouncementAudience is who an announcement is for, as the console sends
// it and as the list shows it back.
type AnnouncementAudience struct {
	Kind    string   `json:"kind" example:"players" doc:"all (every player not switched off), game (every player not switched off with the game on their shelf), or players (those named)."`
	Game    string   `json:"game,omitempty" example:"fixture.xo" doc:"For kind game: the game whose players hear it."`
	Players []string `json:"players,omitempty" example:"[\"@zayn_4821\", \"ply_7f3k9d2m8q1x4b6n\"]" doc:"For kind players: up to 500 player ids or @handles. Unknown and switched-off players are skipped."`
}

// Announce is an announcement to send, or to count.
type Announce struct {
	Audience *AnnouncementAudience
	Title    string
	Body     string
	// Game is the game the notification opens, its store page in the app;
	// empty for none.
	Game string
	// DryRun counts who it would reach and writes nothing.
	DryRun bool
}

// Announced is what a send did: the announcement's id (empty on a dry run)
// and how many players it reached.
type Announced struct {
	ID    string
	Reach int
}

// Announce sends an admin's announcement, or with DryRun counts who it would
// reach. Every problem comes back at once. A send writes the announcement and
// one system notification per recipient in one transaction, then the audit
// line; once it has committed, each notification is pushed to its
// recipient's phones.
func (i *Inbox) Announce(ctx context.Context, adminID string, in Announce) (*Announced, error) {
	title, body, game := strings.TrimSpace(in.Title), strings.TrimSpace(in.Body), strings.TrimSpace(in.Game)
	bad := map[string]string{}
	switch n := utf8.RuneCountInString(title); {
	case n == 0:
		bad["title"] = "required"
	case n > maxTitle:
		bad["title"] = "too_long"
	}
	if utf8.RuneCountInString(body) > maxBody {
		bad["body"] = "too_long"
	}
	if game != "" {
		found, err := i.gameExists(ctx, game)
		if err != nil {
			return nil, err
		}
		if !found {
			bad["game"] = "not_found"
		}
	}

	audience, err := i.checkAudience(ctx, in.Audience, bad)
	if err != nil {
		return nil, err
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	if in.DryRun {
		reach, err := i.reach(ctx, audience)
		if err != nil {
			return nil, err
		}
		return &Announced{Reach: reach}, nil
	}

	sent, err := json.Marshal(audience.sent)
	if err != nil {
		return nil, errors.Internal(err)
	}
	ann := &model.Announcement{
		ID: ids.New(ids.Announcement), AdminID: adminID, Audience: sent, Title: title, Body: body,
		CreatedAt: data.Now(),
	}
	payload := map[string]any{"announcement": ann.ID}
	if game != "" {
		ann.GameID = &game
		payload["game"] = game
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Internal(err)
	}

	err = data.NewTx(i.db).Do(ctx, func(ctx context.Context) error {
		recipients, err := i.recipients(ctx, audience)
		if err != nil {
			return err
		}
		ann.Reach = len(recipients)
		if err := data.Conn(ctx, i.db).Create(ann).Error; err != nil {
			return err
		}
		if len(recipients) == 0 {
			return nil
		}
		key := "announcement:" + ann.ID
		rows := make([]model.Notification, 0, len(recipients))
		written := make([]string, 0, len(recipients))
		for _, p := range recipients {
			row := model.Notification{
				ID: ids.New(ids.Notification), PlayerID: p, Kind: enum.NoticeSystem,
				Title: title, Body: body, Data: raw, Key: &key, CreatedAt: ann.CreatedAt,
			}
			rows = append(rows, row)
			written = append(written, row.ID)
		}
		if err := data.Conn(ctx, i.db).CreateInBatches(rows, announceBatches).Error; err != nil {
			return err
		}
		i.push(ctx, written...)
		return nil
	})
	if err != nil {
		return nil, errors.Internal(err)
	}

	i.record(ctx, adminID, model.ActionNotificationSent, ann.ID, map[string]any{
		"audience": audience.sent.Kind, "reach": ann.Reach,
	})
	return &Announced{ID: ann.ID, Reach: ann.Reach}, nil
}

// audience is an audience once checked: as it was sent, and for a players
// audience the ids and handles its refs name.
type audience struct {
	sent    AnnouncementAudience
	ids     []string
	handles []string
}

// checkAudience reads the audience into bad as refusals, and answers it
// checked. A players audience that names nobody who may hear it is refused
// here too, so it comes back with every other problem.
func (i *Inbox) checkAudience(ctx context.Context, in *AnnouncementAudience, bad map[string]string) (audience, error) {
	if in == nil || strings.TrimSpace(in.Kind) == "" {
		bad["audience"] = "required"
		return audience{}, nil
	}
	out := audience{sent: AnnouncementAudience{Kind: strings.TrimSpace(in.Kind)}}
	switch out.sent.Kind {
	case AudienceAll:
	case AudienceGame:
		out.sent.Game = strings.TrimSpace(in.Game)
		found := false
		if out.sent.Game != "" {
			var err error
			if found, err = i.gameExists(ctx, out.sent.Game); err != nil {
				return audience{}, err
			}
		}
		if !found {
			bad["audience.game"] = "not_found"
		}
	case AudiencePlayers:
		seen := map[string]bool{}
		for _, ref := range in.Players {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			out.sent.Players = append(out.sent.Players, ref)
			// A ref shaped like an id is read as one; anything else is a
			// handle, its @ optional and its case ignored.
			key := ref
			if !ids.Is(ids.Player, ref) {
				key = strings.ToLower(strings.TrimPrefix(ref, "@"))
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if ids.Is(ids.Player, ref) {
				out.ids = append(out.ids, ref)
			} else {
				out.handles = append(out.handles, key)
			}
		}
		switch {
		case len(out.sent.Players) == 0:
			bad["audience.players"] = "required"
		case len(out.sent.Players) > maxRecipients:
			bad["audience.players"] = "up_to_500"
		default:
			n, err := i.reach(ctx, out)
			if err != nil {
				return audience{}, err
			}
			if n == 0 {
				bad["audience.players"] = "nobody"
			}
		}
	default:
		bad["audience.kind"] = "unknown"
	}
	return out, nil
}

// recipientsSQL is who an audience reaches, as one query: players who are not
// switched off, all of them, a game's, or the ones named.
func recipientsSQL(a audience, what string) (string, []any) {
	switch a.sent.Kind {
	case AudienceGame:
		return `SELECT ` + what + ` FROM players p JOIN library l ON l.player_id = p.id
		        WHERE l.game_id = ? AND NOT p.disabled`, []any{a.sent.Game}
	case AudiencePlayers:
		// An empty IN list is no match; a sentinel keeps the SQL well formed.
		named, handles := a.ids, a.handles
		if len(named) == 0 {
			named = []string{""}
		}
		if len(handles) == 0 {
			handles = []string{""}
		}
		return `SELECT ` + what + ` FROM players p WHERE NOT p.disabled AND (p.id IN ? OR p.handle IN ?)`,
			[]any{named, handles}
	default:
		return `SELECT ` + what + ` FROM players p WHERE NOT p.disabled`, nil
	}
}

// reach counts who an audience reaches now.
func (i *Inbox) reach(ctx context.Context, a audience) (int, error) {
	q, args := recipientsSQL(a, "count(*)")
	var n int64
	if err := data.Conn(ctx, i.db).Raw(q, args...).Scan(&n).Error; err != nil {
		return 0, errors.Internal(err)
	}
	return int(n), nil
}

// recipients is who an audience reaches, by id, read in the send's
// transaction so the reach is the rows it writes.
func (i *Inbox) recipients(ctx context.Context, a audience) ([]string, error) {
	q, args := recipientsSQL(a, "p.id")
	var out []string
	err := data.Conn(ctx, i.db).Raw(q+` ORDER BY p.id`, args...).Scan(&out).Error
	return out, err
}

// gameExists says whether a game is registered, in any listing.
func (i *Inbox) gameExists(ctx context.Context, id string) (bool, error) {
	var found bool
	err := data.Conn(ctx, i.db).Raw(`SELECT EXISTS (SELECT 1 FROM games WHERE id = ?)`, id).Scan(&found).Error
	if err != nil {
		return false, errors.Internal(err)
	}
	return found, nil
}

// record writes the audit line for a send already made. A log that is down
// must not refuse the admin a send that happened, so a failure is logged.
func (i *Inbox) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if i.Audit == nil {
		return
	}
	if err := i.Audit.Write(ctx, model.Entry{Actor: actor, Action: action, Subject: subject, Detail: detail}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "subject", subject, "err", err)
	}
}

// IDName is something named by its id and its name: the game an announcement
// opens, the admin who sent it.
type IDName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Announcement is one announcement as the console lists it.
type Announcement struct {
	ID        string               `json:"id" example:"ann_7f3k9d2m8q1x4b6n"`
	Title     string               `json:"title" example:"صيانة الليلة"`
	Body      string               `json:"body"`
	Game      *IDName              `json:"game" doc:"The game the notification opens, its store page in the app; null for none."`
	Audience  AnnouncementAudience `json:"audience" doc:"As it was sent."`
	Reach     int                  `json:"reach" doc:"How many players it was written for."`
	Admin     IDName               `json:"admin" doc:"Who sent it."`
	CreatedAt string               `json:"created_at"`
}

// Announcements is one page of the announcements sent.
type Announcements struct {
	Items      []Announcement `json:"items" doc:"Newest first."`
	NextCursor *string        `json:"next_cursor" nullable:"true" doc:"Hand it back as cursor for the next page; null on the last."`
}

// announcementRow is an announcement with its game's and its admin's names.
type announcementRow struct {
	model.Announcement
	GameName  *string
	AdminName *string
}

// Announcements is what was sent, newest first (created_at descending, then
// id), a page at a time.
func (i *Inbox) Announcements(ctx context.Context, cursor string, limit int) (*Announcements, error) {
	bad := map[string]string{}
	if limit < 1 || limit > announcePage {
		bad["limit"] = "1_to_100"
	}
	after, ok := readAnnouncementCursor(cursor)
	if !ok {
		bad["cursor"] = "invalid"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	q := `
		SELECT a.*, g.name AS game_name, ad.name AS admin_name
		FROM announcements a
		LEFT JOIN games g ON g.id = a.game_id
		LEFT JOIN admins ad ON ad.id = a.admin_id`
	var args []any
	if after != nil {
		q += ` WHERE a.created_at < ? OR (a.created_at = ? AND a.id > ?)`
		args = append(args, after.at, after.at, after.id)
	}
	q += ` ORDER BY a.created_at DESC, a.id LIMIT ?`
	args = append(args, limit+1)

	var rows []announcementRow
	if err := data.Conn(ctx, i.db).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, errors.Internal(err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	out := &Announcements{Items: make([]Announcement, 0, len(rows))}
	for _, r := range rows {
		a := Announcement{
			ID: r.ID, Title: r.Title, Body: r.Body, Reach: r.Reach,
			Admin: IDName{ID: r.AdminID, Name: deref(r.AdminName)}, CreatedAt: stamp(r.CreatedAt),
		}
		// What was sent is what the console wrote; one that cannot be read
		// back still lists, with its kind empty.
		_ = json.Unmarshal(r.Audience, &a.Audience)
		if r.GameID != nil {
			a.Game = &IDName{ID: *r.GameID, Name: deref(r.GameName)}
		}
		out.Items = append(out.Items, a)
	}
	if more {
		last := rows[len(rows)-1]
		next := announcementCursor(last.CreatedAt, last.ID)
		out.NextCursor = &next
	}
	return out, nil
}

// announcementCursor is the cursor after an announcement: opaque to the
// console, its time and id underneath.
func announcementCursor(at time.Time, id string) string {
	raw := strconv.FormatInt(at.UnixMicro(), 10) + "." + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// readAnnouncementCursor is a cursor the list wrote, or not one at all.
// Empty is the first page.
func readAnnouncementCursor(s string) (*position, bool) {
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
	if !found || !ids.Is(ids.Announcement, id) {
		return nil, false
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || n < 0 {
		return nil, false
	}
	return &position{at: time.UnixMicro(n).UTC(), id: id}, true
}
