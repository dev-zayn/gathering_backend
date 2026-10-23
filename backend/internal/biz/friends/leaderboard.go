package friends

import (
	"context"
	"slices"
	"strings"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// ScopeFriends is the one leaderboard there is in v1: the caller and their
// friends.
const ScopeFriends = "friends"

// Board is a game's leaderboard among friends.
type Board struct {
	Game    games.Tile `json:"game"`
	Entries []Entry    `json:"entries" doc:"Ranked: wins, then the best score, then plays, each highest first."`
}

// Entry is one player's line: their results in the game, summed up.
type Entry struct {
	Player       players.Card `json:"player"`
	Rank         int          `json:"rank" doc:"1 is the best; ties share a rank (1, 1, 3)."`
	Plays        int          `json:"plays" doc:"Rounds of this game with a result for them."`
	Wins         int          `json:"wins" doc:"Of those, the ones they ranked first in."`
	Best         float64      `json:"best" doc:"Their highest score."`
	LastPlayedAt string       `json:"last_played_at" doc:"Their latest result's finish."`
	You          bool         `json:"you" doc:"The caller's own line."`
}

// Leaderboard ranks the caller and their friends who have results in a
// published game: wins (rank 1) descending, then the best score, then plays,
// each descending; ties share a rank and are listed by nickname. scope is
// friends, or empty for it.
func (s *Service) Leaderboard(ctx context.Context, playerID, gameID, scope string) (*Board, error) {
	if scope != "" && scope != ScopeFriends {
		return nil, errors.Invalid(map[string]string{"scope": "friends_only"})
	}
	g, err := publishedGame(ctx, s.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil {
		return nil, errNotFound
	}
	rows, err := standings(ctx, s.db, playerID, g.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}

	slices.SortStableFunc(rows, func(a, b standingRow) int {
		if c := compareStanding(a, b); c != 0 {
			return c
		}
		if c := byName(a.Nickname, b.Nickname); c != 0 {
			return c
		}
		return strings.Compare(a.PlayerID, b.PlayerID)
	})

	out := &Board{Game: games.TileOf(ctx, s.Files, g.ID, g), Entries: make([]Entry, 0, len(rows))}
	for i, r := range rows {
		rank := i + 1
		if i > 0 && compareStanding(rows[i-1], r) == 0 {
			rank = out.Entries[i-1].Rank
		}
		out.Entries = append(out.Entries, Entry{
			Player: players.NewCard(ctx, s.Files, s.AvatarTTL, r.PlayerID, r.Nickname, r.Handle, r.AvatarKey),
			Rank:   rank, Plays: r.Plays, Wins: r.Wins, Best: r.Best,
			LastPlayedAt: stamp(r.LastPlayedAt), You: r.PlayerID == playerID,
		})
	}
	return out, nil
}

// compareStanding is the ranking alone: negative when a ranks above b, zero
// for a tie.
func compareStanding(a, b standingRow) int {
	switch {
	case a.Wins != b.Wins:
		return b.Wins - a.Wins
	case a.Best > b.Best:
		return -1
	case a.Best < b.Best:
		return 1
	default:
		return b.Plays - a.Plays
	}
}
