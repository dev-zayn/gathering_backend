package model

// What a room's results are, kept for good.

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Result is one ranked player's line in one round of a room. The room row is
// deleted a day after the end and these stay, so what the reads still need
// from the room is copied in: its code, and the seat the player held, which
// orders a tie. All the rows of one round come from one write.
type Result struct {
	RoomID string `gorm:"primaryKey"`
	// Round is the round of the room these results are for, from 1.
	Round    int    `gorm:"primaryKey;autoIncrement:false"`
	PlayerID string `gorm:"primaryKey"`
	GameID   string
	Code     string
	Seat     int

	// Score is numeric in the column; Rank is 1 for the best, ties sharing
	// one; Players is how many were ranked in this round.
	Score   float64
	Rank    int
	Players int

	// FinishedAt is the round's finish, or the room's end, or when the game's
	// server wrote them into a round still playing.
	FinishedAt  time.Time
	SubmittedBy enum.Submitter
}

func (Result) TableName() string { return "results" }
