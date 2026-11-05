package biz

import (
	"context"
	stderrors "errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Switch is the console switching a player off and back on
// (docs/CONSOLE.md, "Switching a player off"). Off ends everything the player
// holds at once — their sessions, their games' tokens, their seat in an open
// room — so it spans identity, the games and the rooms, and lives here
// rather than in any one of them.
type Switch struct {
	DB        *gorm.DB
	Sessions  *identity.SessionStore
	Whitelist identity.Whitelist

	// Rooms takes the player out of an open room as their own leave would,
	// which closes their socket; Presence is cleared so friends see them gone
	// at once. Either may be nil in a test.
	Rooms interface {
		Leave(ctx context.Context, playerID, roomID string) error
	}
	Presence interface {
		Clear(ctx context.Context, id string) error
	}
	Audit audit.Log
}

// switchedOff is what one switch-off leaves to do after the commit.
type switchedOff struct {
	sessions []string
	games    []string
	rooms    []string
}

// Off switches the player off: they cannot sign in, nobody finds them, and
// every credential they hold stops working now, not when it expires. A
// player already off is switched off again, which ends anything that slipped
// through since. Deleted accounts are not found.
func (s *Switch) Off(ctx context.Context, adminID, playerID string) error {
	var (
		left    switchedOff
		changed bool
	)
	err := data.NewTx(s.DB).Do(ctx, func(ctx context.Context) error {
		db := data.Conn(ctx, s.DB)
		p, err := lockLivePlayer(db, playerID)
		if err != nil {
			return err
		}
		changed = !p.Disabled

		// Every game that may have a token of theirs listed: the ones with a
		// refresh token, and the shelf, where a launch's token comes from.
		err = db.Raw(`SELECT game_id FROM game_tokens WHERE player_id = ?
		              UNION SELECT game_id FROM library WHERE player_id = ?`, playerID, playerID).
			Scan(&left.games).Error
		if err != nil {
			return err
		}
		err = db.Table("room_members AS m").Joins("JOIN rooms r ON r.id = m.room_id").
			Where("m.player_id = ? AND m.left_at IS NULL AND r.status <> 'ended'", playerID).
			Pluck("m.room_id", &left.rooms).Error
		if err != nil {
			return err
		}

		// A game's refresh token would be refused at its next use anyway; gone
		// now, being switched back on is a new sign-in to the game. An unspent
		// code would still buy a game a fresh pair.
		for _, table := range []string{"game_tokens", "oauth_codes"} {
			if err := db.Exec("DELETE FROM "+table+" WHERE player_id = ?", playerID).Error; err != nil {
				return err
			}
		}
		if left.sessions, err = s.Sessions.RevokeAll(ctx, playerID); err != nil {
			return err
		}
		return setDisabled(db, playerID, true)
	})
	if err := asError(err); err != nil {
		return err
	}

	s.afterOff(ctx, playerID, left)
	if changed {
		s.record(ctx, adminID, model.ActionPlayerDisabled, playerID, map[string]any{
			"sessions": len(left.sessions), "rooms": left.rooms,
		})
	}
	return nil
}

// On switches the player back on: they may sign in again. Nothing they held
// comes back; their next sign-in is a new session.
func (s *Switch) On(ctx context.Context, adminID, playerID string) error {
	var changed bool
	err := data.NewTx(s.DB).Do(ctx, func(ctx context.Context) error {
		db := data.Conn(ctx, s.DB)
		p, err := lockLivePlayer(db, playerID)
		if err != nil {
			return err
		}
		if changed = p.Disabled; !changed {
			return nil
		}
		return setDisabled(db, playerID, false)
	})
	if err := asError(err); err != nil {
		return err
	}
	if changed {
		s.record(ctx, adminID, model.ActionPlayerEnabled, playerID, nil)
	}
	return nil
}

// lockLivePlayer holds the player's row for the switch; a deleted account is
// not there to switch.
func lockLivePlayer(db *gorm.DB, playerID string) (*model.Player, error) {
	var p model.Player
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND deleted_at IS NULL", playerID).Take(&p).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.NotFound("not_found")
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func setDisabled(db *gorm.DB, playerID string, disabled bool) error {
	return db.Model(&model.Player{}).Where("id = ?", playerID).
		Updates(map[string]any{"disabled": disabled, "updated_at": data.Now()}).Error
}

func asError(err error) error {
	var e *errors.Error
	switch {
	case err == nil:
		return nil
	case stderrors.As(err, &e):
		return e
	default:
		return errors.Internal(err)
	}
}

// afterOff is the part outside the database: the tokens still listed, the
// seat in an open room, the presence. The player is off whatever happens
// here; each failure is logged.
func (s *Switch) afterOff(ctx context.Context, playerID string, left switchedOff) {
	log := applog.From(ctx)
	for _, id := range left.sessions {
		if err := s.Whitelist.RevokeSession(ctx, id); err != nil {
			log.Error("session not revoked", "player", playerID, "session", id, "err", err)
		}
	}
	for _, g := range left.games {
		if err := s.Whitelist.RevokeGame(ctx, g, playerID); err != nil {
			log.Error("game tokens not revoked", "player", playerID, "game", g, "err", err)
		}
	}
	if s.Rooms != nil {
		for _, room := range left.rooms {
			if err := s.Rooms.Leave(ctx, playerID, room); err != nil {
				log.Error("not taken out of the room", "player", playerID, "room", room, "err", err)
			}
		}
	}
	if s.Presence != nil {
		if err := s.Presence.Clear(ctx, playerID); err != nil {
			log.Error("presence not cleared", "player", playerID, "err", err)
		}
	}
}

func (s *Switch) record(ctx context.Context, adminID, action, playerID string, detail map[string]any) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Write(ctx, model.Entry{
		Actor: adminID, Action: action, Subject: playerID, Detail: detail,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "err", err)
	}
}
