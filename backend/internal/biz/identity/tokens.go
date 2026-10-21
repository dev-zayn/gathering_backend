package identity

// The credentials a game's backend ends up holding: the one-time code it
// spends, and the refresh token it keeps. Both live in Postgres, hashed; the
// access token between them is a JWT the whitelist can end.

import (
	"context"
	stderrors "errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

type CodeStore struct{ db *gorm.DB }

func NewCodeStore(db *gorm.DB) *CodeStore { return &CodeStore{db: db} }

func (c *CodeStore) Create(ctx context.Context, in *model.Code) error {
	return data.Conn(ctx, c.db).Create(in).Error
}

// Spend is one conditional UPDATE. Reading and then writing would let two
// requests holding the same code both pass the check.
func (c *CodeStore) Spend(ctx context.Context, hash string, now time.Time) (*model.Code, error) {
	var row model.Code
	res := data.Conn(ctx, c.db).Model(&row).Clauses(clause.Returning{}).
		Where("hash = ? AND used_at IS NULL AND expires_at > ?", hash, now).
		Update("used_at", now)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

// Sweep removes spent and expired authorization codes older than before. The
// api's clock decides when, and the owner of the table decides what.
func (c *CodeStore) Sweep(ctx context.Context, before time.Time) (int64, error) {
	res := data.Conn(ctx, c.db).
		Where("created_at < ? AND (used_at IS NOT NULL OR expires_at < ?)", before, time.Now()).
		Delete(&model.Code{})
	return res.RowsAffected, res.Error
}

type GameTokenStore struct{ db *gorm.DB }

func NewGameTokenStore(db *gorm.DB) *GameTokenStore { return &GameTokenStore{db: db} }

func (g *GameTokenStore) Create(ctx context.Context, t *model.GameToken) error {
	return data.Conn(ctx, g.db).Create(t).Error
}

func (g *GameTokenStore) Get(ctx context.Context, hash string) (*model.GameToken, error) {
	var out model.GameToken
	err := data.Conn(ctx, g.db).Take(&out, "hash = ?", hash).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (g *GameTokenStore) Touch(ctx context.Context, hash string, at time.Time) error {
	return data.Conn(ctx, g.db).Model(&model.GameToken{}).
		Where("hash = ?", hash).Update("used_at", at).Error
}

func (g *GameTokenStore) Delete(ctx context.Context, hash string) error {
	return data.Conn(ctx, g.db).Where("hash = ?", hash).Delete(&model.GameToken{}).Error
}

// DeleteForPlayer ends every refresh token one player's copy of a game holds:
// what removing the game from their library reaches for.
func (g *GameTokenStore) DeleteForPlayer(ctx context.Context, gameID, playerID string) error {
	return data.Conn(ctx, g.db).
		Where("game_id = ? AND player_id = ?", gameID, playerID).
		Delete(&model.GameToken{}).Error
}
