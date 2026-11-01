// Package reports is a player reporting another, and an admin reading the
// reports (docs/REPORTS.md).
package reports

import (
	"context"
	"encoding/base64"
	stderrors "errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

const (
	hourly       = 20  // reports one player may file in an hour
	consoleLimit = 100 // the most one page of the console's list holds
	maxCursor    = 128
)

var (
	errNotFound = errors.NotFound("not_found")
	errResolved = errors.Conflict("report_resolved")
)

type Service struct {
	DB      *gorm.DB
	Limiter identity.Limiter
	Audit   audit.Log
}

// File is a player reporting another. Reporting again while the first is open
// replaces its reason and note, and keeps its place in the list. The reported
// player is never told.
func (s *Service) File(ctx context.Context, reporterID, playerID, reason, note string) error {
	note = strings.TrimSpace(note)
	bad := map[string]string{}
	switch {
	case reason == "":
		bad["reason"] = "required"
	case !enum.ReportReason(reason).Valid():
		bad["reason"] = "unknown"
	}
	if utf8.RuneCountInString(note) > model.MaxReportNote {
		bad["note"] = "too_long"
	}
	if reporterID == playerID {
		bad["player"] = "is_you"
	}
	if len(bad) > 0 {
		return errors.Invalid(bad)
	}

	if !ids.Is(ids.Player, playerID) {
		return errNotFound
	}
	var there int64
	err := data.Conn(ctx, s.DB).Model(&model.Player{}).
		Where("id = ? AND NOT disabled AND deleted_at IS NULL", playerID).Count(&there).Error
	if err != nil {
		return errors.Internal(err)
	}
	if there == 0 {
		return errNotFound
	}

	if s.Limiter != nil {
		ok, retry, err := s.Limiter.Allow(ctx, "report:"+reporterID, hourly, time.Hour)
		if err != nil {
			return errors.Internal(err)
		}
		if !ok {
			return errors.TooManyRequests(retry)
		}
	}

	row := &model.Report{
		ID: ids.New(ids.Report), ReporterID: reporterID, PlayerID: playerID,
		Reason: enum.ReportReason(reason), Note: note,
		Status: enum.ReportOpen, CreatedAt: data.Now(),
	}
	err = data.Conn(ctx, s.DB).Clauses(clause.OnConflict{
		Columns:     []clause.Column{{Name: "reporter_id"}, {Name: "player_id"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "status = 'open'"}}},
		DoUpdates:   clause.AssignmentColumns([]string{"reason", "note"}),
	}).Create(row).Error
	if err != nil {
		return errors.Internal(err)
	}
	s.record(ctx, reporterID, model.ActionReportFiled, playerID, map[string]any{"reason": reason})
	return nil
}

// ReportPerson is who a report names, or who filed it.
type ReportPerson struct {
	ID       string `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname string `json:"nickname"`
	Handle   string `json:"handle"`
}

// ReportedPlayer is who a report names, with what the console needs to judge.
type ReportedPlayer struct {
	ReportPerson
	Avatar   string `json:"avatar" doc:"A bundled avatar id, or a stored picture's key, or empty."`
	Disabled bool   `json:"disabled"`
}

// ConsoleReport is one report as the console shows it.
type ConsoleReport struct {
	ID         string         `json:"id" example:"rpt_7f3k9d2m8q1x4b6n"`
	Reason     string         `json:"reason" enum:"name,photo,harassment,cheating,other"`
	Note       string         `json:"note"`
	Status     string         `json:"status" enum:"open,resolved"`
	CreatedAt  string         `json:"created_at"`
	ResolvedAt *string        `json:"resolved_at" nullable:"true"`
	ResolvedBy *string        `json:"resolved_by" nullable:"true" doc:"The admin's name."`
	Reporter   ReportPerson   `json:"reporter"`
	Player     ReportedPlayer `json:"player"`
	Against    int            `json:"against" doc:"How many open reports name this player."`
}

// ConsoleReports is one page of the list.
type ConsoleReports struct {
	Items      []ConsoleReport `json:"items"`
	NextCursor *string         `json:"next_cursor" nullable:"true"`
	Open       int64           `json:"open" doc:"Open reports across the platform, whatever the filters."`
}

type consoleRow struct {
	model.Report
	ReporterNickname string
	ReporterHandle   string
	PlayerNickname   string
	PlayerHandle     string
	PlayerAvatar     string
	PlayerDisabled   bool
	AdminName        *string
	Against          int
}

// List is the console's: newest first, the open ones unless asked otherwise.
func (s *Service) List(ctx context.Context, status, cursor string, limit int) (*ConsoleReports, error) {
	bad := map[string]string{}
	where, args := "", []any{}
	switch status {
	case "", string(enum.ReportOpen):
		where, args = "r.status = ?", []any{enum.ReportOpen}
	case string(enum.ReportResolved):
		where, args = "r.status = ?", []any{enum.ReportResolved}
	case "all":
		where = "TRUE"
	default:
		bad["status"] = "unknown"
	}
	if limit < 1 || limit > consoleLimit {
		bad["limit"] = "1_to_100"
	}
	at, id, ok := readCursor(cursor)
	if !ok {
		bad["cursor"] = "invalid"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	sql := `
		SELECT r.*,
		       rp.nickname AS reporter_nickname, rp.handle AS reporter_handle,
		       p.nickname AS player_nickname, p.handle AS player_handle,
		       p.avatar_key AS player_avatar, p.disabled AS player_disabled,
		       a.name AS admin_name,
		       (SELECT count(*) FROM reports o WHERE o.player_id = r.player_id AND o.status = 'open') AS against
		FROM reports r
		JOIN players rp ON rp.id = r.reporter_id
		JOIN players p ON p.id = r.player_id
		LEFT JOIN admins a ON a.id = r.resolved_by
		WHERE ` + where
	if id != "" {
		sql += ` AND (r.created_at < ? OR (r.created_at = ? AND r.id > ?))`
		args = append(args, at, at, id)
	}
	sql += ` ORDER BY r.created_at DESC, r.id LIMIT ?`
	args = append(args, limit+1)

	var rows []consoleRow
	db := data.Conn(ctx, s.DB)
	if err := db.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, errors.Internal(err)
	}

	out := &ConsoleReports{Items: []ConsoleReport{}}
	if err := db.Model(&model.Report{}).Where("status = ?", enum.ReportOpen).Count(&out.Open).Error; err != nil {
		return nil, errors.Internal(err)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next := writeCursor(last.CreatedAt, last.ID)
		out.NextCursor = &next
	}
	for _, r := range rows {
		item := ConsoleReport{
			ID: r.ID, Reason: string(r.Reason), Note: r.Note, Status: string(r.Status),
			CreatedAt:  stamp(r.CreatedAt),
			ResolvedBy: r.AdminName,
			Reporter:   ReportPerson{ID: r.ReporterID, Nickname: r.ReporterNickname, Handle: r.ReporterHandle},
			Player: ReportedPlayer{
				ReportPerson: ReportPerson{ID: r.PlayerID, Nickname: r.PlayerNickname, Handle: r.PlayerHandle},
				Avatar:       r.PlayerAvatar, Disabled: r.PlayerDisabled,
			},
			Against: r.Against,
		}
		if r.ResolvedAt != nil {
			at := stamp(*r.ResolvedAt)
			item.ResolvedAt = &at
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// Resolve records that an admin dealt with a report. What they did about it is
// theirs to do on the player's page.
func (s *Service) Resolve(ctx context.Context, adminID, reportID string) error {
	if !ids.Is(ids.Report, reportID) {
		return errNotFound
	}
	err := data.NewTx(s.DB).Do(ctx, func(ctx context.Context) error {
		var row model.Report
		err := data.Conn(ctx, s.DB).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", reportID).Take(&row).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if row.Status == enum.ReportResolved {
			return errResolved
		}
		return data.Conn(ctx, s.DB).Model(&model.Report{}).Where("id = ?", reportID).Updates(map[string]any{
			"status": enum.ReportResolved, "resolved_at": data.Now(), "resolved_by": adminID,
		}).Error
	})
	var e *errors.Error
	switch {
	case stderrors.As(err, &e):
		return e
	case err != nil:
		return errors.Internal(err)
	}
	s.record(ctx, adminID, model.ActionReportResolved, reportID, nil)
	return nil
}

// record never fails what it describes.
func (s *Service) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Write(ctx, model.Entry{Actor: actor, Action: action, Subject: subject, Detail: detail}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "err", err)
	}
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func writeCursor(at time.Time, id string) string {
	raw := strconv.FormatInt(at.UnixMicro(), 10) + "." + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// readCursor is a cursor the list wrote, or not one at all. Empty is the first
// page, which comes back with no id.
func readCursor(s string) (time.Time, string, bool) {
	if s == "" {
		return time.Time{}, "", true
	}
	if len(s) > maxCursor {
		return time.Time{}, "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", false
	}
	micros, id, found := strings.Cut(string(raw), ".")
	if !found || !ids.Is(ids.Report, id) {
		return time.Time{}, "", false
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || n < 0 {
		return time.Time{}, "", false
	}
	return time.UnixMicro(n).UTC(), id, true
}
