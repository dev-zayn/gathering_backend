package enum

// ScoreOrder is which end of a board is the best (docs/SCORES.md): the
// highest score (points) or the lowest (a time).
type ScoreOrder string

const (
	ScoreHigh ScoreOrder = "high"
	ScoreLow  ScoreOrder = "low"
)

var ScoreOrders = []ScoreOrder{ScoreHigh, ScoreLow}

func (o ScoreOrder) Valid() bool { return in(ScoreOrders, o) }

// Beats is whether score a is better than b on a board of this order.
func (o ScoreOrder) Beats(a, b float64) bool {
	if o == ScoreLow {
		return a < b
	}
	return a > b
}
