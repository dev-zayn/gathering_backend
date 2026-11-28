// Package scores is a game played alone keeping a player's best on the
// boards its manifest declares, and those boards read among friends
// (docs/SCORES.md). It is apart from a room's results: nothing here changes
// a leaderboard, a history or a tournament.
package scores

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
)

// Hourly is how many scores one player may send in one game in an hour.
const Hourly = 200

// ScopeFriends is the one board there is in v1: the caller and their friends.
const ScopeFriends = "friends"

var errNotFound = errors.NotFound("not_found")

type Service struct {
	DB      *gorm.DB
	Limiter identity.Limiter

	// Files signs the avatars and the game's icon on the way out; AvatarTTL
	// is how long an avatar's link lives.
	Files     files.Files
	AvatarTTL time.Duration
}

// Sent is what a score did to the player's line on its board.
type Sent struct {
	Board   string  `json:"board" example:"reaction"`
	Score   float64 `json:"score" example:"231"`
	Best    float64 `json:"best" doc:"The player's best on the board after this score." example:"214"`
	NewBest bool    `json:"new_best" doc:"Whether this score became the best: strictly better, or the board's first."`
	Plays   int     `json:"plays" doc:"Scores the player has sent on the board, this one included."`
}

// Submit keeps one score of the player's on one of the game's boards: their
// best if it beats it, a play either way.
func (s *Service) Submit(ctx context.Context, playerID, gameID, board string, score *float64) (*Sent, error) {
	g, err := publishedGame(ctx, s.DB, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil {
		return nil, errNotFound
	}
	b := boardOf(g, board)

	bad := map[string]string{}
	switch {
	case board == "":
		bad["board"] = "required"
	case b == nil:
		bad["board"] = "unknown"
	}
	switch {
	case score == nil:
		bad["score"] = "required"
	case b == nil:
	case b.Min != nil && *score < *b.Min:
		bad["score"] = "below_min"
	case b.Max != nil && *score > *b.Max:
		bad["score"] = "above_max"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	if s.Limiter != nil {
		ok, retry, err := s.Limiter.Allow(ctx, "score:"+playerID+":"+g.ID, Hourly, time.Hour)
		if err != nil {
			return nil, errors.Internal(err)
		}
		if !ok {
			return nil, errors.TooManyRequests(retry)
		}
	}

	// One statement, so two scores at once cannot both lose the other's play.
	// The comparison is the board's order: a constant, never the caller's.
	beats := ">"
	if b.Order == enum.ScoreLow {
		beats = "<"
	}
	now := data.Now()
	var row model.BestScore
	err = data.Conn(ctx, s.DB).Raw(`
		INSERT INTO best_scores (game_id, board, player_id, best, plays, best_at, last_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT (game_id, board, player_id) DO UPDATE SET
			plays   = best_scores.plays + 1,
			last_at = EXCLUDED.last_at,
			best    = CASE WHEN EXCLUDED.best `+beats+` best_scores.best THEN EXCLUDED.best ELSE best_scores.best END,
			best_at = CASE WHEN EXCLUDED.best `+beats+` best_scores.best THEN EXCLUDED.best_at ELSE best_scores.best_at END
		RETURNING *`, g.ID, b.ID, playerID, *score, now, now).Scan(&row).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &Sent{
		Board: b.ID, Score: *score, Best: row.Best, Plays: row.Plays,
		NewBest: row.BestAt.Equal(now) && row.Best == *score,
	}, nil
}

// ScoreBoard is one of a game's boards among the caller and their friends.
type ScoreBoard struct {
	Game    games.Tile   `json:"game"`
	Board   string       `json:"board" example:"reaction"`
	Order   string       `json:"order" enum:"high,low" doc:"Which end is the best: the highest score, or the lowest."`
	Entries []ScoreEntry `json:"entries" doc:"Ranked by best in the board's order; equal bests share a rank, the first to get there listed first."`
}

// ScoreEntry is one player's line on a board.
type ScoreEntry struct {
	Player players.Card `json:"player"`
	Rank   int          `json:"rank" doc:"1 is the best; equal bests share a rank (1, 1, 3)."`
	Best   float64      `json:"best"`
	Plays  int          `json:"plays"`
	BestAt string       `json:"best_at" doc:"When the best was sent."`
	You    bool         `json:"you" doc:"The caller's own line."`
}

// Board ranks the caller and their friends who have a score on one of the
// game's boards. scope is friends, or empty for it.
func (s *Service) Board(ctx context.Context, playerID, gameID, board, scope string) (*ScoreBoard, error) {
	if scope != "" && scope != ScopeFriends {
		return nil, errors.Invalid(map[string]string{"scope": "friends_only"})
	}
	g, err := publishedGame(ctx, s.DB, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	b := boardOf(g, board)
	if b == nil {
		return nil, errNotFound
	}
	rows, err := standings(ctx, s.DB, playerID, g.ID, b.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	slices.SortStableFunc(rows, func(x, y standingRow) int {
		if c := compareBest(b.Order, x.Best, y.Best); c != 0 {
			return c
		}
		if c := x.BestAt.Compare(y.BestAt); c != 0 {
			return c
		}
		if c := byName(x.Nickname, y.Nickname); c != 0 {
			return c
		}
		return strings.Compare(x.PlayerID, y.PlayerID)
	})

	out := &ScoreBoard{Game: games.TileOf(ctx, s.Files, g.ID, g), Board: b.ID, Order: string(b.Order), Entries: make([]ScoreEntry, 0, len(rows))}
	for i, r := range rows {
		rank := i + 1
		if i > 0 && rows[i-1].Best == r.Best {
			rank = out.Entries[i-1].Rank
		}
		out.Entries = append(out.Entries, ScoreEntry{
			Player: players.NewCard(ctx, s.Files, s.AvatarTTL, r.PlayerID, r.Nickname, r.Handle, r.AvatarKey),
			Rank:   rank, Best: r.Best, Plays: r.Plays, BestAt: stamp(r.BestAt), You: r.PlayerID == playerID,
		})
	}
	return out, nil
}

// compareBest is negative when a is the better best on a board of order o.
func compareBest(o enum.ScoreOrder, a, b float64) int {
	switch {
	case a == b:
		return 0
	case o.Beats(a, b):
		return -1
	default:
		return 1
	}
}

// boardOf is the board by that id in the game's manifest, or nil for no
// game, no block, or no such board.
func boardOf(g *model.Game, id string) *model.Board {
	if g == nil || id == "" {
		return nil
	}
	var m struct {
		Scores model.ScoresBlock `json:"scores"`
	}
	if json.Unmarshal(g.Manifest, &m) != nil {
		return nil
	}
	return m.Scores.Board(id)
}

// publishedGame is a game in the store, or nil.
func publishedGame(ctx context.Context, db *gorm.DB, id string) (*model.Game, error) {
	var rows []model.Game
	err := data.Conn(ctx, db).Where("id = ? AND listing = ?", id, enum.Published).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// standingRow is one player's line on a board, with their card's columns.
type standingRow struct {
	PlayerID  string
	Best      float64
	Plays     int
	BestAt    time.Time
	Nickname  string
	Handle    string
	AvatarKey string
}

// standings is the caller's and their friends' lines on one board, for those
// who have one, the switched off left out.
func standings(ctx context.Context, db *gorm.DB, playerID, gameID, board string) ([]standingRow, error) {
	var rows []standingRow
	err := data.Conn(ctx, db).Raw(`
		WITH circle AS (
			SELECT CAST(? AS varchar) AS id
			UNION
			SELECT player_hi FROM friendships WHERE player_lo = ? AND status = 'accepted'
			UNION
			SELECT player_lo FROM friendships WHERE player_hi = ? AND status = 'accepted'
		)
		SELECT s.player_id, s.best, s.plays, s.best_at, p.nickname, p.handle, p.avatar_key
		FROM best_scores s
		JOIN circle c ON c.id = s.player_id
		JOIN players p ON p.id = s.player_id
		WHERE s.game_id = ? AND s.board = ? AND NOT p.disabled`,
		playerID, playerID, playerID, gameID, board).Scan(&rows).Error
	return rows, err
}

// byName orders nicknames as the friends' lists do: case aside, then as is.
func byName(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// stamp is the one timestamp format this API answers in: UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
