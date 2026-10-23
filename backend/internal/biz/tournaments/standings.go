package tournaments

// The league's table, read from its plays (docs/TOURNAMENTS.md, "Deciding"):
// a point for every ranked entrant beaten in a round, then first places,
// then the draw's seed; whoever withdrew below everybody who stayed. The
// table is never kept: every read counts it again.

import (
	"sort"

	"github.com/jamaa/gathering/backend/internal/data/model"
)

// tableRow is one entrant's line of a league's table.
type tableRow struct {
	player    string
	seed      int
	points    int
	wins      int
	played    int
	withdrawn bool
}

// table is the league's table: every entrant, in the order of their places.
// Once it runs every row is an entrant who played or withdrew: the draw
// dropped those removed while it was open.
func (st *state) table(plays []model.TournamentPlay) []tableRow {
	rows := make([]tableRow, 0, len(st.entrants))
	at := make(map[string]int, len(st.entrants))
	for _, e := range st.entrants {
		at[e.PlayerID] = len(rows)
		rows = append(rows, tableRow{player: e.PlayerID, seed: e.Seed, withdrawn: !e.Current()})
	}
	for _, p := range plays {
		for _, line := range p.Results {
			i, ok := at[line.Player]
			if !ok {
				continue
			}
			rows[i].played++
			if line.Rank == 1 {
				rows[i].wins++
			}
			for _, other := range p.Results {
				if other.Rank > line.Rank {
					rows[i].points++
				}
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.withdrawn != b.withdrawn {
			return !a.withdrawn
		}
		if a.points != b.points {
			return a.points > b.points
		}
		if a.wins != b.wins {
			return a.wins > b.wins
		}
		return a.seed < b.seed
	})
	return rows
}
