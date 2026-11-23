package games

import (
	"context"
	goerrors "errors"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Open is the tap. There is no consent screen: a published game is a
// player's to play, and the first time they open it the row is written so the
// tile stays on their shelf. Every open after that moves last_played_at and
// counts one more play. A tester's game under test is theirs to open the
// same way, published or not (docs/PUBLISHERS.md).
func (c *Catalogue) Open(ctx context.Context, playerID, gameID string) (*model.Library, error) {
	seen, err := SeenBy(ctx, c.db, playerID, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if seen == nil {
		return nil, errNotFound
	}
	return c.shelve(ctx, playerID, &seen.Game)
}

// OpenIn is the tap inside a room, which plays the version it was made with:
// buildID is the room's build, "" for the live game. A test room's game
// opened by somebody who no longer tests it is testers_only.
func (c *Catalogue) OpenIn(ctx context.Context, playerID, gameID, buildID string) (*model.Library, error) {
	seen, ok, err := SeenIn(ctx, c.db, playerID, gameID, buildID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if !ok {
		return nil, ErrTestersOnly
	}
	if seen == nil {
		return nil, errNotFound
	}
	return c.shelve(ctx, playerID, &seen.Game)
}

// ErrTestersOnly is a test room, or its game, asked for by a player who does
// not test the game (docs/PUBLISHERS.md, "Rooms").
var ErrTestersOnly = errors.Forbidden("testers_only")

// shelve is the first open's row, or every later open's play.
func (c *Catalogue) shelve(ctx context.Context, playerID string, g *model.Game) (*model.Library, error) {
	gameID := g.ID
	now := data.Now()
	existing, err := libraryRow(ctx, c.db, playerID, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if existing != nil {
		if err := touchLibrary(ctx, c.db, playerID, gameID, now); err != nil {
			return nil, errors.Internal(err)
		}
		existing.LastPlayedAt = &now
		existing.Plays++
		return existing, nil
	}

	in := model.Library{PlayerID: playerID, GameID: g.ID, AddedAt: now, LastPlayedAt: &now, Plays: 1}
	if err := addToLibrary(ctx, c.db, in); err != nil {
		if goerrors.Is(err, ErrAlreadyInLibrary) {
			// Two first opens arriving together: the other one won, which is fine.
			return libraryRow(ctx, c.db, playerID, gameID)
		}
		return nil, errors.Internal(err)
	}
	// The subject is the player: a game landed on their shelf, and theirs is
	// the page somebody asks what they have.
	c.record(ctx, playerID, model.ActionLibraryAdded, playerID, map[string]any{
		"game": g.ID, "version": g.Version,
	})
	return &in, nil
}

// Remove drops the tile and every live token. Nothing inside the game is
// deleted: that data is the studio's and we make no promise about it.
func (c *Catalogue) Remove(ctx context.Context, playerID, gameID string) error {
	removed, err := removeFromLibrary(ctx, c.db, playerID, gameID)
	if err != nil {
		return errors.Internal(err)
	}
	if !removed {
		return errors.NotFound("not_in_library")
	}

	// Removing is the revocation, and it has to reach the tokens now rather
	// than when they expire. Auth minted them, so auth ends them.
	if c.revoker != nil {
		if err := c.revoker.Revoke(ctx, gameID, playerID); err != nil {
			// The row is gone, so nothing renews. Only the access token outlives
			// this, and failing the remove over it would be worse.
			applog.From(ctx).Error("live tokens not revoked", "game", gameID, "player", playerID, "err", err)
		}
	}
	c.record(ctx, playerID, model.ActionLibraryRemoved, playerID, map[string]any{"game": gameID})
	return nil
}

// Grant is what must be true before a credential is minted: the game is
// real, the player's to see — published, or a build they test — and on this
// player's shelf. The scopes are the manifest's of the version they get, as
// it stands today.
func (c *Catalogue) Grant(ctx context.Context, playerID, gameID string) (*model.Grant, error) {
	g, err := anyGame(ctx, c.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil {
		return nil, errNotFound
	}

	// A suspended game is on nobody's shelf as far as anything downstream is
	// concerned: the kill switch has to reach live tokens, not just tiles.
	seen, err := SeenBy(ctx, c.db, playerID, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return c.granted(ctx, playerID, g, seen)
}

// GrantIn is Grant for a launch into a room: the version the room plays.
func (c *Catalogue) GrantIn(ctx context.Context, playerID, gameID, buildID string) (*model.Grant, error) {
	g, err := anyGame(ctx, c.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil {
		return nil, errNotFound
	}
	seen, ok, err := SeenIn(ctx, c.db, playerID, gameID, buildID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if !ok {
		return nil, ErrTestersOnly
	}
	return c.granted(ctx, playerID, g, seen)
}

// granted is the grant for the version seen, nil when the game is not the
// player's to see.
func (c *Catalogue) granted(ctx context.Context, playerID string, g *model.Game, seen *Seen) (*model.Grant, error) {
	out := &model.Grant{Game: *g}
	if seen == nil {
		return out, nil
	}
	out.Game = seen.Game
	g = &out.Game
	gameID := g.ID

	row, err := libraryRow(ctx, c.db, playerID, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if row == nil {
		return out, nil
	}

	scopes, err := scopesFor(ctx, c.db, g.Scopes)
	if err != nil {
		return nil, errors.Internal(err)
	}

	out.InLibrary = true
	out.Scopes = scopes
	return out, nil
}

// Authenticate checks the game's client secret. It is here rather than in
// identity because the game record is this package's, and there is one copy.
func (c *Catalogue) Authenticate(ctx context.Context, gameID, secret string) (*model.Game, error) {
	g, err := anyGame(ctx, c.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}

	// One answer for an unknown game and a wrong secret. Which of the two it
	// was is not something a caller guessing should be told.
	if g == nil || !model.SecretMatches(secret, g.SecretHash) {
		return nil, errors.Unauthorized("invalid_client")
	}
	return g, nil
}
