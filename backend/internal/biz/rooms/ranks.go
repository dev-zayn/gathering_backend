package rooms

import (
	"sort"

	"github.com/jamaa/gathering/backend/internal/data/model"
)

// ScoreIn is one line of an end as the host sent it: a player, a score, and a
// rank when the game decided one itself.
type ScoreIn struct {
	Player string
	Score  float64
	Rank   *int
}

// ranked fills in every missing rank by score, highest first, ties sharing a
// rank and the next rank skipping past them (1, 1, 3). A rank the game gave
// is kept as it is: the game knows its own rules. The order is the one sent.
func ranked(in []ScoreIn) []model.Score {
	order := make([]int, len(in))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return in[order[a]].Score > in[order[b]].Score })

	computed := make([]int, len(in))
	for pos, i := range order {
		switch {
		case pos == 0:
			computed[i] = 1
		case in[i].Score == in[order[pos-1]].Score:
			computed[i] = computed[order[pos-1]]
		default:
			computed[i] = pos + 1
		}
	}

	out := make([]model.Score, len(in))
	for i, s := range in {
		rank := computed[i]
		if s.Rank != nil {
			rank = *s.Rank
		}
		out[i] = model.Score{Player: s.Player, Score: s.Score, Rank: rank}
	}
	return out
}
