package rooms

import (
	"context"

	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// LinkCard is a room as its invite link's page shows it to anybody holding
// the link: the game and the code, nothing more. Who hosts it, who is in it
// and who may come in stay with the room; the join asks those.
type LinkCard struct {
	Code string
	Game Game
}

// LinkCard is the open room holding a code, for the invite link's page
// (docs/JOINING.md). The code is read as a typed code is read: case, spaces
// and dashes do not count. Nil when no open room holds it, whatever the
// reason: no such room, a room that has ended, or something that cannot be
// a code at all. The page says the same to all three.
func (s *Service) LinkCard(ctx context.Context, typed string) (*LinkCard, error) {
	code := normalizeCode(typed)
	if !s.validCode(code) {
		return nil, nil
	}
	r, err := openByCode(ctx, s.db, code)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if r == nil {
		return nil, nil
	}
	g, err := gameRow(ctx, s.db, r.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &LinkCard{Code: r.Code, Game: s.gameCard(ctx, g, r.GameID)}, nil
}
