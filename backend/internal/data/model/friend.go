package model

// Who is whose friend, and who refused whom.

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Friendship is one pair, the lower id first so a pair has one row whoever
// asked. A request until the other accepts; a decline, a cancel, a removal
// and a block delete it.
type Friendship struct {
	PlayerLo    string `gorm:"primaryKey"`
	PlayerHi    string `gorm:"primaryKey"`
	Status      enum.FriendshipStatus
	RequestedBy string

	// CreatedAt is the request; RespondedAt the accept.
	CreatedAt   time.Time
	RespondedAt *time.Time
}

func (Friendship) TableName() string { return "friendships" }

// Pair orders two ids the way a friendship row keeps them.
func Pair(a, b string) (lo, hi string) {
	if a < b {
		return a, b
	}
	return b, a
}

// Block is one player refusing another, in one direction.
type Block struct {
	BlockerID string `gorm:"primaryKey"`
	BlockedID string `gorm:"primaryKey"`
	CreatedAt time.Time
}

func (Block) TableName() string { return "blocks" }
