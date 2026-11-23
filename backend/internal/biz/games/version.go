package games

// Which version of a game a player gets (docs/PUBLISHERS.md, "What a tester
// gets"), decided here and only here: the store, the store page, the launch
// and a new room all ask the same question and get the same answer.

import (
	"context"
	stderrors "errors"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// Seen is a game as one player gets it: the live row, or for a tester the
// open build laid over it.
type Seen struct {
	Game model.Game

	// Build is the build under test the player gets; nil for the live game.
	Build *model.GameBuild
}

// Testing says what the player gets is a build.
func (s *Seen) Testing() bool { return s != nil && s.Build != nil }

// BuildID is the build's id, or "" for the live game.
func (s *Seen) BuildID() string {
	if s == nil || s.Build == nil {
		return ""
	}
	return s.Build.ID
}

// SeenBy is the version of gameID that playerID gets outside a room: a
// tester of a game with an open build gets the build, anybody else the live
// game when it is published, and nil means the game is not theirs to see — a
// suspended game is nobody's, testers included. It reads through the
// transaction ctx carries, so a room's rules see what they are deciding on.
func SeenBy(ctx context.Context, db *gorm.DB, playerID, gameID string) (*Seen, error) {
	g, err := gameIn(ctx, db, gameID)
	if err != nil || g == nil || g.Listing == enum.Suspended {
		return nil, err
	}
	if g.PublisherID != nil && playerID != "" {
		b, err := testedBuild(ctx, db, playerID, gameID)
		if err != nil {
			return nil, err
		}
		if b != nil {
			return &Seen{Game: b.Over(*g), Build: b}, nil
		}
	}
	if g.Listing == enum.Published {
		return &Seen{Game: *g}, nil
	}
	return nil, nil
}

// SeenIn is the version a room plays: the build it was made with, for a
// player who still tests the game, or the live game. ok false is a test
// room's game asked for by somebody who no longer tests it; a nil Seen with
// ok true is a game nobody may open (gone, suspended, or a live room's game
// unpublished since).
func SeenIn(ctx context.Context, db *gorm.DB, playerID, gameID, buildID string) (seen *Seen, ok bool, err error) {
	g, err := gameIn(ctx, db, gameID)
	if err != nil || g == nil || g.Listing == enum.Suspended {
		return nil, true, err
	}
	if buildID == "" {
		if g.Listing != enum.Published {
			return nil, true, nil
		}
		return &Seen{Game: *g}, true, nil
	}

	tester, err := IsTester(ctx, db, gameID, playerID)
	if err != nil {
		return nil, false, err
	}
	if !tester {
		return nil, false, nil
	}
	b, err := BuildByID(ctx, db, buildID)
	if err != nil {
		return nil, false, err
	}
	if b == nil || b.GameID != gameID {
		return nil, true, nil
	}
	return &Seen{Game: b.Over(*g), Build: b}, true, nil
}

// IsTester says whether playerID tests gameID.
func IsTester(ctx context.Context, db *gorm.DB, gameID, playerID string) (bool, error) {
	var n int64
	err := data.Conn(ctx, db).Model(&model.Tester{}).
		Where("game_id = ? AND player_id = ?", gameID, playerID).Count(&n).Error
	return n > 0, err
}

// Testers is which of players test gameID, as a set.
func Testers(ctx context.Context, db *gorm.DB, gameID string, players []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(players) == 0 {
		return out, nil
	}
	var ids []string
	err := data.Conn(ctx, db).Model(&model.Tester{}).
		Where("game_id = ? AND player_id IN ?", gameID, players).Pluck("player_id", &ids).Error
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// BuildByID is one build, open or not, or nil.
func BuildByID(ctx context.Context, db *gorm.DB, id string) (*model.GameBuild, error) {
	var row model.GameBuild
	err := data.Conn(ctx, db).Take(&row, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// OpenBuild is the game's open build, or nil.
func OpenBuild(ctx context.Context, db *gorm.DB, gameID string) (*model.GameBuild, error) {
	var rows []model.GameBuild
	err := data.Conn(ctx, db).Where("game_id = ? AND status IN ?", gameID, enum.OpenBuilds).
		Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// testedBuild is the game's open build when playerID tests the game.
func testedBuild(ctx context.Context, db *gorm.DB, playerID, gameID string) (*model.GameBuild, error) {
	var rows []model.GameBuild
	err := data.Conn(ctx, db).
		Where("game_id = ? AND status IN ?", gameID, enum.OpenBuilds).
		Where("EXISTS (SELECT 1 FROM game_testers t WHERE t.game_id = game_builds.game_id AND t.player_id = ?)", playerID).
		Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// testedBuilds is every open build playerID tests, by game: the store's one
// query for all of them.
func testedBuilds(ctx context.Context, db *gorm.DB, playerID string) (map[string]model.GameBuild, error) {
	var rows []model.GameBuild
	err := data.Conn(ctx, db).
		Where("status IN ?", enum.OpenBuilds).
		Where("game_id IN (SELECT game_id FROM game_testers WHERE player_id = ?)", playerID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]model.GameBuild, len(rows))
	for _, r := range rows {
		out[r.GameID] = r
	}
	return out, nil
}

// gameIn is anyGame through the transaction ctx carries.
func gameIn(ctx context.Context, db *gorm.DB, id string) (*model.Game, error) {
	var row model.Game
	err := data.Conn(ctx, db).Take(&row, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
