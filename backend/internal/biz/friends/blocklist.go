package friends

import (
	"context"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// The blocked list a player reads to unblock, and a player's blocks as the
// console sees them (docs/FRIENDS.md, "The blocked list"; docs/CONSOLE.md,
// "A player's blocks").

// BlockedPlayer is one player the caller has blocked, as the app lists them.
type BlockedPlayer struct {
	players.Card
	BlockedAt string `json:"blocked_at" example:"2026-11-22T10:00:00Z" doc:"When the caller blocked them."`
}

// BlockedPlayers is who the caller has blocked, newest block first. A player
// an admin switched off is left out: unblocking them would be not_found.
func (s *Service) BlockedPlayers(ctx context.Context, callerID string) ([]BlockedPlayer, error) {
	var rows []cardRow
	err := data.Conn(ctx, s.db).Raw(`
		SELECT p.id, p.nickname, p.handle, p.avatar_key, b.created_at AS at
		FROM blocks b JOIN players p ON p.id = b.blocked_id
		WHERE b.blocker_id = ? AND NOT p.disabled
		ORDER BY b.created_at DESC, p.id`, callerID).Scan(&rows).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := make([]BlockedPlayer, 0, len(rows))
	for _, r := range rows {
		b := BlockedPlayer{Card: s.card(ctx, r)}
		if r.At != nil {
			b.BlockedAt = stamp(*r.At)
		}
		out = append(out, b)
	}
	return out, nil
}

// BlockRow is one block as the console shows it: the other player, whether
// an admin switched them off, and when.
type BlockRow struct {
	ID        string `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname  string `json:"nickname"`
	Handle    string `json:"handle"`
	Avatar    string `json:"avatar" doc:"A bundled avatar's id, an uploaded picture's key, or empty."`
	Disabled  bool   `json:"disabled"`
	BlockedAt string `json:"blocked_at" example:"2026-11-22T10:00:00Z"`
}

type blockRow struct {
	ID        string
	Nickname  string
	Handle    string
	AvatarKey string
	Disabled  bool
	At        *time.Time
}

// Blocks is a player's blocks both ways, newest first: who they blocked, and
// who blocked them. not_found for nobody or a deleted account.
func (s *Service) Blocks(ctx context.Context, playerID string) (blocked, blockedBy []BlockRow, err error) {
	if err := s.live(ctx, playerID); err != nil {
		return nil, nil, err
	}
	if blocked, err = s.blockRows(ctx, "blocked_id", "blocker_id", playerID); err != nil {
		return nil, nil, err
	}
	if blockedBy, err = s.blockRows(ctx, "blocker_id", "blocked_id", playerID); err != nil {
		return nil, nil, err
	}
	return blocked, blockedBy, nil
}

// blockRows reads the blocks whose `mine` column is the player, drawing the
// player in the `other` column. Both names are this file's constants.
func (s *Service) blockRows(ctx context.Context, other, mine, playerID string) ([]BlockRow, error) {
	var rows []blockRow
	err := data.Conn(ctx, s.db).Raw(`
		SELECT p.id, p.nickname, p.handle, p.avatar_key, p.disabled, b.created_at AS at
		FROM blocks b JOIN players p ON p.id = b.`+other+`
		WHERE b.`+mine+` = ?
		ORDER BY b.created_at DESC, p.id`, playerID).Scan(&rows).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := make([]BlockRow, 0, len(rows))
	for _, r := range rows {
		b := BlockRow{ID: r.ID, Nickname: r.Nickname, Handle: r.Handle, Avatar: r.AvatarKey, Disabled: r.Disabled}
		if r.At != nil {
			b.BlockedAt = stamp(*r.At)
		}
		out = append(out, b)
	}
	return out, nil
}

// Lift is an admin lifting blockerID's block of blockedID, as the player's
// own unblock does: nothing restored, nobody told. Lifting a block that is
// not there is not refused and writes nothing; not_found when the blocker is
// nobody or a deleted account.
func (s *Service) Lift(ctx context.Context, adminID, blockerID, blockedID string) error {
	if err := s.live(ctx, blockerID); err != nil {
		return err
	}
	res := data.Conn(ctx, s.db).Where("blocker_id = ? AND blocked_id = ?", blockerID, blockedID).
		Delete(&model.Block{})
	if res.Error != nil {
		return errors.Internal(res.Error)
	}
	if res.RowsAffected > 0 && s.Audit != nil {
		err := s.Audit.Write(ctx, model.Entry{
			Actor: adminID, Action: model.ActionBlockLifted, Subject: blockerID,
			Detail: map[string]any{"blocked": blockedID},
		})
		if err != nil {
			applog.From(ctx).Error("audit not written", "action", model.ActionBlockLifted, "err", err)
		}
	}
	return nil
}

// live is not_found for nobody and for a deleted account; a switched-off
// player is still somebody to the console.
func (s *Service) live(ctx context.Context, id string) error {
	p, err := s.Players.ByID(ctx, id)
	if err != nil {
		return errors.Internal(err)
	}
	if p == nil || p.DeletedAt != nil {
		return errNotFound
	}
	return nil
}
