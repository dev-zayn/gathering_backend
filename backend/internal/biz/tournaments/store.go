package tournaments

// The queries, as functions over a connection: inside a transaction they join
// it (data.Conn), outside it they use the pool. Every change to a tournament
// is made holding its row (lockTournament), and reads the whole of it — its
// entrants and its matches — once, as a state: a tournament is at most 64
// entrants and 63 matches.

import (
	"context"
	stderrors "errors"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

func tournamentByID(ctx context.Context, db *gorm.DB, id string, lock bool) (*model.Tournament, error) {
	q := data.Conn(ctx, db)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var t model.Tournament
	err := q.Take(&t, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// liveByCode is the open or running tournament holding a code, held when
// lock is set. At most one: the partial unique index says so.
func liveByCode(ctx context.Context, db *gorm.DB, code string, lock bool) (*model.Tournament, error) {
	q := data.Conn(ctx, db)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var rows []model.Tournament
	err := q.Where("code = ? AND status IN ?", code, []enum.TournamentStatus{enum.TournamentOpen, enum.TournamentRunning}).
		Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// finishedWithCode says whether a tournament that is over held this code:
// "the code you typed was a tournament, and it is over".
func finishedWithCode(ctx context.Context, db *gorm.DB, code string) (bool, error) {
	var found bool
	err := data.Conn(ctx, db).Raw(`SELECT EXISTS (SELECT 1 FROM tournaments WHERE code = ? AND status IN ?)`,
		code, []enum.TournamentStatus{enum.TournamentDone, enum.TournamentCancelled}).Scan(&found).Error
	return found, err
}

// entrantRow is one player's row in a tournament, or nil.
func entrantRow(ctx context.Context, db *gorm.DB, tournamentID, playerID string) (*model.TournamentEntrant, error) {
	var rows []model.TournamentEntrant
	err := data.Conn(ctx, db).Where("tournament_id = ? AND player_id = ?", tournamentID, playerID).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func matchByID(ctx context.Context, db *gorm.DB, id string) (*model.TournamentMatch, error) {
	var m model.TournamentMatch
	err := data.Conn(ctx, db).Take(&m, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func entrantsOf(ctx context.Context, db *gorm.DB, tournamentID string) ([]*model.TournamentEntrant, error) {
	var rows []*model.TournamentEntrant
	err := data.Conn(ctx, db).Where("tournament_id = ?", tournamentID).Order("seed, joined_at, player_id").Find(&rows).Error
	return rows, err
}

func matchesOf(ctx context.Context, db *gorm.DB, tournamentID string) ([]*model.TournamentMatch, error) {
	var rows []*model.TournamentMatch
	err := data.Conn(ctx, db).Where("tournament_id = ?", tournamentID).Order("stage, slot").Find(&rows).Error
	return rows, err
}

// playsOf is every counted round of these matches, oldest first.
func playsOf(ctx context.Context, db *gorm.DB, matchIDs []string) ([]model.TournamentPlay, error) {
	if len(matchIDs) == 0 {
		return nil, nil
	}
	var rows []model.TournamentPlay
	err := data.Conn(ctx, db).Where("match_id IN ?", matchIDs).Order("played_at, room_id, round").Find(&rows).Error
	return rows, err
}

// openRooms is the open room of each of these matches, by match.
func openRooms(ctx context.Context, db *gorm.DB, matchIDs []string) (map[string]openRoom, error) {
	out := map[string]openRoom{}
	if len(matchIDs) == 0 {
		return out, nil
	}
	var rows []openRoom
	err := data.Conn(ctx, db).Raw(`
		SELECT match_id, id, code, status, round FROM rooms
		WHERE match_id IN ? AND status <> ?`, matchIDs, enum.RoomEnded).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.MatchID] = r
	}
	return out, nil
}

// openRoom is a match's open room, as the views name it.
type openRoom struct {
	MatchID string
	ID      string
	Code    string
	Status  string
	Round   int
}

// insertTournament writes the row unless a live tournament already holds the
// code, and says whether it did: a collision draws again in the same
// transaction.
func insertTournament(ctx context.Context, db *gorm.DB, t *model.Tournament) (bool, error) {
	res := data.Conn(ctx, db).Exec(`
		INSERT INTO tournaments (id, code, game_id, organizer_id, admin_id, name, format, visibility, status,
		                         best_of, rounds, size, config, stages, starts_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT (code) WHERE status IN ('open', 'running') DO NOTHING`,
		t.ID, t.Code, t.GameID, t.OrganizerID, t.AdminID, t.Name, t.Format, t.Visibility, t.Status,
		t.BestOf, t.Rounds, t.Size, t.Config, t.StartsAt, t.CreatedAt)
	return res.RowsAffected == 1, res.Error
}

// state is one tournament as a change sees it: the row, held, its entrants
// and its matches, read once, changed in memory, and written back by save.
// What the change did beyond its rows — the matches that turned ready, those
// decided, the tournament's end — is kept here for its notifications and the
// rooms to close once it commits.
type state struct {
	t        *model.Tournament
	entrants []*model.TournamentEntrant
	byPlayer map[string]*model.TournamentEntrant
	matches  []*model.TournamentMatch
	at       map[[2]int]*model.TournamentMatch

	changedT bool
	changedE map[string]bool
	changedM map[string]bool
	created  []*model.TournamentMatch

	// ready is the matches that turned ready in this change; closing those
	// whose open room must close once it commits (decided, or their
	// tournament over); began says it was the draw, ended that it took the
	// tournament to its end.
	ready   []*model.TournamentMatch
	closing []string
	began   bool
	ended   bool

	now time.Time
}

// load reads a tournament whole, holding its row: nil when there is none.
// Inside the caller's transaction.
func load(ctx context.Context, db *gorm.DB, tournamentID string) (*state, error) {
	t, err := tournamentByID(ctx, db, tournamentID, true)
	if err != nil || t == nil {
		return nil, err
	}
	return loadRest(ctx, db, t)
}

// loadRest reads the entrants and the matches of a tournament already held.
func loadRest(ctx context.Context, db *gorm.DB, t *model.Tournament) (*state, error) {
	entrants, err := entrantsOf(ctx, db, t.ID)
	if err != nil {
		return nil, err
	}
	matches, err := matchesOf(ctx, db, t.ID)
	if err != nil {
		return nil, err
	}
	st := &state{
		t: t, entrants: entrants, matches: matches,
		byPlayer: make(map[string]*model.TournamentEntrant, len(entrants)),
		at:       make(map[[2]int]*model.TournamentMatch, len(matches)),
		changedE: map[string]bool{}, changedM: map[string]bool{},
		now: data.Now(),
	}
	for _, e := range entrants {
		st.byPlayer[e.PlayerID] = e
	}
	for _, m := range matches {
		st.at[[2]int{m.Stage, m.Slot}] = m
	}
	return st, nil
}

// match is one of the tournament's matches by id, or nil.
func (st *state) match(id string) *model.TournamentMatch {
	for _, m := range st.matches {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// current is the entrants still in it: neither withdrawn nor removed, in seed
// order.
func (st *state) current() []*model.TournamentEntrant {
	out := make([]*model.TournamentEntrant, 0, len(st.entrants))
	for _, e := range st.entrants {
		if e.Current() {
			out = append(out, e)
		}
	}
	return out
}

// isCurrent says whether the player is a current entrant.
func (st *state) isCurrent(playerID string) bool {
	e := st.byPlayer[playerID]
	return e != nil && e.Current()
}

// league is the league's one match, or nil.
func (st *state) league() *model.TournamentMatch {
	if st.t.Format != enum.FormatLeague || len(st.matches) == 0 {
		return nil
	}
	return st.matches[0]
}

// touchT, touchE and touchM mark what save writes back.
func (st *state) touchT()                           { st.changedT = true }
func (st *state) touchE(e *model.TournamentEntrant) { st.changedE[e.PlayerID] = true }
func (st *state) touchM(m *model.TournamentMatch)   { st.changedM[m.ID] = true }

// addMatch is a new match, inserted by save.
func (st *state) addMatch(m *model.TournamentMatch) {
	st.matches = append(st.matches, m)
	st.at[[2]int{m.Stage, m.Slot}] = m
	st.created = append(st.created, m)
}

// sortEntrants puts the entrants back in seed order, after the draw.
func (st *state) sortEntrants() {
	sort.SliceStable(st.entrants, func(i, j int) bool { return st.entrants[i].Seed < st.entrants[j].Seed })
}

// save writes back what the change touched, in the caller's transaction.
func (st *state) save(ctx context.Context, db *gorm.DB) error {
	conn := data.Conn(ctx, db)
	if st.changedT {
		t := st.t
		err := conn.Model(&model.Tournament{}).Where("id = ?", t.ID).Updates(map[string]any{
			"status": t.Status, "stages": t.Stages, "champion_id": t.ChampionID, "end_reason": t.EndReason,
			"started_at": t.StartedAt, "ended_at": t.EndedAt,
		}).Error
		if err != nil {
			return err
		}
	}
	created := map[string]bool{}
	if len(st.created) > 0 {
		if err := conn.Create(&st.created).Error; err != nil {
			return err
		}
		for _, m := range st.created {
			created[m.ID] = true
		}
	}
	for _, m := range st.matches {
		if !st.changedM[m.ID] || created[m.ID] {
			continue
		}
		err := conn.Model(&model.TournamentMatch{}).Where("id = ?", m.ID).Updates(map[string]any{
			"player_a": m.PlayerA, "player_b": m.PlayerB, "void_a": m.VoidA, "void_b": m.VoidB,
			"status": m.Status, "winner_id": m.WinnerID, "decided_by": m.DecidedBy,
			"ready_at": m.ReadyAt, "decided_at": m.DecidedAt,
		}).Error
		if err != nil {
			return err
		}
	}
	for _, e := range st.entrants {
		if !st.changedE[e.PlayerID] {
			continue
		}
		err := conn.Model(&model.TournamentEntrant{}).
			Where("tournament_id = ? AND player_id = ?", e.TournamentID, e.PlayerID).
			Updates(map[string]any{"seed": e.Seed, "left_at": e.LeftAt, "removed": e.Removed, "place": e.Place}).Error
		if err != nil {
			return err
		}
	}
	return nil
}
