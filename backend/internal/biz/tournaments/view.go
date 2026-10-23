package tournaments

// A tournament as everybody reads it (docs/TOURNAMENTS.md, "Reading them"):
// the views, defined here once for the app's routes and the console's, and
// who may read what.

import (
	"context"
	"sort"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Tournament is a tournament as a player or an admin reads it: everything the
// app draws its page from, so one read after any change is enough.
type Tournament struct {
	ID         string               `json:"id" example:"trn_7f3k9d2m8q1x4b6n"`
	Code       string               `json:"code" example:"T4K9QX" doc:"Six symbols, upper case: what a friend types to join. Free again once it is done or cancelled."`
	Link       string               `json:"link" doc:"LINK_PUBLIC_URL/t/CODE: the app opens it where it is installed, a page with the tournament and the code answers everywhere else."`
	Name       string               `json:"name" example:"بطولة الجمعة"`
	Game       rooms.Game           `json:"game"`
	Format     string               `json:"format" enum:"knockout,league"`
	Visibility string               `json:"visibility" enum:"private,friends,public"`
	Status     string               `json:"status" enum:"open,running,done,cancelled"`
	Official   bool                 `json:"official" doc:"Made by the platform, from the console: public, with no organizer."`
	Organizer  *players.Card        `json:"organizer" doc:"The player who made it; null for an official one."`
	BestOf     int                  `json:"best_of" doc:"Knockout: a match's length, 1, 3 or 5. League: 1."`
	Need       int                  `json:"need" doc:"Knockout: the round wins that take a match. League: 1."`
	Rounds     int                  `json:"rounds" doc:"League: the rounds it plays. Knockout: 0."`
	Size       int                  `json:"size" doc:"The most entrants it takes."`
	Min        int                  `json:"min" doc:"The entrants it needs to start."`
	Stages     int                  `json:"stages" doc:"Knockout: the bracket's stages, the last the final, from the draw (0 before it). League: 1."`
	Stage      *int                 `json:"stage" nullable:"true" doc:"Knockout: the earliest stage with a match undecided. Null otherwise."`
	Entrants   []TournamentEntrant  `json:"entrants" doc:"By seed: the order they came while it is open, the draw's after."`
	Matches    []TournamentMatch    `json:"matches" doc:"By stage, then slot. Empty before the draw."`
	Standings  []TournamentStanding `json:"standings" doc:"A league's table; a knockout's entrants with a place, by place."`
	Champion   *players.Card        `json:"champion" doc:"Once it is done; null otherwise, and for a knockout nobody won."`
	You        *TournamentYou       `json:"you" doc:"The caller's place in it; null for a caller who is neither an entrant nor its organizer, and for the console."`
	EndReason  *string              `json:"end_reason" nullable:"true" enum:"played,early,withdrawals,organizer,admin,too_few,expired"`
	StartsAt   *string              `json:"starts_at" nullable:"true" doc:"When the clock starts it, if it was given a time."`
	CreatedAt  string               `json:"created_at"`
	StartedAt  *string              `json:"started_at" nullable:"true"`
	EndedAt    *string              `json:"ended_at" nullable:"true"`
}

// TournamentEntrant is one entrant: the card, the seed, where they stand and
// their place once known.
type TournamentEntrant struct {
	players.Card
	Seed   int    `json:"seed"`
	Status string `json:"status" enum:"in,out,withdrawn" doc:"in: still in it; out: lost a knockout match; withdrawn: left, or was removed, once it ran."`
	Place  *int   `json:"place" nullable:"true" doc:"The final place: a knockout's as they go out, everybody's at a league's end."`
}

// TournamentMatch is one match of the bracket, or the league's one.
type TournamentMatch struct {
	ID        string      `json:"id" example:"trm_7f3k9d2m8q1x4b6n"`
	Stage     int         `json:"stage" doc:"1 is the first; stages is the final."`
	Slot      int         `json:"slot" doc:"From 0, within the stage."`
	Status    string      `json:"status" enum:"waiting,ready,done" doc:"waiting: a side is still to be decided; ready: it can be played; done: decided."`
	Sides     []MatchSide `json:"sides" doc:"A knockout match's two sides; empty for a league's."`
	Winner    *string     `json:"winner" nullable:"true" doc:"The winner's id once decided; null for a match nobody won."`
	DecidedBy *string     `json:"decided_by" nullable:"true" enum:"play,bye,walkover,award,standings"`
	Played    int         `json:"played" doc:"The rounds counted, in any of its rooms."`
	Room      *MatchRoom  `json:"room" doc:"Its open room, while it has one: play joins it."`
	ReadyAt   *string     `json:"ready_at" nullable:"true"`
	DecidedAt *string     `json:"decided_at" nullable:"true"`
}

// MatchSide is one side of a knockout match.
type MatchSide struct {
	Player    *players.Card `json:"player" doc:"Null while the side is still to be decided, and for a void one."`
	Wins      int           `json:"wins" doc:"Rounds won in this match."`
	Void      bool          `json:"void" doc:"Nobody will ever take it: a bye, or a match before it that nobody won."`
	Withdrawn bool          `json:"withdrawn"`
}

// MatchRoom is a match's open room.
type MatchRoom struct {
	ID     string `json:"id" example:"room_7f3k9d2m8q1x4b6n"`
	Code   string `json:"code" example:"K7M2QX"`
	Status string `json:"status" enum:"lobby,playing"`
	Round  int    `json:"round"`
}

// TournamentStanding is one line of the standings.
type TournamentStanding struct {
	Rank      int          `json:"rank" doc:"A league's place; a knockout's place, which several share below the final."`
	Player    players.Card `json:"player"`
	Points    int          `json:"points" doc:"League: a point for every ranked entrant beaten in a round."`
	Wins      int          `json:"wins" doc:"League: first places."`
	Played    int          `json:"played" doc:"League: rounds ranked in."`
	Withdrawn bool         `json:"withdrawn"`
}

// TournamentYou is the caller's place in a tournament.
type TournamentYou struct {
	Entrant   bool    `json:"entrant" doc:"A current entrant."`
	Organizer bool    `json:"organizer"`
	Status    *string `json:"status" nullable:"true" enum:"in,out,withdrawn" doc:"Null for an organizer who is not an entrant."`
	Match     *string `json:"match" nullable:"true" doc:"The caller's match still to be played (ready or waiting), or the league's while they are in it."`
}

// stamp is the one timestamp format the API answers in: UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func stampOf(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := stamp(*t)
	return &s
}

// readable is who may read a tournament: its organizer, any entrant it has
// or had (e is their row), anybody for a public one, and the organizer's
// friends for a friends one.
func (s *Service) readable(ctx context.Context, t *model.Tournament, e *model.TournamentEntrant, playerID string) (bool, error) {
	if e != nil || t.Organizer(playerID) {
		return true, nil
	}
	switch t.Visibility {
	case enum.TournamentPublic:
		return true, nil
	case enum.TournamentFriends:
		return s.friendOfOrganizer(ctx, t, playerID)
	}
	return false, nil
}

// Get is one tournament for anybody who may read it: not_found for everybody
// else. An admin reads every one.
func (s *Service) Get(ctx context.Context, by Actor, tournamentID string) (*Tournament, error) {
	t, err := tournamentByID(ctx, s.db, tournamentID, false)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if t == nil {
		return nil, errNotFound
	}
	st, err := loadRest(ctx, s.db, t)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if by.Admin == "" {
		ok, err := s.readable(ctx, t, st.byPlayer[by.Player], by.Player)
		if err != nil {
			return nil, asError(err)
		}
		if !ok {
			return nil, errNotFound
		}
	}
	return s.view(ctx, st, by.Player)
}

// ByCode is the live tournament holding a code, for anybody holding it.
func (s *Service) ByCode(ctx context.Context, playerID, typed string) (*Tournament, error) {
	code := normalizeCode(typed)
	if !s.validCode(code) {
		return nil, errors.Invalid(map[string]string{"code": "must_be_6_symbols"})
	}
	t, err := liveByCode(ctx, s.db, code, false)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if t == nil {
		return nil, asError(s.noLiveCode(ctx, code))
	}
	st, err := loadRest(ctx, s.db, t)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return s.view(ctx, st, playerID)
}

// LinkCard is a live tournament as its link's page shows it to anybody
// holding the link: its name, its game and its code. Nil when no live
// tournament holds the code, whatever the reason.
type LinkCard struct {
	Code string
	Name string
	Game rooms.Game
}

func (s *Service) LinkCard(ctx context.Context, typed string) (*LinkCard, error) {
	code := normalizeCode(typed)
	if !s.validCode(code) {
		return nil, nil
	}
	t, err := liveByCode(ctx, s.db, code, false)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if t == nil {
		return nil, nil
	}
	g, err := gameRow(ctx, s.db, t.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &LinkCard{Code: t.Code, Name: t.Name, Game: s.gameCard(ctx, g, t.GameID)}, nil
}

// gameCard is as much of the game as a tournament draws.
func (s *Service) gameCard(ctx context.Context, g *model.Game, id string) rooms.Game {
	if g == nil {
		return rooms.Game{ID: id}
	}
	return rooms.Game{ID: g.ID, Name: g.Name, Icon: games.IconURL(ctx, s.Files, g), Orientation: string(g.Orientation)}
}

// cards reads the cards of these players at once, the pictures signed.
func (s *Service) cards(ctx context.Context, ids []string) (map[string]players.Card, error) {
	out := map[string]players.Card{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID        string
		Nickname  string
		Handle    string
		AvatarKey string
	}
	err := data.Conn(ctx, s.db).Raw(`SELECT id, nickname, handle, avatar_key FROM players WHERE id IN ?`, ids).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = players.NewCard(ctx, s.Files, s.cfg.AvatarTTL, r.ID, r.Nickname, r.Handle, r.AvatarKey)
	}
	return out, nil
}

// cardOf is a player's card from the ones read, or the bare id.
func cardOf(cards map[string]players.Card, id string) players.Card {
	if c, ok := cards[id]; ok {
		return c
	}
	return players.Card{ID: id}
}

// view is the tournament as viewer reads it ("" for the console).
func (s *Service) view(ctx context.Context, st *state, viewer string) (*Tournament, error) {
	t := st.t
	g, err := gameRow(ctx, s.db, t.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	matchIDs := make([]string, 0, len(st.matches))
	for _, m := range st.matches {
		matchIDs = append(matchIDs, m.ID)
	}
	plays, err := playsOf(ctx, s.db, matchIDs)
	if err != nil {
		return nil, errors.Internal(err)
	}
	open, err := openRooms(ctx, s.db, matchIDs)
	if err != nil {
		return nil, errors.Internal(err)
	}
	ids := make([]string, 0, len(st.entrants)+2)
	for _, e := range st.entrants {
		ids = append(ids, e.PlayerID)
	}
	if t.OrganizerID != nil {
		ids = append(ids, *t.OrganizerID)
	}
	cards, err := s.cards(ctx, ids)
	if err != nil {
		return nil, errors.Internal(err)
	}

	out := &Tournament{
		ID: t.ID, Code: t.Code, Link: s.cfg.LinkURL + "/t/" + t.Code, Name: t.Name,
		Game: s.gameCard(ctx, g, t.GameID), Format: string(t.Format), Visibility: string(t.Visibility),
		Status: string(t.Status), Official: t.Official(), BestOf: t.BestOf, Need: t.Need(), Rounds: t.Rounds,
		Size: t.Size, Min: minEntrants(t), Stages: t.Stages, Stage: st.stage(),
		Entrants: []TournamentEntrant{}, Matches: []TournamentMatch{}, Standings: []TournamentStanding{},
		StartsAt: stampOf(t.StartsAt), CreatedAt: stamp(t.CreatedAt),
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
	if t.EndReason != nil {
		why := string(*t.EndReason)
		out.EndReason = &why
	}

	for _, e := range st.entrants {
		if e.Removed && t.Status == enum.TournamentOpen {
			continue
		}
		out.Entrants = append(out.Entrants, TournamentEntrant{
			Card: cardOf(cards, e.PlayerID), Seed: e.Seed, Status: st.standing(e), Place: e.Place,
		})
	}

	byMatch := map[string][]model.TournamentPlay{}
	for _, p := range plays {
		byMatch[p.MatchID] = append(byMatch[p.MatchID], p)
	}
	for _, m := range st.matches {
		out.Matches = append(out.Matches, st.matchView(m, byMatch[m.ID], open, cards))
	}

	switch {
	case t.Format == enum.FormatLeague && t.Status != enum.TournamentOpen:
		for i, row := range st.table(plays) {
			out.Standings = append(out.Standings, TournamentStanding{
				Rank: i + 1, Player: cardOf(cards, row.player), Points: row.points, Wins: row.wins,
				Played: row.played, Withdrawn: row.withdrawn,
			})
		}
	case t.Format == enum.FormatKnockout:
		placed := make([]*model.TournamentEntrant, 0, len(st.entrants))
		for _, e := range st.entrants {
			if e.Place != nil {
				placed = append(placed, e)
			}
		}
		sort.SliceStable(placed, func(i, j int) bool { return *placed[i].Place < *placed[j].Place })
		for _, e := range placed {
			out.Standings = append(out.Standings, TournamentStanding{
				Rank: *e.Place, Player: cardOf(cards, e.PlayerID), Withdrawn: !e.Current(),
			})
		}
	}

	out.You = st.you(viewer)
	return out, nil
}

// stage is a knockout's earliest stage with a match undecided, or nil.
func (st *state) stage() *int {
	if st.t.Format != enum.FormatKnockout {
		return nil
	}
	for _, m := range st.matches {
		if m.Status != enum.MatchDone {
			stage := m.Stage
			return &stage
		}
	}
	return nil
}

// standing is where an entrant stands: withdrawn, out of a knockout, or in.
func (st *state) standing(e *model.TournamentEntrant) string {
	switch {
	case !e.Current():
		return "withdrawn"
	case st.t.Format == enum.FormatKnockout && e.Place != nil && *e.Place > 1:
		return "out"
	}
	return "in"
}

// matchView draws one match with its tally and its open room.
func (st *state) matchView(m *model.TournamentMatch, plays []model.TournamentPlay, open map[string]openRoom,
	cards map[string]players.Card) TournamentMatch {
	out := TournamentMatch{
		ID: m.ID, Stage: m.Stage, Slot: m.Slot, Status: string(m.Status), Sides: []MatchSide{},
		Winner: m.WinnerID, Played: len(plays), ReadyAt: stampOf(m.ReadyAt), DecidedAt: stampOf(m.DecidedAt),
	}
	if m.DecidedBy != nil {
		by := string(*m.DecidedBy)
		out.DecidedBy = &by
	}
	if r, ok := open[m.ID]; ok {
		out.Room = &MatchRoom{ID: r.ID, Code: r.Code, Status: r.Status, Round: r.Round}
	}
	if st.t.Format != enum.FormatKnockout {
		return out
	}
	wins := winsOf(plays)
	for _, side := range []struct {
		player *string
		void   bool
	}{{m.PlayerA, m.VoidA}, {m.PlayerB, m.VoidB}} {
		s := MatchSide{Void: side.void}
		if side.player != nil {
			c := cardOf(cards, *side.player)
			s.Player, s.Wins = &c, wins[*side.player]
			s.Withdrawn = !st.isCurrent(*side.player)
		}
		out.Sides = append(out.Sides, s)
	}
	return out
}

// you is the viewer's place: nil for a viewer who is neither an entrant nor
// the organizer.
func (st *state) you(viewer string) *TournamentYou {
	if viewer == "" {
		return nil
	}
	e := st.byPlayer[viewer]
	if e != nil && e.Removed && st.t.Status == enum.TournamentOpen {
		e = nil
	}
	organizer := st.t.Organizer(viewer)
	if e == nil && !organizer {
		return nil
	}
	out := &TournamentYou{Organizer: organizer}
	if e != nil {
		standing := st.standing(e)
		out.Entrant, out.Status = e.Current(), &standing
		if m := st.yourMatch(viewer); m != nil {
			out.Match = &m.ID
		}
	}
	return out
}

// yourMatch is the player's match still to be played: a knockout's ready or
// waiting match of theirs, or the league's while they are in it and it runs.
func (st *state) yourMatch(playerID string) *model.TournamentMatch {
	if st.t.Status != enum.TournamentRunning || !st.isCurrent(playerID) {
		return nil
	}
	for _, m := range st.matches {
		if m.Status == enum.MatchDone {
			continue
		}
		if st.t.Format == enum.FormatLeague || m.Has(playerID) {
			return m
		}
	}
	return nil
}
