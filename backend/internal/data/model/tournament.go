package model

// What a tournament is, with no idea of the rooms its matches are played in.

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Tournament limits, as docs/TOURNAMENTS.md sets them. Checked on the way in,
// so a value that would not do is a field error.
const (
	MaxTournamentName = 40
	MaxLeagueRounds   = 20
	MaxEntrants       = 64
)

// Tournament is one tournament: a game, a format, a code to join by, and the
// game's room block as it stood when it was made. Its entrants and matches
// are rows of their own; its tallies are read from the plays.
type Tournament struct {
	ID     string `gorm:"primaryKey"`
	Code   string
	GameID string

	// OrganizerID is the player who made it; nil for an official one, made
	// from the console by AdminID.
	OrganizerID *string
	AdminID     *string

	Name       string
	Format     enum.TournamentFormat
	Visibility enum.TournamentVisibility
	Status     enum.TournamentStatus

	// BestOf is a knockout match's length (1, 3 or 5); Rounds a league's
	// (1 to 20); Size the most entrants it takes.
	BestOf int
	Rounds int
	Size   int

	// Config is the game's room block when the tournament was made: a match
	// room is shaped by it, whatever the manifest says later.
	Config RoomBlock

	// Stages is set by the draw: a knockout's log2 of its bracket, a
	// league's 1.
	Stages int

	ChampionID *string
	EndReason  *enum.TournamentEnd

	StartsAt  *time.Time
	CreatedAt time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
}

func (Tournament) TableName() string { return "tournaments" }

// Official is a tournament the platform made, from the console.
func (t *Tournament) Official() bool { return t.OrganizerID == nil }

// Need is how many rounds a knockout match takes to win: a majority of its
// best of. A league's match is never won this way.
func (t *Tournament) Need() int {
	if t.Format != enum.FormatKnockout {
		return 1
	}
	return t.BestOf/2 + 1
}

// Organizer says whether the player made this tournament.
func (t *Tournament) Organizer(playerID string) bool {
	return t.OrganizerID != nil && *t.OrganizerID == playerID
}

// TournamentEntrant is a player in a tournament. The row outlives a
// withdrawal once it runs (LeftAt), so the bracket still names them, and a
// removal for good (Removed), so the code never lets them back.
type TournamentEntrant struct {
	TournamentID string `gorm:"primaryKey"`
	PlayerID     string `gorm:"primaryKey"`
	Seed         int
	JoinedAt     time.Time
	LeftAt       *time.Time
	Removed      bool

	// Place is the final place, once it is known: a knockout's loser as they
	// go out, everybody's at a league's end.
	Place *int
}

func (TournamentEntrant) TableName() string { return "tournament_entrants" }

// Current is an entrant who has neither withdrawn nor been removed.
func (e *TournamentEntrant) Current() bool { return e.LeftAt == nil && !e.Removed }

// TournamentMatch is one match. A side is a player (PlayerA, PlayerB), void
// (nobody will ever take it), or still to come (nil and not void). A
// league's one match has no sides: every current entrant plays it.
type TournamentMatch struct {
	ID           string `gorm:"primaryKey"`
	TournamentID string
	Stage        int
	Slot         int

	PlayerA *string
	PlayerB *string
	VoidA   bool
	VoidB   bool

	Status    enum.MatchStatus
	WinnerID  *string
	DecidedBy *enum.MatchDecision

	ReadyAt   *time.Time
	DecidedAt *time.Time
}

func (TournamentMatch) TableName() string { return "tournament_matches" }

// Has says whether the player is one of the match's two sides.
func (m *TournamentMatch) Has(playerID string) bool {
	return (m.PlayerA != nil && *m.PlayerA == playerID) || (m.PlayerB != nil && *m.PlayerB == playerID)
}

// Other is the side facing the player, or nil.
func (m *TournamentMatch) Other(playerID string) *string {
	switch {
	case m.PlayerA != nil && *m.PlayerA == playerID:
		return m.PlayerB
	case m.PlayerB != nil && *m.PlayerB == playerID:
		return m.PlayerA
	}
	return nil
}

// TournamentPlay is one counted round of a match: the results the round
// closed with, kept to the match's players, and its one winner, nil for a
// draw. Kept with the tournament for good; the room it was played in goes a
// day after its end, so no foreign key.
type TournamentPlay struct {
	MatchID  string `gorm:"primaryKey"`
	RoomID   string `gorm:"primaryKey"`
	Round    int    `gorm:"primaryKey;autoIncrement:false"`
	Results  Scores
	WinnerID *string
	PlayedAt time.Time
}

func (TournamentPlay) TableName() string { return "tournament_plays" }
