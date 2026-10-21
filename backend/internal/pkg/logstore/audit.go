package logstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Entry is one line of the audit trail: who did what to which thing. One table
// with an area column, rather than three to merge on every read.
type Entry struct {
	At      time.Time
	ID      string
	Area    string
	Actor   string
	Action  string
	Subject string
	Detail  map[string]any
}

// Query narrows the log. Every field is optional; an empty query is the whole
// of one area's log, newest first.
type Query struct {
	Actor   string
	Action  string
	Subject string

	// Actions is any of these, for a reader that wants a set rather than one.
	Actions []string

	// Search is one box over the whole line, because an admin looking has half an
	// app id or a word from a reason, not the column it belongs to.
	Search string

	// Area narrows a reader opened over the whole log. One opened for an area
	// reads that area whatever this says.
	Area string

	Offset int
	Limit  int
}

// Audit is one area's view of the shared table, reading and writing only its own
// name so it cannot see or corrupt another's history. Opened with no area it
// is the console's reader over all of them, and it never writes.
type Audit struct {
	db   *gorm.DB
	area string
}

func NewAudit(db *gorm.DB, area string) *Audit {
	return &Audit{db: db, area: area}
}

// NewReader spans every area. It is for reading the log back, so Write refuses.
func NewReader(db *gorm.DB) *Audit {
	return &Audit{db: db}
}

// auditRow is a line as the table holds it. Detail is the JSON text; Entry
// carries it parsed.
type auditRow struct {
	ID      string `gorm:"primaryKey"`
	At      time.Time
	Area    string
	Actor   string
	Action  string
	Subject string
	Detail  string `gorm:"type:jsonb"`
}

func (auditRow) TableName() string { return "logs.audit" }

func (a *Audit) Write(ctx context.Context, e Entry) error {
	if a.area == "" {
		return fmt.Errorf("logstore: a reader over every area cannot write")
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	detail, err := json.Marshal(orEmpty(e.Detail))
	if err != nil {
		return fmt.Errorf("logstore: detail is not JSON: %w", err)
	}

	return a.db.WithContext(ctx).Create(&auditRow{
		ID: e.ID, At: e.At.UTC(), Area: a.area, Actor: clip(e.Actor, 64),
		Action: e.Action, Subject: clip(e.Subject, 64), Detail: string(detail),
	}).Error
}

func (a *Audit) List(ctx context.Context, q Query) ([]Entry, error) {
	query := a.filtered(ctx, q)
	if area := a.areaOf(q); area != "" {
		query = query.Where("area = ?", area)
	}

	limit := LimitOf(q.Limit)
	var rows []auditRow
	err := query.Order("at DESC, id DESC").Limit(limit).Offset(max(q.Offset, 0)).Find(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{At: r.At, ID: r.ID, Area: r.Area, Actor: r.Actor, Action: r.Action, Subject: r.Subject}
		// A line we cannot parse is still a line that happened. Losing the
		// detail is bad; losing the row would be worse.
		_ = json.Unmarshal([]byte(r.Detail), &e.Detail)
		e.Detail = orEmpty(e.Detail)
		out = append(out, e)
	}
	return out, nil
}

// Counts is how many lines match, per area, ignoring the area asked for: the
// console draws every tab's number from one answer.
func (a *Audit) Counts(ctx context.Context, q Query) (map[string]int, error) {
	query := a.filtered(ctx, q)
	if a.area != "" {
		query = query.Where("area = ?", a.area)
	}

	var rows []struct {
		Area string
		N    int64
	}
	if err := query.Select("area, COUNT(*) AS n").Group("area").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Area] = int(r.N)
	}
	return out, nil
}

// areaOf is the one the query is bound to: the store's own, or the one asked
// for on a reader over all of them.
func (a *Audit) areaOf(q Query) string {
	if a.area != "" {
		return a.area
	}
	return q.Area
}

// filtered is the table narrowed by every condition but the area, shared by
// the page and its counts so the two cannot disagree.
func (a *Audit) filtered(ctx context.Context, q Query) *gorm.DB {
	query := a.db.WithContext(ctx).Model(&auditRow{})
	if q.Actor != "" {
		query = query.Where("actor = ?", q.Actor)
	}
	if q.Action != "" {
		query = query.Where("action = ?", q.Action)
	}
	if len(q.Actions) > 0 {
		query = query.Where("action IN ?", q.Actions)
	}
	if q.Subject != "" {
		query = query.Where("subject = ?", q.Subject)
	}
	// Case-insensitive substring over four columns, which reads every row the
	// other conditions leave. Fine while a year of the log is megabytes; a
	// pg_trgm index is the answer if it stops being.
	if term := strings.TrimSpace(q.Search); term != "" {
		pattern := contains(term)
		query = query.Where(a.db.Where("subject ILIKE ?", pattern).
			Or("actor ILIKE ?", pattern).
			Or("action ILIKE ?", pattern).
			Or("detail::text ILIKE ?", pattern))
	}
	return query
}
