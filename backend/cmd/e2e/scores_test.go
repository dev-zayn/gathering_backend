//go:build e2e

package e2e

import (
	"strconv"
	"testing"
	"time"
)

// Scores from a game played alone (docs/SCORES.md): the studio's board
// keeps each player's best in its order and counts every score; the
// refusals; the board among friends, ranked, a stranger left out.
func TestScores(t *testing.T) {
	id := "sweep." + strconv.FormatInt(time.Now().UnixNano(), 36)
	studio := serveStudio(t, id)

	admin := newClient(t)
	admin.origin = console
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, nil)
	admin.call("PATCH", "/v1/admin/games/"+id, map[string]any{"tagline": "نتائج السويب", "category": "speed"}, 200, nil)
	admin.upload("PUT", "/v1/admin/games/"+id+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)

	a, pa := player(t, "زين")
	b, pb := player(t, "Mariam")
	c, _ := player(t, "Stranger")
	aID, bID := pa["id"].(string), pb["id"].(string)

	// Not in the store yet: nothing takes a score.
	a.call("POST", "/v1/games/"+id+"/scores", map[string]any{"board": "sweep-time", "score": 300}, 404, nil)
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, nil)
	t.Cleanup(func() {
		admin.call("POST", "/v1/admin/games/"+id+"/suspend", map[string]any{"reason": "the scores sweep is over"}, 200, nil)
	})

	type sent struct {
		Board   string  `json:"board"`
		Score   float64 `json:"score"`
		Best    float64 `json:"best"`
		NewBest bool    `json:"new_best"`
		Plays   int     `json:"plays"`
	}
	for _, s := range []struct {
		score, best float64
		newBest     bool
		plays       int
	}{{300, 300, true, 1}, {450, 300, false, 2}, {240, 240, true, 3}} {
		var got sent
		a.call("POST", "/v1/games/"+id+"/scores", map[string]any{"board": "sweep-time", "score": s.score}, 200, &got)
		if got.Board != "sweep-time" || got.Score != s.score || got.Best != s.best || got.NewBest != s.newBest || got.Plays != s.plays {
			t.Fatalf("%v: %+v", s.score, got)
		}
	}
	t.Logf("ok   A's scores 300, 450, 240: best 240, three plays, new only when faster")

	var refused errorJSON
	for _, r := range []struct {
		body        map[string]any
		field, code string
	}{
		{map[string]any{"score": 200}, "board", "required"},
		{map[string]any{"board": "points", "score": 200}, "board", "unknown"},
		{map[string]any{"board": "sweep-time"}, "score", "required"},
		{map[string]any{"board": "sweep-time", "score": 20}, "score", "below_min"},
		{map[string]any{"board": "sweep-time", "score": 60001}, "score", "above_max"},
	} {
		a.call("POST", "/v1/games/"+id+"/scores", r.body, 422, &refused)
		if refused.Fields[r.field] != r.code {
			t.Fatalf("%v: %+v", r.body, refused)
		}
	}
	a.call("POST", "/v1/games/sweep.nothing/scores", map[string]any{"board": "sweep-time", "score": 300}, 404, nil)
	t.Logf("ok   the refusals: board required/unknown, score required/below_min/above_max, no such game")

	// B is A's friend and faster; the stranger is faster still, and nobody's.
	var standing friendshipJSON
	a.call("POST", "/v1/friends/requests", map[string]any{"player": bID}, 201, &standing)
	b.call("POST", "/v1/friends/requests/"+aID+"/accept", nil, 200, &standing)
	b.call("POST", "/v1/games/"+id+"/scores", map[string]any{"board": "sweep-time", "score": 210}, 200, nil)
	c.call("POST", "/v1/games/"+id+"/scores", map[string]any{"board": "sweep-time", "score": 150}, 200, nil)

	var board struct {
		Game    tileJSON `json:"game"`
		Board   string   `json:"board"`
		Order   string   `json:"order"`
		Entries []struct {
			Player cardPlayerJSON `json:"player"`
			Rank   int            `json:"rank"`
			Best   float64        `json:"best"`
			Plays  int            `json:"plays"`
			BestAt string         `json:"best_at"`
			You    bool           `json:"you"`
		} `json:"entries"`
	}
	a.call("GET", "/v1/games/"+id+"/scores/sweep-time?scope=friends", nil, 200, &board)
	if board.Game.ID != id || board.Board != "sweep-time" || board.Order != "low" || len(board.Entries) != 2 {
		t.Fatalf("the board: %+v", board)
	}
	first, second := board.Entries[0], board.Entries[1]
	if first.Player.ID != bID || first.Rank != 1 || first.Best != 210 || first.You || first.BestAt == "" ||
		second.Player.ID != aID || second.Rank != 2 || second.Best != 240 || second.Plays != 3 || !second.You {
		t.Fatalf("the board: %+v", board)
	}
	a.call("GET", "/v1/games/"+id+"/scores/sweep-time?scope=global", nil, 422, &refused)
	if refused.Fields["scope"] != "friends_only" {
		t.Fatalf("another scope: %+v", refused)
	}
	a.call("GET", "/v1/games/"+id+"/scores/points", nil, 404, nil)
	t.Logf("ok   the board among friends: B 1st (210), A 2nd (240, you), the stranger left out")
}
