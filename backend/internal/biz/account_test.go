package biz

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

type clearedPresence struct{ ids []string }

func (c *clearedPresence) Clear(_ context.Context, id string) error {
	c.ids = append(c.ids, id)
	return nil
}

type deletionFixture struct {
	*Deletion
	db        *gorm.DB
	whitelist *biztest.Whitelist
	files     *biztest.Files
	presence  *clearedPresence
	log       *biztest.Audit
	issuer    *identity.Issuer
}

func deletion(t *testing.T) deletionFixture {
	t.Helper()
	db := biztest.DB(t)
	f := deletionFixture{
		db: db, whitelist: &biztest.Whitelist{}, files: biztest.NewFiles(),
		presence: &clearedPresence{}, log: &biztest.Audit{},
	}
	sessions := identity.NewSessionStore(db)
	f.issuer = &identity.Issuer{
		Sessions: sessions, Tokens: identity.NewRefreshStore(db),
		Minter: identity.NewMinter(biztest.Signer(t)), Whitelist: f.whitelist,
		SessionTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	}
	f.Deletion = &Deletion{
		DB: db, Sessions: sessions, Whitelist: f.whitelist,
		Presence: f.presence, Files: f.files, Audit: f.log,
	}
	return f
}

func count(t *testing.T, db *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func exec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatal(err)
	}
}

// Deleting takes everything that is the person's and leaves the row emptied,
// so the games they played still have somebody in that seat. Their friend
// keeps their own account, and the result they share stays.
func TestDeletingAnAccount(t *testing.T) {
	f := deletion(t)
	ctx := context.Background()
	now := data.Now()

	sara := biztest.SeedPlayer(t, f.db, "+9647700000001", "Sara")
	ali := biztest.SeedPlayer(t, f.db, "+9647700000002", "Ali")
	exec(t, f.db, `UPDATE players SET avatar_key = ?, bio = 'hi' WHERE id = ?`, "avatars/"+sara.ID+".png", sara.ID)
	if err := identity.NewPlayerStore(f.db).AddIdentity(ctx, &model.Identity{
		Provider: enum.ProviderGoogle, Subject: "g-1", PlayerID: sara.ID,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.issuer.Open(ctx, sara, ""); err != nil {
		t.Fatal(err)
	}
	session, err := identity.NewSessionStore(f.db).ListByPlayer(ctx, sara.ID)
	if err != nil || len(session) != 1 {
		t.Fatalf("sessions %v, %v", session, err)
	}
	if _, err := identity.NewDevices(f.db).Register(ctx, session[0].ID, sara.ID, enum.PlatformAndroid, "fcm-1"); err != nil {
		t.Fatal(err)
	}

	lo, hi := sara.ID, ali.ID
	if hi < lo {
		lo, hi = hi, lo
	}
	exec(t, f.db, `INSERT INTO friendships (player_lo, player_hi, status, requested_by, created_at)
	               VALUES (?, ?, 'accepted', ?, ?)`, lo, hi, ali.ID, now)
	exec(t, f.db, `INSERT INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)`, ali.ID, sara.ID, now)
	exec(t, f.db, `INSERT INTO notifications (id, player_id, kind, actor_id, title, created_at)
	               VALUES ('ntf_1', ?, 'friend_request', ?, 'x', ?), ('ntf_2', ?, 'friend_request', ?, 'x', ?),
	                      ('ntf_3', ?, 'system', NULL, 'x', ?)`,
		sara.ID, ali.ID, now, ali.ID, sara.ID, now, ali.ID, now)

	biztest.SeedGame(t, f.db, "studio.xo", model.Manifest{})
	biztest.SeedLibrary(t, f.db, sara.ID, "studio.xo")
	exec(t, f.db, `INSERT INTO game_tokens (hash, game_id, player_id) VALUES (?, 'studio.xo', ?)`,
		"0000000000000000000000000000000000000000000000000000000000000001", sara.ID)
	exec(t, f.db, `INSERT INTO rooms (id, code, game_id, host_id, status, config, ended_at)
	               VALUES ('room_1', 'K7M2QX', 'studio.xo', ?, 'ended', '{}', ?)`, sara.ID, now)
	exec(t, f.db, `INSERT INTO room_members (room_id, player_id, role, seat, left_at)
	               VALUES ('room_1', ?, 'host', 0, ?), ('room_1', ?, 'player', 1, ?)`, sara.ID, now, ali.ID, now)
	exec(t, f.db, `INSERT INTO results (room_id, player_id, game_id, code, seat, score, rank, players, finished_at, submitted_by)
	               VALUES ('room_1', ?, 'studio.xo', 'K7M2QX', 0, 3, 1, 2, ?, 'host')`, sara.ID, now)

	exec(t, f.db, `INSERT INTO reports (id, reporter_id, player_id, reason, created_at)
	               VALUES ('rpt_1', ?, ?, 'name', ?), ('rpt_2', ?, ?, 'photo', ?)`,
		sara.ID, ali.ID, now, ali.ID, sara.ID, now)

	if err := f.Do(ctx, sara.ID); err != nil {
		t.Fatal(err)
	}

	row, err := identity.NewPlayerStore(f.db).ByID(ctx, sara.ID)
	if err != nil || row == nil {
		t.Fatalf("the row went: %v", err)
	}
	if row.Phone != nil || row.Nickname != model.DeletedNickname || row.AvatarKey != "" || row.Bio != "" ||
		!row.Disabled || row.DeletedAt == nil || row.Handle == "sara" || len(row.Handle) > model.MaxHandle {
		t.Fatalf("not emptied: %+v", row)
	}

	for table, where := range map[string]string{
		"identities":    "player_id = ?",
		"devices":       "player_id = ?",
		"friendships":   "player_lo = @p OR player_hi = @p",
		"blocks":        "blocker_id = @p OR blocked_id = @p",
		"notifications": "player_id = @p OR actor_id = @p",
		"library":       "player_id = ?",
		"game_tokens":   "player_id = ?",
		"reports":       "reporter_id = @p OR player_id = @p",
	} {
		arg := any(sara.ID)
		if where != "player_id = ?" {
			arg = map[string]any{"p": sara.ID}
		}
		if n := count(t, f.db, table, where, arg); n != 0 {
			t.Errorf("%s: %d rows left", table, n)
		}
	}
	if n := count(t, f.db, "sessions", "player_id = ? AND revoked_at IS NULL", sara.ID); n != 0 {
		t.Errorf("%d sessions still open", n)
	}

	// Kept: the result, the room's members, and everybody else.
	if count(t, f.db, "results", "player_id = ?", sara.ID) != 1 || count(t, f.db, "room_members", "room_id = 'room_1'") != 2 {
		t.Error("the games played went with the account")
	}
	if count(t, f.db, "notifications", "id = 'ntf_3'") != 1 {
		t.Error("somebody else's notification went")
	}
	if p, _ := identity.NewPlayerStore(f.db).ByID(ctx, ali.ID); p == nil || p.DeletedAt != nil {
		t.Error("the friend went too")
	}

	// After the commit: the tokens, the presence, the picture, the line.
	if len(f.whitelist.Revoked) != 1 || f.whitelist.Revoked[0] != session[0].ID {
		t.Errorf("sessions cleared %v", f.whitelist.Revoked)
	}
	if len(f.whitelist.RevokedGames) != 1 || f.whitelist.RevokedGames[0] != "studio.xo:"+sara.ID {
		t.Errorf("games cleared %v", f.whitelist.RevokedGames)
	}
	if len(f.presence.ids) != 1 || len(f.files.Deleted) != 1 || f.files.Deleted[0] != "avatars/"+sara.ID+".png" {
		t.Errorf("presence %v, pictures %v", f.presence.ids, f.files.Deleted)
	}
	w := f.log.Written()
	if len(w) != 1 || w[0].Action != model.ActionPlayerDeleted || w[0].Actor != sara.ID {
		t.Fatalf("the log holds %+v", w)
	}

	// The number and the account are free: a new signup takes them.
	if p, _ := identity.NewPlayerStore(f.db).ByPhone(ctx, "+9647700000001"); p != nil {
		t.Error("the number still opens the deleted row")
	}
	if p, _ := identity.NewPlayerStore(f.db).ByIdentity(ctx, enum.ProviderGoogle, "g-1"); p != nil {
		t.Error("the Google account still opens the deleted row")
	}

	// Twice is not found: there is nothing left to delete.
	if err := f.Do(ctx, sara.ID); status(err) != http.StatusNotFound {
		t.Fatalf("a second delete got %v", err)
	}
}

// In a room that has not ended, or in a live tournament: refused, and
// nothing is touched.
func TestDeletingWaitsForRoomsAndTournaments(t *testing.T) {
	f := deletion(t)
	ctx := context.Background()
	now := data.Now()
	sara := biztest.SeedPlayer(t, f.db, "+9647700000001", "Sara")
	ali := biztest.SeedPlayer(t, f.db, "+9647700000002", "Ali")
	biztest.SeedGame(t, f.db, "studio.xo", model.Manifest{})

	exec(t, f.db, `INSERT INTO rooms (id, code, game_id, host_id, status, config)
	               VALUES ('room_1', 'K7M2QX', 'studio.xo', ?, 'playing', '{}')`, ali.ID)
	exec(t, f.db, `INSERT INTO room_members (room_id, player_id, role, seat) VALUES ('room_1', ?, 'player', 1)`, sara.ID)
	if err := f.Do(ctx, sara.ID); code(err) != "in_room" {
		t.Fatalf("in a room got %v", err)
	}

	exec(t, f.db, `UPDATE room_members SET left_at = ? WHERE player_id = ?`, now, sara.ID)
	exec(t, f.db, `INSERT INTO tournaments (id, code, game_id, organizer_id, name, format, status, size, config)
	               VALUES ('trn_1', 'T7M2QX', 'studio.xo', ?, 'Cup', 'knockout', 'running', 8, '{}')`, ali.ID)
	exec(t, f.db, `INSERT INTO tournament_entrants (tournament_id, player_id, seed, joined_at) VALUES ('trn_1', ?, 1, ?)`,
		sara.ID, now)
	if err := f.Do(ctx, sara.ID); code(err) != "in_tournament" {
		t.Fatalf("in a tournament got %v", err)
	}

	// Organizing one counts too, entrant or not.
	exec(t, f.db, `DELETE FROM tournament_entrants`)
	exec(t, f.db, `UPDATE tournaments SET organizer_id = ?, status = 'open'`, sara.ID)
	if err := f.Do(ctx, sara.ID); code(err) != "in_tournament" {
		t.Fatalf("organizing one got %v", err)
	}

	if p, _ := identity.NewPlayerStore(f.db).ByID(ctx, sara.ID); p == nil || p.DeletedAt != nil || p.Phone == nil {
		t.Fatalf("a refusal touched the row: %+v", p)
	}
	if len(f.log.Written()) != 0 || len(f.whitelist.Revoked) != 0 {
		t.Fatal("a refusal wrote or revoked something")
	}

	// Once it is over, the deletion goes through.
	exec(t, f.db, `UPDATE tournaments SET status = 'done', ended_at = ?`, now)
	if err := f.Do(ctx, sara.ID); err != nil {
		t.Fatal(err)
	}
}

func status(err error) int {
	if e, ok := err.(*errors.Error); ok {
		return e.Status
	}
	return 0
}

func code(err error) string {
	if e, ok := err.(*errors.Error); ok && e.Status == http.StatusConflict {
		return e.Code
	}
	return ""
}
