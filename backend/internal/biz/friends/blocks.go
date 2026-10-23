package friends

import (
	"context"

	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// Block is the caller refusing another player. It ends any friendship or
// request between the two and takes the requests' notifications with it,
// either way; it keeps them from asking again, inviting each other, or
// meeting in a friends-only room. Blocking twice is blocking once. Nothing
// is raised: nobody is told.
func (s *Service) Block(ctx context.Context, callerID, targetID string) error {
	if callerID == targetID {
		return errIsYou
	}
	if _, err := s.target(ctx, targetID); err != nil {
		return err
	}
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPair(ctx, s.db, callerID, targetID); err != nil {
			return err
		}
		err := data.Conn(ctx, s.db).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Block{
			BlockerID: callerID, BlockedID: targetID, CreatedAt: data.Now(),
		}).Error
		if err != nil {
			return err
		}
		if err := deleteFriendship(ctx, s.db, callerID, targetID); err != nil {
			return err
		}
		if err := s.Inbox.Withdraw(ctx, callerID, "", requestKey(targetID)); err != nil {
			return err
		}
		return s.Inbox.Withdraw(ctx, targetID, "", requestKey(callerID))
	})
	return errorOrNil(err)
}

// Unblock lifts the caller's block, if there was one. It restores nothing:
// a friendship the block ended stays ended.
func (s *Service) Unblock(ctx context.Context, callerID, targetID string) error {
	if callerID == targetID {
		return errIsYou
	}
	if _, err := s.target(ctx, targetID); err != nil {
		return err
	}
	err := data.Conn(ctx, s.db).Where("blocker_id = ? AND blocked_id = ?", callerID, targetID).
		Delete(&model.Block{}).Error
	return errorOrNil(err)
}
