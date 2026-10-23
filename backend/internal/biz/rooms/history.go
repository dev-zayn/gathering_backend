package rooms

// Reading results back: one round's sheet, which outlives the room row, and a
// player's history, one item per round, newest first, a page at a time.

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// RoomResults is one round's result sheet: what the results page draws.
type RoomResults struct {
	Room       string   `json:"room" example:"room_7f3k9d2m8q1x4b6n"`
	Code       string   `json:"code" example:"K7M2QX"`
	Game       Game     `json:"game"`
	Round      int      `json:"round" doc:"The round shown: the one asked for, else the latest with results the caller may read."`
	Rounds     []int    `json:"rounds" doc:"Every round of the room with results that the caller may read, ascending."`
	FinishedAt *string  `json:"finished_at" nullable:"true" doc:"When the round's results were written; null for a round with none."`
	Results    []Result `json:"results" doc:"Ranked, ties in seat order."`
}

// maxRound is longer than any round number worth reading.
const maxRound = 9

// readRound is a round as the query names it: empty for the default, else a
// whole number from 1.
func readRound(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	if len(s) > maxRound || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1
}

// roundRow is one round of a room with rows, and whether the caller has one
// in it.
type roundRow struct {
	Round int
	Mine  bool
}

// RoomResults is one round of a room's results. A player who is or was in the
// room may read every round while the room row is kept; anybody else, the
// rounds they have a result in. Everybody else is told there is nothing to
// see. The rows outlive the room row, so this answers a day after the end and
// for good. round is the query's: empty for the latest the caller may read.
func (s *Service) RoomResults(ctx context.Context, playerID, roomID, round string) (*RoomResults, error) {
	asked, ok := readRound(round)
	if !ok {
		return nil, errors.Invalid(map[string]string{"round": "must_be_positive"})
	}

	var rounds []roundRow
	err := data.Conn(ctx, s.db).Raw(`
		SELECT round, bool_or(player_id = ?) AS mine FROM results
		WHERE room_id = ? GROUP BY round ORDER BY round`, playerID, roomID).Scan(&rounds).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	r, err := roomByID(ctx, s.db, roomID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	member := false
	if r != nil {
		m, err := membership(ctx, s.db, r.ID, playerID)
		if err != nil {
			return nil, errors.Internal(err)
		}
		member = m != nil
	}

	readable := make([]int, 0, len(rounds))
	mine := map[int]bool{}
	for _, rd := range rounds {
		if rd.Mine {
			mine[rd.Round] = true
		}
		if member || rd.Mine {
			readable = append(readable, rd.Round)
		}
	}
	shown := asked
	switch {
	case shown != 0:
	case len(readable) > 0:
		shown = readable[len(readable)-1]
	case member:
		shown = r.Round
	}
	if !member && !mine[shown] {
		return nil, errNotFound
	}

	rows, err := resultRows(ctx, s.db, roomID, shown)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := &RoomResults{Room: roomID, Round: shown, Rounds: readable}
	gameID := ""
	if len(rows) > 0 {
		at := stamp(rows[0].FinishedAt)
		out.Code, gameID, out.FinishedAt = rows[0].Code, rows[0].GameID, &at
		out.Results = s.lines(ctx, rows)
	} else {
		// No rows, so the caller is a member and the room row is here. A room
		// from before the table reads its own copy for its latest round.
		if r == nil {
			return nil, errNotFound
		}
		out.Code, gameID, out.Results = r.Code, r.GameID, []Result{}
		if len(rounds) == 0 && shown == r.Round {
			if out.Results, err = s.copied(ctx, r); err != nil {
				return nil, err
			}
			if len(out.Results) > 0 && r.EndedAt != nil {
				at := stamp(*r.EndedAt)
				out.FinishedAt = &at
			}
		}
	}
	g, err := gameRow(ctx, s.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out.Game = s.gameCard(ctx, g, gameID)
	return out, nil
}

// HistoryItem is one round in a player's history: their own line in it.
type HistoryItem struct {
	Room        string  `json:"room" example:"room_7f3k9d2m8q1x4b6n"`
	Code        string  `json:"code" example:"PY6LSX"`
	Game        Game    `json:"game"`
	Round       int     `json:"round" doc:"Which round of the room, from 1."`
	Score       float64 `json:"score"`
	Rank        int     `json:"rank" doc:"1 is the best; ties share a rank."`
	Players     int     `json:"players" doc:"How many were ranked in the round."`
	FinishedAt  string  `json:"finished_at"`
	SubmittedBy string  `json:"submitted_by" enum:"host,game"`
}

// History is one page of a player's history.
type History struct {
	Items      []HistoryItem `json:"items" doc:"Newest first."`
	NextCursor *string       `json:"next_cursor" nullable:"true" doc:"Hand it back as cursor for the next page; null on the last."`
}

// maxHistoryLimit is the longest page a client may ask for. The default, 20,
// is the route's.
const maxHistoryLimit = 50

// History is the player's results, one item per round, newest first:
// finished_at descending, then room id, then round, the order of the
// results_history index. cursor is the next_cursor of the page before, or
// empty for the first page.
func (s *Service) History(ctx context.Context, playerID, cursor string, limit int) (*History, error) {
	bad := map[string]string{}
	if limit < 1 || limit > maxHistoryLimit {
		bad["limit"] = "1_to_50"
	}
	after, ok := readCursor(cursor)
	if !ok {
		bad["cursor"] = "invalid"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	q := `SELECT * FROM results WHERE player_id = ?`
	args := []any{playerID}
	if after != nil {
		q += ` AND (finished_at < ? OR (finished_at = ? AND (room_id > ? OR (room_id = ? AND round > ?))))`
		args = append(args, after.at, after.at, after.room, after.room, after.round)
	}
	q += ` ORDER BY finished_at DESC, room_id, round LIMIT ?`
	args = append(args, limit+1)

	var rows []model.Result
	if err := data.Conn(ctx, s.db).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, errors.Internal(err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}

	out := &History{Items: make([]HistoryItem, 0, len(rows))}
	cards := map[string]Game{}
	for _, row := range rows {
		card, ok := cards[row.GameID]
		if !ok {
			g, err := gameRow(ctx, s.db, row.GameID)
			if err != nil {
				return nil, errors.Internal(err)
			}
			card = s.gameCard(ctx, g, row.GameID)
			cards[row.GameID] = card
		}
		out.Items = append(out.Items, HistoryItem{
			Room: row.RoomID, Code: row.Code, Game: card, Round: row.Round, Score: row.Score, Rank: row.Rank,
			Players: row.Players, FinishedAt: stamp(row.FinishedAt), SubmittedBy: string(row.SubmittedBy),
		})
	}
	if more {
		next := cursorOf(rows[len(rows)-1])
		out.NextCursor = &next
	}
	return out, nil
}

// position is where a page of history starts: after this finish, to the
// microsecond, this room and this round.
type position struct {
	at    time.Time
	room  string
	round int
}

// maxCursor is longer than any cursor this package writes.
const maxCursor = 128

// cursorOf is the cursor after a row: opaque to the client, and nothing but
// the row's finish, room and round underneath.
func cursorOf(row model.Result) string {
	raw := strconv.FormatInt(row.FinishedAt.UnixMicro(), 10) + "." + row.RoomID + "." + strconv.Itoa(row.Round)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// readCursor is a cursor this package wrote, or not one at all. Empty is the
// first page. One written before rounds, with no round in it, is not one.
func readCursor(s string) (*position, bool) {
	if s == "" {
		return nil, true
	}
	if len(s) > maxCursor {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 || !ids.Is(ids.Room, parts[1]) {
		return nil, false
	}
	micros, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || micros < 0 {
		return nil, false
	}
	round, ok := readRound(parts[2])
	if !ok || round == 0 {
		return nil, false
	}
	return &position{at: time.UnixMicro(micros).UTC(), room: parts[1], round: round}, true
}
