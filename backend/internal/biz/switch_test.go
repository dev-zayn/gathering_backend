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
	"github.com/jamaa/gathering/backend/internal/data/model"
)

type leftRooms struct{ rooms []string }

func (l *leftRooms) Leave(_ context.Context, _ string, roomID string) error {
	l.rooms = append(l.rooms, roomID)
	return nil
}

type switchFixture struct {
	*Switch
	db        *gorm.DB
	whitelist *biztest.Whitelist
	rooms     *leftRooms
	presence  *clearedPresence
	log       *biztest.Audit
	issuer    *identity.Issuer
}

func switchRules(t *testing.T) switchFixture {
	t.Helper()
	db := biztest.DB(t)
	f := switchFixture{
		db: db, whitelist: &biztest.Whitelist{}, rooms: &leftRooms{},
		presence: &clearedPresence{}, log: &biztest.Audit{},
	}
	sessions := identity.NewSessionStore(db)
	f.issuer = &identity.Issuer{
		Sessions: sessions, Tokens: identity.NewRefreshStore(db),
		Minter: identity.NewMinter(biztest.Signer(t)), Whitelist: f.whitelist,
		SessionTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	}
	f.Switch = &Switch{
		DB: db, Sessions: sessions, Whitelist: f.whitelist,
		Rooms: f.rooms, Presence: f.presence, Audit: f.log,
	}
	return f
}

// Switching a player off ends what they hold now, not when it expires: every
// session, their games' tokens and unspent codes, their seat in an open room.
// Somebody else's are left alone, and the row stays as it was but for the
// flag.
func TestSwitchingAPlayerOff(t *testing.T) {
	f := switchRules(t)
	ctx := context.Background()
	now := data.Now()

	sara := biztest.SeedPlayer(t, f.db, "+9647700000001", "Sara")
	ali := biztest.SeedPlayer(t, f.db, "+9647700000002", "Ali")
	for _, p := range []*model.Player{sara, sara, ali} {
		if _, err := f.issuer.Open(ctx, p, ""); err != nil {
			t.Fatal(err)
		}
	}
	saras, err := identity.NewSessionStore(f.db).ListByPlayer(ctx, sara.ID)
	if err != nil || len(saras) != 2 {
		t.Fatalf("sessions %v, %v", saras, err)
	}

	biztest.SeedGame(t, f.db, "studio.xo", model.Manifest{})
	biztest.SeedGame(t, f.db, "studio.tap", model.Manifest{})
	biztest.SeedLibrary(t, f.db, sara.ID, "studio.xo")
	biztest.SeedLibrary(t, f.db, sara.ID, "studio.tap")
	exec(t, f.db, `INSERT INTO game_tokens (hash, game_id, player_id) VALUES (?, 'studio.xo', ?), (?, 'studio.xo', ?)`,
		"0000000000000000000000000000000000000000000000000000000000000001", sara.ID,
		"0000000000000000000000000000000000000000000000000000000000000002", ali.ID)
	exec(t, f.db, `INSERT INTO oauth_codes (hash, game_id, player_id, expires_at) VALUES (?, 'studio.xo', ?, ?)`,
		"0000000000000000000000000000000000000000000000000000000000000003", sara.ID, now.Add(time.Minute))

	// One open room she is in, one she left, one that ended.
	exec(t, f.db, `INSERT INTO rooms (id, code, game_id, host_id, status, config, ended_at)
	               VALUES ('room_1', 'K7M2QX', 'studio.xo', ?, 'playing', '{}', NULL),
	                      ('room_2', 'K7M2QY', 'studio.xo', ?, 'lobby', '{}', NULL),
	                      ('room_3', 'K7M2QZ', 'studio.xo', ?, 'ended', '{}', ?)`, ali.ID, ali.ID, sara.ID, now)
	exec(t, f.db, `INSERT INTO room_members (room_id, player_id, role, seat, left_at)
	               VALUES ('room_1', ?, 'host', 0, NULL), ('room_1', ?, 'player', 1, NULL),
	                      ('room_2', ?, 'host', 0, NULL), ('room_2', ?, 'player', 1, ?),
	                      ('room_3', ?, 'host', 0, NULL)`,
		ali.ID, sara.ID, ali.ID, sara.ID, now, sara.ID)

	if err := f.Off(ctx, "adm_1", sara.ID); err != nil {
		t.Fatal(err)
	}

	row, err := identity.NewPlayerStore(f.db).ByID(ctx, sara.ID)
	if err != nil || row == nil || !row.Disabled || row.Nickname != "Sara" || row.Phone == nil {
		t.Fatalf("the row: %+v, %v", row, err)
	}
	if n := count(t, f.db, "sessions", "player_id = ? AND revoked_at IS NULL", sara.ID); n != 0 {
		t.Errorf("%d of her sessions still open", n)
	}
	if n := count(t, f.db, "game_tokens", "player_id = ?", sara.ID); n != 0 {
		t.Errorf("%d of her game tokens left", n)
	}
	if n := count(t, f.db, "oauth_codes", "player_id = ?", sara.ID); n != 0 {
		t.Errorf("%d of her codes left", n)
	}

	// Somebody else's: kept.
	if count(t, f.db, "sessions", "player_id = ? AND revoked_at IS NULL", ali.ID) != 1 ||
		count(t, f.db, "game_tokens", "player_id = ?", ali.ID) != 1 {
		t.Error("Ali's session or game token went too")
	}
	if count(t, f.db, "library", "player_id = ?", sara.ID) != 2 {
		t.Error("her shelf went: switching back on should find it as it was")
	}

	// After the commit: both sessions and both games cleared in the
	// whitelist, out of the open room only, presence cleared, one line.
	if len(f.whitelist.Revoked) != 2 {
		t.Errorf("sessions cleared %v", f.whitelist.Revoked)
	}
	games := map[string]bool{}
	for _, g := range f.whitelist.RevokedGames {
		games[g] = true
	}
	if len(games) != 2 || !games["studio.xo:"+sara.ID] || !games["studio.tap:"+sara.ID] {
		t.Errorf("games cleared %v", f.whitelist.RevokedGames)
	}
	if len(f.rooms.rooms) != 1 || f.rooms.rooms[0] != "room_1" {
		t.Errorf("taken out of %v", f.rooms.rooms)
	}
	if len(f.presence.ids) != 1 || f.presence.ids[0] != sara.ID {
		t.Errorf("presence cleared for %v", f.presence.ids)
	}
	w := f.log.Written()
	if len(w) != 1 || w[0].Action != model.ActionPlayerDisabled || w[0].Actor != "adm_1" || w[0].Subject != sara.ID ||
		w[0].Detail["sessions"] != 2 {
		t.Fatalf("the log holds %+v", w)
	}

	// Off again: nothing new to end and nothing new to say.
	if err := f.Off(ctx, "adm_1", sara.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.log.Written()) != 1 || len(f.whitelist.Revoked) != 2 {
		t.Errorf("a second switch-off wrote %d lines, cleared %v", len(f.log.Written()), f.whitelist.Revoked)
	}

	// Back on: she may sign in again; nothing she held comes back.
	if err := f.On(ctx, "adm_1", sara.ID); err != nil {
		t.Fatal(err)
	}
	if row, _ := identity.NewPlayerStore(f.db).ByID(ctx, sara.ID); row == nil || row.Disabled {
		t.Fatalf("still off: %+v", row)
	}
	if n := count(t, f.db, "sessions", "player_id = ? AND revoked_at IS NULL", sara.ID); n != 0 {
		t.Errorf("a session came back: %d", n)
	}
	w = f.log.Written()
	if len(w) != 2 || w[1].Action != model.ActionPlayerEnabled || w[1].Actor != "adm_1" {
		t.Fatalf("the log holds %+v", w)
	}
	if err := f.On(ctx, "adm_1", sara.ID); err != nil || len(f.log.Written()) != 2 {
		t.Errorf("on twice: %v, %d lines", err, len(f.log.Written()))
	}
}

// Nobody, or a deleted account: not found, both ways, and nothing written.
func TestSwitchingFindsOnlyLivePlayers(t *testing.T) {
	f := switchRules(t)
	ctx := context.Background()
	gone := biztest.SeedPlayer(t, f.db, "+9647700000003", "Gone")
	exec(t, f.db, `UPDATE players SET deleted_at = ?, disabled = true WHERE id = ?`, data.Now(), gone.ID)

	for _, id := range []string{"ply_nobody", gone.ID} {
		if err := f.Off(ctx, "adm_1", id); status(err) != http.StatusNotFound {
			t.Errorf("off %s got %v", id, err)
		}
		if err := f.On(ctx, "adm_1", id); status(err) != http.StatusNotFound {
			t.Errorf("on %s got %v", id, err)
		}
	}
	if row, _ := identity.NewPlayerStore(f.db).ByID(ctx, gone.ID); row == nil || !row.Disabled {
		t.Errorf("a deleted account was switched on: %+v", row)
	}
	if len(f.log.Written()) != 0 || len(f.whitelist.Revoked) != 0 || len(f.rooms.rooms) != 0 {
		t.Error("a refusal wrote, revoked or left something")
	}
}
