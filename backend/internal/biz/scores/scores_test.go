package scores

import (
	"context"
	stderrors "errors"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/biztest"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

const reflex, tap = "arcade.reflex", "fixture.tap"

func f(v float64) *float64 { return &v }

type fixture struct {
	t   *testing.T
	ctx context.Context
	db  *gorm.DB
	s   *Service
}

// newFixture is two games: one with a time board (lowest best, bounded) and a
// points board (highest best), and one with no boards at all.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := biztest.DB(t)
	m := model.Manifest{Name: "أسرع إصبع", Version: "1.0.0", Entry: "https://games.example/reflex/"}
	m.Scores.Boards = []model.Board{
		{ID: "reaction", Order: enum.ScoreLow, Min: f(100), Max: f(3000)},
		{ID: "points", Order: enum.ScoreHigh},
	}
	biztest.SeedGame(t, db, reflex, m)
	biztest.SeedGame(t, db, tap, model.Manifest{Name: "سباق النقر", Version: "1.0.0", Entry: "https://games.example/tap/"})
	return &fixture{t: t, ctx: context.Background(), db: db, s: &Service{DB: db}}
}

// player is a new player with no phone: a board reads nothing of one.
func (x *fixture) player(name string) *model.Player {
	x.t.Helper()
	return biztest.SeedPlayer(x.t, x.db, "", name)
}

func (x *fixture) friends(a, b *model.Player) {
	x.t.Helper()
	lo, hi := model.Pair(a.ID, b.ID)
	now := data.Now()
	err := x.db.Create(&model.Friendship{PlayerLo: lo, PlayerHi: hi, Status: enum.FriendshipAccepted, RequestedBy: a.ID, CreatedAt: now, RespondedAt: &now}).Error
	if err != nil {
		x.t.Fatal(err)
	}
}

func (x *fixture) send(p *model.Player, board string, score float64) *Sent {
	x.t.Helper()
	got, err := x.s.Submit(x.ctx, p.ID, reflex, board, &score)
	if err != nil {
		x.t.Fatalf("%s %v: %v", board, score, err)
	}
	return got
}

// refused checks the refusal and answers its field problems.
func refused(t *testing.T, err error, status int, code string) map[string]string {
	t.Helper()
	var e *errors.Error
	if !stderrors.As(err, &e) || e.Status != status || e.Code != code {
		t.Fatalf("got %v, want %d %s", err, status, code)
	}
	return e.Fields
}

// A board keeps the best in its own order and counts every score; only a
// strictly better one is a new best.
func TestABoardKeepsTheBest(t *testing.T) {
	x := newFixture(t)
	me := x.player("Zayn")

	for _, c := range []struct {
		board   string
		score   float64
		best    float64
		newBest bool
		plays   int
	}{
		{"reaction", 300, 300, true, 1}, // the first is the best
		{"reaction", 250, 250, true, 2}, // faster
		{"reaction", 400, 250, false, 3},
		{"reaction", 250, 250, false, 4}, // equal is not new
		{"points", 40, 40, true, 1},      // another board, its own line
		{"points", 90, 90, true, 2},      // higher
		{"points", 10, 90, false, 3},
	} {
		got := x.send(me, c.board, c.score)
		if got.Board != c.board || got.Score != c.score || got.Best != c.best || got.NewBest != c.newBest || got.Plays != c.plays {
			t.Errorf("%s %v: got %+v, want best %v new %v plays %d", c.board, c.score, got, c.best, c.newBest, c.plays)
		}
	}
}

// The refusals, in the contract's order: the game first, then every field
// problem at once, and the bounds are the board's own.
func TestScoresAreRefused(t *testing.T) {
	x := newFixture(t)
	me := x.player("Zayn")
	score := 200.0

	_, err := x.s.Submit(x.ctx, me.ID, "arcade.nope", "reaction", &score)
	refused(t, err, http.StatusNotFound, "not_found")

	for _, c := range []struct {
		game, board string
		score       *float64
		want        map[string]string
	}{
		{reflex, "", nil, map[string]string{"board": "required", "score": "required"}},
		{reflex, "sprint", &score, map[string]string{"board": "unknown"}},
		{tap, "reaction", &score, map[string]string{"board": "unknown"}},
		{reflex, "reaction", f(99), map[string]string{"score": "below_min"}},
		{reflex, "reaction", f(3001), map[string]string{"score": "above_max"}},
	} {
		_, err := x.s.Submit(x.ctx, me.ID, c.game, c.board, c.score)
		fields := refused(t, err, http.StatusUnprocessableEntity, "invalid_fields")
		if len(fields) != len(c.want) {
			t.Errorf("%s %s: got %v, want %v", c.game, c.board, fields, c.want)
		}
		for k, v := range c.want {
			if fields[k] != v {
				t.Errorf("%s %s: %s got %q, want %q", c.game, c.board, k, fields[k], v)
			}
		}
	}

	// The bounds themselves are in, and a board with none takes anything.
	x.send(me, "reaction", 100)
	x.send(me, "reaction", 3000)
	x.send(me, "points", -5)

	// A game taken out of the store takes no more scores.
	if err := x.db.Model(&model.Game{}).Where("id = ?", reflex).Update("listing", enum.Unpublished).Error; err != nil {
		t.Fatal(err)
	}
	_, err = x.s.Submit(x.ctx, me.ID, reflex, "reaction", &score)
	refused(t, err, http.StatusNotFound, "not_found")
}

// The limit is per player and game.
func TestTheHourlyLimit(t *testing.T) {
	x := newFixture(t)
	me := x.player("Zayn")
	lim := &countingLimiter{max: 2}
	x.s.Limiter = lim
	x.send(me, "reaction", 300)
	x.send(me, "points", 1)
	score := 200.0
	_, err := x.s.Submit(x.ctx, me.ID, reflex, "reaction", &score)
	refused(t, err, http.StatusTooManyRequests, "too_many_requests")
	if lim.keys[0] != "score:"+me.ID+":"+reflex {
		t.Fatalf("key %q", lim.keys[0])
	}
}

// The board ranks the caller and their friends in its order: equal bests
// share a rank, the first to get there listed first; a stranger, a switched
// off friend and another board are left out.
func TestTheBoardAmongFriends(t *testing.T) {
	x := newFixture(t)
	me, sara, ali, mona, omar, stranger := x.player("Zayn"), x.player("Sara"), x.player("Ali"),
		x.player("Mona"), x.player("Omar"), x.player("Stranger")
	for _, p := range []*model.Player{sara, ali, mona, omar} {
		x.friends(me, p)
	}

	x.send(ali, "reaction", 240)
	x.send(sara, "reaction", 300)
	x.send(sara, "reaction", 210) // her best, two plays
	x.send(me, "reaction", 240)   // equal to Ali's, but later
	x.send(stranger, "reaction", 150)
	x.send(mona, "points", 99) // another board
	x.send(omar, "reaction", 120)
	if err := x.db.Model(&model.Player{}).Where("id = ?", omar.ID).Update("disabled", true).Error; err != nil {
		t.Fatal(err)
	}

	board, err := x.s.Board(x.ctx, me.ID, reflex, "reaction", "")
	if err != nil {
		t.Fatal(err)
	}
	if board.Game.ID != reflex || board.Board != "reaction" || board.Order != "low" {
		t.Fatalf("board %+v", board)
	}
	want := []struct {
		id    string
		rank  int
		best  float64
		plays int
		you   bool
	}{
		{sara.ID, 1, 210, 2, false},
		{ali.ID, 2, 240, 1, false},
		{me.ID, 2, 240, 1, true},
	}
	if len(board.Entries) != len(want) {
		t.Fatalf("entries %+v", board.Entries)
	}
	for i, w := range want {
		e := board.Entries[i]
		if e.Player.ID != w.id || e.Rank != w.rank || e.Best != w.best || e.Plays != w.plays || e.You != w.you {
			t.Errorf("%d: got %+v, want %+v", i, e, w)
		}
	}

	// From Sara's side: herself and me, her only friend.
	board, err = x.s.Board(x.ctx, sara.ID, reflex, "reaction", ScopeFriends)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Entries) != 2 || board.Entries[0].Player.ID != sara.ID || !board.Entries[0].You || board.Entries[1].Player.ID != me.ID {
		t.Fatalf("Sara's board %+v", board.Entries)
	}

	// A points board ranks the highest first; nobody's score is empty, not a refusal.
	x.send(me, "points", 10)
	board, err = x.s.Board(x.ctx, me.ID, reflex, "points", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Entries) != 2 || board.Entries[0].Player.ID != mona.ID || board.Entries[1].Player.ID != me.ID {
		t.Fatalf("points %+v", board.Entries)
	}
	board, err = x.s.Board(x.ctx, stranger.ID, reflex, "points", "")
	if err != nil || len(board.Entries) != 0 {
		t.Fatalf("an empty board: %+v %v", board, err)
	}
}

func TestTheBoardsRefusals(t *testing.T) {
	x := newFixture(t)
	me := x.player("Zayn")
	_, err := x.s.Board(x.ctx, me.ID, reflex, "reaction", "everybody")
	refused(t, err, http.StatusUnprocessableEntity, "invalid_fields")
	for _, c := range [][2]string{{"arcade.nope", "reaction"}, {reflex, "sprint"}, {tap, "reaction"}} {
		_, err := x.s.Board(x.ctx, me.ID, c[0], c[1], "")
		refused(t, err, http.StatusNotFound, "not_found")
	}
}

type countingLimiter struct {
	max  int
	keys []string
}

func (c *countingLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, int, error) {
	c.keys = append(c.keys, key)
	return len(c.keys) <= c.max, 60, nil
}
