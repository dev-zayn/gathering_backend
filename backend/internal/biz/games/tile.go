package games

import (
	"context"

	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
)

// Tile is as much of a game as a row in a list draws: where a friend is
// playing, what a notification is about, whose leaderboard this is.
type Tile struct {
	ID   string `json:"id" example:"fixture.xo"`
	Name string `json:"name" example:"إكس أو"`
	Icon string `json:"icon" doc:"A link that expires; empty when the game has no tile yet."`
}

// TileOf draws one, the icon signed on the way out. A game whose row is
// gone is its id alone.
func TileOf(ctx context.Context, store files.Files, id string, g *model.Game) Tile {
	if g == nil {
		return Tile{ID: id}
	}
	return Tile{ID: g.ID, Name: g.Name, Icon: IconURL(ctx, store, g)}
}
