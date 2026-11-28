package games

// A game's publisher (docs/PUBLISHERS.md, "A game's publisher"): the admin
// hands a game to another publisher account, or to none.

import (
	"context"
	stderrors "errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// SetOwner makes publisherID the account whose game gameID is, or none for
// "". The name players see becomes the new account's; with none it stays.
// The open build, the testers and their codes stay with the game. Taking the
// publisher away while a build is open is refused: nobody could publish it.
func (r *Review) SetOwner(ctx context.Context, adminID, gameID, publisherID string) (*model.Game, error) {
	g, err := r.s.reg.Get(ctx, gameID)
	if err != nil {
		return nil, err
	}

	var to *model.Publisher
	if publisherID != "" {
		var p model.Publisher
		err := data.Conn(ctx, r.s.db).Where("id = ?", publisherID).Take(&p).Error
		switch {
		case stderrors.Is(err, gorm.ErrRecordNotFound):
			return nil, errors.Invalid(map[string]string{"publisher": "unknown"})
		case err != nil:
			return nil, errors.Internal(err)
		case p.DisabledAt != nil:
			return nil, errors.Invalid(map[string]string{"publisher": "disabled"})
		}
		to = &p
	}

	from := ""
	if g.PublisherID != nil {
		from = *g.PublisherID
	}
	if from == publisherID {
		return g, nil
	}

	err = r.s.tx.Do(ctx, func(ctx context.Context) error {
		// Held while the open builds are counted, so the answer stands for
		// the write that follows it.
		var held model.Game
		if err := data.Conn(ctx, r.s.db).Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("id = ?", gameID).Take(&held).Error; err != nil {
			return err
		}
		fields := map[string]any{"publisher_id": nil, "updated_at": data.Now()}
		if to == nil {
			var open int64
			if err := data.Conn(ctx, r.s.db).Model(&model.GameBuild{}).
				Where("game_id = ? AND status IN ?", gameID, enum.OpenBuilds).Count(&open).Error; err != nil {
				return err
			}
			if open > 0 {
				return errBuildOpen
			}
		} else {
			fields["publisher_id"] = to.ID
			fields["publisher"] = to.Name
		}
		return data.Conn(ctx, r.s.db).Model(&model.Game{}).Where("id = ?", gameID).Updates(fields).Error
	})
	if err != nil {
		var e *errors.Error
		if stderrors.As(err, &e) {
			return nil, e
		}
		return nil, errors.Internal(err)
	}

	r.s.reg.record(ctx, adminID, model.ActionOwnerChanged, gameID, map[string]any{"from": from, "to": publisherID})
	return r.s.reg.Get(ctx, gameID)
}
