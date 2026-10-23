package rooms

// A room's results, kept for good, one set per round: written by the host's
// finish or end, or by the game's own server, whose word is the stronger, and
// read by the Room, the room's result sheet and the player's history.
// docs/ROOMS.md, "Results (M5)" and "Rounds", is the contract.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// errRoomNotStarted is a round to finish, or results to write, in a room no
// round of which is under way or was ever played.
var errRoomNotStarted = errors.Conflict("room_not_started")

// Result is one line of a round's results as everybody reads it, a player who
// has since left included.
type Result struct {
	Player      string  `json:"player" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname    string  `json:"nickname" example:"زين"`
	Avatar      string  `json:"avatar" doc:"A bundled avatar's id, or empty."`
	AvatarURL   *string `json:"avatar_url" nullable:"true" doc:"A link that expires, when the player uploaded a picture."`
	Score       float64 `json:"score"`
	Rank        int     `json:"rank" doc:"1 is the best; ties share a rank."`
	SubmittedBy string  `json:"submitted_by" enum:"host,game" doc:"The host's finish or end, or the game's own server."`
}

// checkScores is the rule on every set of scores, the host's or the game's:
// each names somebody who was in the room at some point, once, and a rank
// the game gave is at least 1. Every problem comes back at once.
func checkScores(scores []ScoreIn, ever map[string]int) error {
	if bad := scoreProblems(scores, ever); len(bad) > 0 {
		return errors.Invalid(bad)
	}
	return nil
}

// scoreProblems is checkScores' findings, by field, for a refusal that says
// more than the scores.
func scoreProblems(scores []ScoreIn, ever map[string]int) map[string]string {
	bad := map[string]string{}
	seen := map[string]bool{}
	for i, sc := range scores {
		field := fmt.Sprintf("scores.%d", i)
		_, member := ever[sc.Player]
		switch {
		case !member:
			bad[field+".player"] = "not_a_member"
		case seen[sc.Player]:
			bad[field+".player"] = "duplicate"
		}
		seen[sc.Player] = true
		if sc.Rank != nil && *sc.Rank < 1 {
			bad[field+".rank"] = "must_be_positive"
		}
	}
	return bad
}

// Submission is the game's own server posting a round's results: which game
// is speaking (the token's audience), for which player (its subject), about
// which room (the body's: any of the game's tokens for a player who was in
// the room will do), and which round of it: nil is the room's own round.
type Submission struct {
	GameID   string
	PlayerID string
	RoomID   string
	Round    *int
	Scores   []ScoreIn
}

// Submitted is what the game's server is answered: the round it wrote, and
// that round's results as they now stand.
type Submitted struct {
	Round   int
	Results []Result
}

// SubmitResults is the game's server writing a round's results. Its word is
// the stronger: the round's rows are replaced whole, and the host's finish or
// end leaves them be. The room's copy is rewritten to match when the round is
// its latest and no longer playing; a round under way keeps playing, and
// nobody on the sockets is told.
func (s *Service) SubmitResults(ctx context.Context, in Submission) (*Submitted, error) {
	round := 0
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := lockRoom(ctx, s.db, in.RoomID)
		if err != nil {
			return err
		}
		if r == nil {
			return errNotFound
		}
		if r.GameID != in.GameID {
			return errAnotherGame
		}
		ever, err := everMembers(ctx, s.db, r.ID)
		if err != nil {
			return err
		}
		if _, ok := ever[in.PlayerID]; !ok {
			return errNotMember
		}
		if r.Round == 0 {
			return errRoomNotStarted
		}

		// The round and the scores, every problem at once.
		bad := map[string]string{}
		round = r.Round
		if in.Round != nil {
			if *in.Round < 1 || *in.Round > r.Round {
				bad["round"] = "unknown"
			}
			round = *in.Round
		}
		if len(in.Scores) == 0 {
			bad["scores"] = "required"
		}
		for field, problem := range scoreProblems(in.Scores, ever) {
			bad[field] = problem
		}
		if len(bad) > 0 {
			return errors.Invalid(bad)
		}

		results := ranked(in.Scores)
		finished, err := finishedAt(ctx, s.db, r.ID, round)
		if err != nil {
			return err
		}
		if finished.IsZero() {
			finished = data.Now()
			if round == r.Round && !r.Open() && r.EndedAt != nil {
				finished = *r.EndedAt
			}
		}
		if err := s.writeResults(ctx, r, round, ever, in.PlayerID, enum.SubmitterGame, results, finished); err != nil {
			return err
		}
		if round != r.Round || r.Status == enum.RoomPlaying {
			return nil
		}
		return updateRoom(ctx, s.db, r.ID, map[string]any{"results": model.Scores(results)})
	})
	if err != nil {
		return nil, asError(err)
	}
	rows, err := resultRows(ctx, s.db, in.RoomID, round)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &Submitted{Round: round, Results: s.lines(ctx, rows)}, nil
}

// writeResults replaces a round's rows with one write's: every line ranked,
// who wrote them, when they finished, and what the reads will need once the
// room row is gone (its code, each player's seat). The caller holds the
// room's row, and the event is written in its transaction.
func (s *Service) writeResults(ctx context.Context, r *model.Room, round int, ever map[string]int, writer string,
	by enum.Submitter, results []model.Score, finished time.Time) error {
	conn := data.Conn(ctx, s.db)
	if err := conn.Where("room_id = ? AND round = ?", r.ID, round).Delete(&model.Result{}).Error; err != nil {
		return err
	}
	rows := make([]model.Result, 0, len(results))
	for _, sc := range results {
		rows = append(rows, model.Result{
			RoomID: r.ID, Round: round, PlayerID: sc.Player, GameID: r.GameID, Code: r.Code, Seat: ever[sc.Player],
			Score: sc.Score, Rank: sc.Rank, Players: len(results), FinishedAt: finished, SubmittedBy: by,
		})
	}
	if len(rows) > 0 {
		if err := conn.Create(&rows).Error; err != nil {
			return err
		}
	}
	return event(ctx, s.db, r, writer, enum.EventResults, map[string]any{"by": by, "scores": len(results), "round": round})
}

// finishedAt is when a round's rows say it finished, or zero when it has
// none.
func finishedAt(ctx context.Context, db *gorm.DB, roomID string, round int) (time.Time, error) {
	var rows []struct{ FinishedAt time.Time }
	err := data.Conn(ctx, db).Raw(`SELECT finished_at FROM results WHERE room_id = ? AND round = ? LIMIT 1`,
		roomID, round).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return time.Time{}, err
	}
	return rows[0].FinishedAt, nil
}

// gameResults is what the game's server wrote for a round, ranked, ties in
// seat order; empty when it has written nothing.
func gameResults(ctx context.Context, db *gorm.DB, roomID string, round int) ([]model.Score, error) {
	var out []model.Score
	err := data.Conn(ctx, db).Raw(`
		SELECT player_id AS player, score, rank FROM results
		WHERE room_id = ? AND round = ? AND submitted_by = ?
		ORDER BY rank, seat, player_id`, roomID, round, enum.SubmitterGame).Scan(&out).Error
	return out, err
}

// roundResults is what the round under way carries as it closes, by a finish
// or an end: the game's results when its server wrote this round's, else the
// scores sent, ranked, which the caller writes as the host's once its own
// event is written (write says so). No scores and nothing from the game is
// an empty list.
func roundResults(ctx context.Context, db *gorm.DB, r *model.Room, scores []ScoreIn) (results []model.Score, write bool, err error) {
	kept, err := gameResults(ctx, db, r.ID, r.Round)
	if err != nil {
		return nil, false, err
	}
	if len(kept) > 0 {
		return kept, false, nil
	}
	results = ranked(scores)
	return results, len(results) > 0, nil
}

// latestResults is what an end between rounds carries: the latest round's
// results as the room keeps its copy, or none when no round was played.
func latestResults(r *model.Room) []model.Score {
	if r.Results == nil {
		return []model.Score{}
	}
	return r.Results
}

// resultRow is a row as the reads draw it: the line, where it came from, and
// the card it is drawn with.
type resultRow struct {
	PlayerID    string
	GameID      string
	Code        string
	Seat        int
	Score       float64
	Rank        int
	FinishedAt  time.Time
	SubmittedBy string
	Nickname    string
	AvatarKey   string
}

// resultRows is a round's rows with each player's card, ranked, ties in seat
// order. Whether or not the room row is still there.
func resultRows(ctx context.Context, db *gorm.DB, roomID string, round int) ([]resultRow, error) {
	var rows []resultRow
	err := data.Conn(ctx, db).Raw(`
		SELECT r.player_id, r.game_id, r.code, r.seat, r.score, r.rank, r.finished_at, r.submitted_by,
		       p.nickname, p.avatar_key
		FROM results r JOIN players p ON p.id = r.player_id
		WHERE r.room_id = ? AND r.round = ?
		ORDER BY r.rank, r.seat, r.player_id`, roomID, round).Scan(&rows).Error
	return rows, err
}

// anyRows says whether any round of the room has rows: a room without is one
// from before the table, whose own copy is its results.
func anyRows(ctx context.Context, db *gorm.DB, roomID string) (bool, error) {
	var found bool
	err := data.Conn(ctx, db).Raw(`SELECT EXISTS (SELECT 1 FROM results WHERE room_id = ?)`, roomID).Scan(&found).Error
	return found, err
}

// resultsOf is the Room's results: the rows of its round, or, for a room from
// before the table, its own copy drawn the same way.
func (s *Service) resultsOf(ctx context.Context, r *model.Room) ([]Result, error) {
	rows, err := resultRows(ctx, s.db, r.ID, r.Round)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if len(rows) > 0 {
		return s.lines(ctx, rows), nil
	}
	found, err := anyRows(ctx, s.db, r.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if found {
		return []Result{}, nil
	}
	return s.copied(ctx, r)
}

// copied is a room's own copy of its results (rooms.results) as Result
// lines: the names joined, the seats read from the members, ranked, ties in
// seat order, written by the host — the only one who could, before M5.
func (s *Service) copied(ctx context.Context, r *model.Room) ([]Result, error) {
	if len(r.Results) == 0 {
		return []Result{}, nil
	}
	ids := make([]string, 0, len(r.Results))
	for _, sc := range r.Results {
		ids = append(ids, sc.Player)
	}
	var cards []struct {
		ID        string
		Nickname  string
		AvatarKey string
	}
	if err := data.Conn(ctx, s.db).Raw(`SELECT id, nickname, avatar_key FROM players WHERE id IN ?`, ids).Scan(&cards).Error; err != nil {
		return nil, errors.Internal(err)
	}
	seats, err := everMembers(ctx, s.db, r.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}

	byID := make(map[string]int, len(cards))
	for i, c := range cards {
		byID[c.ID] = i
	}
	rows := make([]resultRow, 0, len(r.Results))
	for _, sc := range r.Results {
		row := resultRow{
			PlayerID: sc.Player, GameID: r.GameID, Code: r.Code, Seat: seats[sc.Player],
			Score: sc.Score, Rank: sc.Rank, SubmittedBy: string(enum.SubmitterHost),
		}
		if i, ok := byID[sc.Player]; ok {
			row.Nickname, row.AvatarKey = cards[i].Nickname, cards[i].AvatarKey
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		if a.Seat != b.Seat {
			return a.Seat < b.Seat
		}
		return a.PlayerID < b.PlayerID
	})
	return s.lines(ctx, rows), nil
}

// lines draws rows as Result lines, the pictures signed on the way out.
func (s *Service) lines(ctx context.Context, rows []resultRow) []Result {
	out := make([]Result, 0, len(rows))
	for _, row := range rows {
		line := Result{
			Player: row.PlayerID, Nickname: row.Nickname, Score: row.Score, Rank: row.Rank,
			SubmittedBy: row.SubmittedBy,
		}
		line.Avatar, line.AvatarURL = s.picture(ctx, row.PlayerID, row.AvatarKey)
		out = append(out, line)
	}
	return out
}
