package tournaments

// The lists (docs/TOURNAMENTS.md, "Reading them" and "The console"): a
// player's own tournaments, the official ones, and every one for the
// console; newest first, a page at a time by an opaque cursor.

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// TournamentCard is a tournament as a list draws it: enough for a row, the
// caller's own match when they have one to play.
type TournamentCard struct {
	ID         string         `json:"id" example:"trn_7f3k9d2m8q1x4b6n"`
	Code       string         `json:"code" example:"T4K9QX"`
	Name       string         `json:"name" example:"بطولة الجمعة"`
	Game       rooms.Game     `json:"game"`
	Format     string         `json:"format" enum:"knockout,league"`
	Visibility string         `json:"visibility" enum:"private,friends,public"`
	Status     string         `json:"status" enum:"open,running,done,cancelled"`
	Official   bool           `json:"official"`
	Organizer  *players.Card  `json:"organizer" doc:"Null for an official one."`
	Entrants   int            `json:"entrants" doc:"How many are in it: while it is open, those who joined; once it runs, those still in it."`
	Size       int            `json:"size"`
	BestOf     int            `json:"best_of"`
	Rounds     int            `json:"rounds"`
	Stage      *int           `json:"stage" nullable:"true" doc:"Knockout: the earliest stage with a match undecided."`
	Stages     int            `json:"stages"`
	Played     int            `json:"played" doc:"League: the rounds counted so far. Knockout: 0."`
	You        *TournamentYou `json:"you" doc:"Null for a caller who is neither an entrant nor its organizer, and for the console."`
	YourMatch  *YourMatch     `json:"your_match" doc:"The caller's match still to be played, else null."`
	Champion   *players.Card  `json:"champion"`
	StartsAt   *string        `json:"starts_at" nullable:"true"`
	CreatedAt  string         `json:"created_at"`
	StartedAt  *string        `json:"started_at" nullable:"true"`
	EndedAt    *string        `json:"ended_at" nullable:"true"`
}

// YourMatch is the caller's match on a card: where it stands, against whom,
// and its open room.
type YourMatch struct {
	ID       string        `json:"id" example:"trm_7f3k9d2m8q1x4b6n"`
	Status   string        `json:"status" enum:"waiting,ready"`
	Stage    int           `json:"stage"`
	Opponent *players.Card `json:"opponent" doc:"A knockout's other side, once decided; null for a league's and while it is to come."`
	Room     *MatchRoom    `json:"room"`
}

// TournamentPage is one page of a list.
type TournamentPage struct {
	Items      []TournamentCard `json:"items" doc:"Newest first."`
	NextCursor *string          `json:"next_cursor" nullable:"true" doc:"Hand it back as cursor for the next page; null on the last."`
}

// ListQuery is a player's list as the route reads it: whose (mine, the
// default, or public), which (active, the default, or past), one game, the
// cursor of the page before and the page's length.
type ListQuery struct {
	View   string
	State  string
	Game   string
	Cursor string
	Limit  int
}

// The pages' lengths.
const (
	listLimit    = 50
	consoleLimit = 100
	maxCursor    = 128
)

// List is the player's tournaments, or the official ones.
func (s *Service) List(ctx context.Context, playerID string, q ListQuery) (*TournamentPage, error) {
	bad := map[string]string{}
	var where []string
	var args []any
	switch q.View {
	case "", "mine":
		where = append(where, `(t.organizer_id = ? OR EXISTS (
			SELECT 1 FROM tournament_entrants e WHERE e.tournament_id = t.id AND e.player_id = ? AND NOT e.removed))`)
		args = append(args, playerID, playerID)
	case "public":
		where, args = append(where, "t.visibility = ?"), append(args, enum.TournamentPublic)
	default:
		bad["view"] = "unknown"
	}
	switch q.State {
	case "", "active":
		where, args = append(where, "t.status IN ?"), append(args, []enum.TournamentStatus{enum.TournamentOpen, enum.TournamentRunning})
	case "past":
		where, args = append(where, "t.status IN ?"), append(args, []enum.TournamentStatus{enum.TournamentDone, enum.TournamentCancelled})
	default:
		bad["state"] = "unknown"
	}
	if q.Limit < 1 || q.Limit > listLimit {
		bad["limit"] = "1_to_50"
	}
	return s.page(ctx, playerID, where, args, q.Game, q.Cursor, q.Limit, bad)
}

// ConsoleQuery is the console's list: a status (live, the default: open and
// running; open, running, done, cancelled, or all), one game, the cursor and
// the page's length.
type ConsoleQuery struct {
	Status string
	Game   string
	Cursor string
	Limit  int
}

// ConsoleList is every tournament, a player's too, for the console.
func (s *Service) ConsoleList(ctx context.Context, q ConsoleQuery) (*TournamentPage, error) {
	bad := map[string]string{}
	var where []string
	var args []any
	switch q.Status {
	case "", "live":
		where, args = append(where, "t.status IN ?"), append(args, []enum.TournamentStatus{enum.TournamentOpen, enum.TournamentRunning})
	case string(enum.TournamentOpen), string(enum.TournamentRunning), string(enum.TournamentDone), string(enum.TournamentCancelled):
		where, args = append(where, "t.status = ?"), append(args, q.Status)
	case "all":
	default:
		bad["status"] = "unknown"
	}
	if q.Limit < 1 || q.Limit > consoleLimit {
		bad["limit"] = "1_to_100"
	}
	return s.page(ctx, "", where, args, q.Game, q.Cursor, q.Limit, bad)
}

// page is one page of tournaments matching where, newest first, drawn as
// cards for viewer ("" for the console).
func (s *Service) page(ctx context.Context, viewer string, where []string, args []any, game, cursor string,
	limit int, bad map[string]string) (*TournamentPage, error) {
	after, ok := readCursor(cursor)
	if !ok {
		bad["cursor"] = "invalid"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	if game != "" {
		where, args = append(where, "t.game_id = ?"), append(args, game)
	}
	if after != nil {
		where = append(where, "(t.created_at < ? OR (t.created_at = ? AND t.id > ?))")
		args = append(args, after.at, after.at, after.id)
	}
	sql := `SELECT t.* FROM tournaments t`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += ` ORDER BY t.created_at DESC, t.id LIMIT ?`
	args = append(args, limit+1)

	var rows []model.Tournament
	if err := data.Conn(ctx, s.db).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, errors.Internal(err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	out := &TournamentPage{Items: make([]TournamentCard, 0, len(rows))}
	for i := range rows {
		card, err := s.card(ctx, &rows[i], viewer)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *card)
	}
	if more {
		last := rows[len(rows)-1]
		next := writeCursor(last.CreatedAt, last.ID)
		out.NextCursor = &next
	}
	return out, nil
}

// card is one tournament as a list's row. A list is a page of at most a
// hundred, and each card reads its tournament whole: no more than its page.
func (s *Service) card(ctx context.Context, t *model.Tournament, viewer string) (*TournamentCard, error) {
	st, err := loadRest(ctx, s.db, t)
	if err != nil {
		return nil, errors.Internal(err)
	}
	g, err := gameRow(ctx, s.db, t.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	ids := []string{}
	if t.OrganizerID != nil {
		ids = append(ids, *t.OrganizerID)
	}
	if t.ChampionID != nil {
		ids = append(ids, *t.ChampionID)
	}
	mine := st.yourMatch(viewer)
	var opponent *string
	if mine != nil && t.Format == enum.FormatKnockout {
		if opponent = mine.Other(viewer); opponent != nil {
			ids = append(ids, *opponent)
		}
	}
	cards, err := s.cards(ctx, ids)
	if err != nil {
		return nil, errors.Internal(err)
	}

	count := 0
	for _, e := range st.entrants {
		if e.Current() {
			count++
		}
	}
	played := 0
	if m := st.league(); m != nil {
		plays, err := playsOf(ctx, s.db, []string{m.ID})
		if err != nil {
			return nil, errors.Internal(err)
		}
		played = len(plays)
	}
	out := &TournamentCard{
		ID: t.ID, Code: t.Code, Name: t.Name, Game: s.gameCard(ctx, g, t.GameID), Format: string(t.Format),
		Visibility: string(t.Visibility), Status: string(t.Status), Official: t.Official(), Entrants: count,
		Size: t.Size, BestOf: t.BestOf, Rounds: t.Rounds, Stage: st.stage(), Stages: t.Stages, Played: played,
		You: st.you(viewer), StartsAt: stampOf(t.StartsAt), CreatedAt: stamp(t.CreatedAt),
		StartedAt: stampOf(t.StartedAt), EndedAt: stampOf(t.EndedAt),
	}
	if t.OrganizerID != nil {
		c := cardOf(cards, *t.OrganizerID)
		out.Organizer = &c
	}
	if t.ChampionID != nil {
		c := cardOf(cards, *t.ChampionID)
		out.Champion = &c
	}
	if mine != nil {
		ym := &YourMatch{ID: mine.ID, Status: string(mine.Status), Stage: mine.Stage}
		if opponent != nil {
			c := cardOf(cards, *opponent)
			ym.Opponent = &c
		}
		open, err := openRooms(ctx, s.db, []string{mine.ID})
		if err != nil {
			return nil, errors.Internal(err)
		}
		if r, ok := open[mine.ID]; ok {
			ym.Room = &MatchRoom{ID: r.ID, Code: r.Code, Status: r.Status, Round: r.Round}
		}
		out.YourMatch = ym
	}
	return out, nil
}

// position is where a page starts: after this creation, to the microsecond,
// and this tournament.
type position struct {
	at time.Time
	id string
}

// writeCursor is the cursor after a tournament: opaque to the caller.
func writeCursor(at time.Time, id string) string {
	raw := strconv.FormatInt(at.UnixMicro(), 10) + "." + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// readCursor is a cursor a list wrote, or not one at all. Empty is the first
// page.
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
	micros, id, found := strings.Cut(string(raw), ".")
	if !found || !ids.Is(ids.Tournament, id) {
		return nil, false
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || n < 0 {
		return nil, false
	}
	return &position{at: time.UnixMicro(n).UTC(), id: id}, true
}
