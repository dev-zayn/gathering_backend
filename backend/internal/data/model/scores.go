package model

// A game's boards for scores played alone (docs/SCORES.md), and the best
// kept on each.

import (
	"fmt"
	"regexp"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// MaxBoards is the most boards one game declares.
const MaxBoards = 8

var boardID = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// ScoresBlock is the manifest's scores block. It has no column of its own:
// the platform reads it from the manifest the game's row keeps whole.
type ScoresBlock struct {
	Boards []Board `json:"boards,omitempty"`
}

// Board is one board a game keeps scores on. Min and Max are optional
// bounds; a score outside them is refused.
type Board struct {
	ID    string          `json:"id"`
	Order enum.ScoreOrder `json:"order"`
	Min   *float64        `json:"min,omitempty"`
	Max   *float64        `json:"max,omitempty"`
}

// Board is the block's board by that id, or nil.
func (s ScoresBlock) Board(id string) *Board {
	for i := range s.Boards {
		if s.Boards[i].ID == id {
			return &s.Boards[i]
		}
	}
	return nil
}

// problem is the first thing wrong with the block, or empty.
func (s ScoresBlock) problem() string {
	if len(s.Boards) > MaxBoards {
		return "too_many"
	}
	seen := map[string]bool{}
	for _, b := range s.Boards {
		switch {
		case !boardID.MatchString(b.ID):
			return "bad id: " + b.ID
		case seen[b.ID]:
			return "duplicate id: " + b.ID
		case !b.Order.Valid():
			return fmt.Sprintf("unknown order: %s", b.Order)
		case b.Min != nil && b.Max != nil && *b.Min > *b.Max:
			return "min above max: " + b.ID
		}
		seen[b.ID] = true
	}
	return ""
}

// BestScore is one player's best on one board of one game, and how many
// scores they have sent there.
type BestScore struct {
	GameID   string `gorm:"primaryKey"`
	Board    string `gorm:"primaryKey"`
	PlayerID string `gorm:"primaryKey"`
	Best     float64
	Plays    int
	BestAt   time.Time
	LastAt   time.Time
}

func (BestScore) TableName() string { return "best_scores" }
