package biz

import (
	"context"
	stderrors "errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Deletion is a player deleting their own account (docs/ACCOUNTS.md). It
// spans identity, friends, the inbox, the library and the games' tokens, so
// it lives here rather than in any one of them.
type Deletion struct {
	DB        *gorm.DB
	Sessions  *identity.SessionStore
	Whitelist identity.Whitelist

	// Presence is cleared so friends see them gone at once; Files holds an
	// uploaded picture. Either may be nil in a test.
	Presence interface {
		Clear(ctx context.Context, id string) error
	}
	Files files.Files
	Audit audit.Log
}

var (
	errInRoom       = errors.Conflict("in_room")
	errInTournament = errors.Conflict("in_tournament")
)

// gone is what one deletion leaves to do after the commit: the tokens the
// whitelist still lists, and the picture in storage.
type gone struct {
	sessions []string
	games    []string
	picture  string
}

// Do deletes the account: everything that is the person's goes, and the row
// stays, emptied, for the games already played. Refused while they are in an
// open room or a live tournament.
func (d *Deletion) Do(ctx context.Context, playerID string) error {
	var left gone
	err := data.NewTx(d.DB).Do(ctx, func(ctx context.Context) error {
		db := data.Conn(ctx, d.DB)

		var p model.Player
		err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", playerID).Take(&p).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errors.NotFound("not_found")
		}
		if err != nil {
			return err
		}

		var inRoom int64
		err = db.Table("room_members AS m").Joins("JOIN rooms r ON r.id = m.room_id").
			Where("m.player_id = ? AND m.left_at IS NULL AND r.status <> 'ended'", playerID).
			Count(&inRoom).Error
		if err != nil {
			return err
		}
		if inRoom > 0 {
			return errInRoom
		}

		var inTournament int64
		err = db.Table("tournaments AS t").
			Where("t.status IN ('open', 'running')").
			Where(`t.organizer_id = ? OR EXISTS (
				SELECT 1 FROM tournament_entrants e
				WHERE e.tournament_id = t.id AND e.player_id = ?
				  AND e.left_at IS NULL AND NOT e.removed)`, playerID, playerID).
			Count(&inTournament).Error
		if err != nil {
			return err
		}
		if inTournament > 0 {
			return errInTournament
		}

		if err := db.Table("game_tokens").Where("player_id = ?", playerID).
			Distinct().Pluck("game_id", &left.games).Error; err != nil {
			return err
		}

		// Each table's rows that are the player's, on whichever side.
		for table, where := range map[string]string{
			"identities":    "player_id = ?",
			"devices":       "player_id = ?",
			"friendships":   "player_lo = ? OR player_hi = ?",
			"blocks":        "blocker_id = ? OR blocked_id = ?",
			"notifications": "player_id = ? OR actor_id = ?",
			"library":       "player_id = ?",
			"game_tokens":   "player_id = ?",
			"oauth_codes":   "player_id = ?",
			"reports":       "reporter_id = ? OR player_id = ?",
		} {
			args := make([]any, strings.Count(where, "?"))
			for i := range args {
				args[i] = playerID
			}
			if err := db.Exec("DELETE FROM "+table+" WHERE "+where, args...).Error; err != nil {
				return err
			}
		}

		left.sessions, err = d.Sessions.RevokeAll(ctx, playerID)
		if err != nil {
			return err
		}

		if strings.HasPrefix(p.AvatarKey, "avatars/") {
			left.picture = p.AvatarKey
		}
		now := data.Now()
		return db.Model(&model.Player{}).Where("id = ?", playerID).Updates(map[string]any{
			"phone":      nil,
			"nickname":   model.DeletedNickname,
			"handle":     deletedHandle(playerID),
			"avatar_key": "",
			"bio":        "",
			"disabled":   true,
			"deleted_at": now,
			"updated_at": now,
		}).Error
	})
	var e *errors.Error
	switch {
	case stderrors.As(err, &e):
		return e
	case err != nil:
		return errors.Internal(err)
	}

	d.after(ctx, playerID, left)
	return nil
}

// deletedHandle frees the old handle and keeps the column unique: deleted_
// and the id's last twelve characters.
func deletedHandle(playerID string) string {
	tail := playerID
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	return "deleted_" + strings.ToLower(tail)
}

// after is the part outside the database: the tokens still listed, the
// presence, the picture and the audit line. The account is gone whatever
// happens here; each failure is logged.
func (d *Deletion) after(ctx context.Context, playerID string, left gone) {
	log := applog.From(ctx)
	for _, s := range left.sessions {
		if err := d.Whitelist.RevokeSession(ctx, s); err != nil {
			log.Error("session not revoked", "player", playerID, "session", s, "err", err)
		}
	}
	for _, g := range left.games {
		if err := d.Whitelist.RevokeGame(ctx, g, playerID); err != nil {
			log.Error("game tokens not revoked", "player", playerID, "game", g, "err", err)
		}
	}
	if d.Presence != nil {
		if err := d.Presence.Clear(ctx, playerID); err != nil {
			log.Error("presence not cleared", "player", playerID, "err", err)
		}
	}
	if left.picture != "" && d.Files != nil {
		if err := d.Files.Delete(ctx, left.picture); err != nil {
			log.Error("picture not deleted", "player", playerID, "key", left.picture, "err", err)
		}
	}
	if d.Audit != nil {
		if err := d.Audit.Write(ctx, model.Entry{
			Actor: playerID, Action: model.ActionPlayerDeleted, Subject: playerID,
		}); err != nil {
			log.Error("audit not written", "action", model.ActionPlayerDeleted, "err", err)
		}
	}
}
