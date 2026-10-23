package friends

import (
	"context"

	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Among is which of others are the player's friends, with no block between
// the two either way: who may come into the player's friends-only room, and
// who the player may invite. The rooms ask it; it holds no rule of theirs.
func (s *Service) Among(ctx context.Context, playerID string, others []string) (map[string]bool, error) {
	out := make(map[string]bool, len(others))
	if len(others) == 0 {
		return out, nil
	}
	found, err := among(ctx, s.db, playerID, others)
	if err != nil {
		return nil, errors.Internal(err)
	}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}
