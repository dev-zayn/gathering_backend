package games

// The catalogue: the registry as one player sees it, what they hold, and what
// a game was granted.

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// ErrAlreadyInLibrary is the primary key on (player, game), named so the rule
// does not have to read a driver's message to know what happened.
var ErrAlreadyInLibrary = stderrors.New("already in the library")

// Catalogue is the player's side of the registry: the store, the shelf, and
// what opening a game means.
type Catalogue struct {
	db      *gorm.DB
	revoker *Revoker
	audit   audit.Log
}

func NewCatalogue(db *gorm.DB, revoker *Revoker, audit audit.Log) *Catalogue {
	return &Catalogue{db: db, revoker: revoker, audit: audit}
}

// Listed is one row of the registry seen by one player. The store and the
// shelf are the same rows, differing by whether the player has each.
type Listed struct {
	Game      model.Game
	InLibrary bool
}

// Detail is the game's page in the store, and whether this player has it.
type Detail struct {
	Game model.Game

	// Library is nil when the player has not opened the game yet.
	Library *model.Library
}

// List is the store: every published game, the featured ones first, then
// ours, then the rest by id, each marked with whether the player has it.
// A category narrows it.
func (c *Catalogue) List(ctx context.Context, playerID, category string) ([]Listed, error) {
	if category != "" && !enum.GameCategory(category).Valid() {
		return nil, errors.Invalid(map[string]string{"category": "unknown"})
	}

	held, err := c.heldSet(ctx, playerID)
	if err != nil {
		return nil, err
	}
	games, err := publishedGames(ctx, c.db, category)
	if err != nil {
		return nil, errors.Internal(err)
	}

	out := make([]Listed, 0, len(games))
	for _, g := range games {
		_, has := held[g.ID]
		out = append(out, Listed{Game: g, InLibrary: has})
	}
	return out, nil
}

func (c *Catalogue) Detail(ctx context.Context, playerID, gameID string) (*Detail, error) {
	g, err := publishedGame(ctx, c.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil {
		return nil, errNotFound
	}

	row, err := libraryRow(ctx, c.db, playerID, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &Detail{Game: *g, Library: row}, nil
}

func (c *Catalogue) heldSet(ctx context.Context, playerID string) (map[string]model.Library, error) {
	rows, err := libraryOf(ctx, c.db, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	set := make(map[string]model.Library, len(rows))
	for _, r := range rows {
		set[r.GameID] = r
	}
	return set, nil
}

// errNotFound is the answer for a game that is suspended, unpublished or
// simply not there. Which of the three it is is not a player's business.
var errNotFound = errors.NotFound("not_found")

// record never fails the thing it describes. A line that did not write is bad;
// refusing to open a game because of it is worse.
func (c *Catalogue) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if c.audit == nil {
		return
	}
	if err := c.audit.Write(ctx, model.Entry{
		Actor: actor, Action: action, Subject: subject, Detail: detail,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "subject", subject, "err", err)
	}
}

// The queries, as functions rather than methods: a player's view and an
// admin's are the same rows with and without the "published only" rule.
func publishedGames(ctx context.Context, db *gorm.DB, category string) ([]model.Game, error) {
	q := db.WithContext(ctx).Where("listing = ?", enum.Published)
	if category != "" {
		q = q.Where("category = ?", category)
	}
	return findGames(q.Order("featured DESC, first_party DESC, id"))
}

func publishedGame(ctx context.Context, db *gorm.DB, id string) (*model.Game, error) {
	var row model.Game
	err := db.WithContext(ctx).
		Where("id = ? AND listing = ?", id, enum.Published).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g := row
	return &g, nil
}

// scopesFor keeps the order the manifest asked in, so a token's scope string
// reads the way the game declared it rather than alphabetically.
func scopesFor(ctx context.Context, db *gorm.DB, keys []string) ([]model.Scope, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	var rows []model.Scope
	if err := db.WithContext(ctx).Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return nil, err
	}

	byKey := make(map[string]model.Scope, len(rows))
	for _, r := range rows {
		byKey[r.Key] = r
	}

	out := make([]model.Scope, 0, len(keys))
	for _, k := range keys {
		r, ok := byKey[k]
		if !ok {
			// A manifest naming a scope outside the vocabulary is a registry
			// mistake, not a player's problem: leave it out of the token.
			continue
		}
		out = append(out, model.Scope{Key: r.Key, Elevated: r.Elevated})
	}
	return out, nil
}

// scopeKeys is the whole vocabulary, for checking a manifest against it.
func scopeKeys(ctx context.Context, db *gorm.DB) (map[string]bool, error) {
	var keys []string
	if err := db.WithContext(ctx).Model(&model.Scope{}).Pluck("key", &keys).Error; err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

func findGames(q *gorm.DB) ([]model.Game, error) {
	var out []model.Game
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func libraryOf(ctx context.Context, db *gorm.DB, playerID string) ([]model.Library, error) {
	var out []model.Library
	err := db.WithContext(ctx).Where("player_id = ?", playerID).
		Order("game_id").Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

func libraryRow(ctx context.Context, db *gorm.DB, playerID, gameID string) (*model.Library, error) {
	var row model.Library
	err := db.WithContext(ctx).
		Where("player_id = ? AND game_id = ?", playerID, gameID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	in := row
	return &in, nil
}

func addToLibrary(ctx context.Context, db *gorm.DB, in model.Library) error {
	err := db.WithContext(ctx).Create(&in).Error

	// The primary key is what actually decides, not the read before it: two
	// first opens arriving together both pass the check and only one gets in.
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
		return ErrAlreadyInLibrary
	}
	return err
}

// touchLibrary is every open after the first: when, and one more play.
func touchLibrary(ctx context.Context, db *gorm.DB, playerID, gameID string, at time.Time) error {
	return db.WithContext(ctx).Model(&model.Library{}).
		Where("player_id = ? AND game_id = ?", playerID, gameID).
		Updates(map[string]any{"last_played_at": at, "plays": gorm.Expr("plays + 1")}).Error
}

func removeFromLibrary(ctx context.Context, db *gorm.DB, playerID, gameID string) (bool, error) {
	res := db.WithContext(ctx).
		Where("player_id = ? AND game_id = ?", playerID, gameID).
		Delete(&model.Library{})
	return res.RowsAffected > 0, res.Error
}

// gameReach counts every game at once: how many shelves it is on, and when
// the last player added it.
func gameReach(ctx context.Context, db *gorm.DB) ([]model.Reach, error) {
	var rows []struct {
		GameID  string
		Players int
		LastAt  *time.Time
	}
	err := db.WithContext(ctx).
		Model(&model.Library{}).
		Select(`game_id, count(*) AS players, max(added_at) AS last_at`).
		Group("game_id").
		Order("players DESC, game_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]model.Reach, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Reach{GameID: r.GameID, Players: r.Players, LastAt: r.LastAt})
	}
	return out, nil
}

func playersWithGames(ctx context.Context, db *gorm.DB) (int, error) {
	var n int64
	err := db.WithContext(ctx).
		Model(&model.Library{}).
		Distinct("player_id").
		Count(&n).Error
	return int(n), err
}

func allGames(ctx context.Context, db *gorm.DB, listing string) ([]model.Game, error) {
	q := db.WithContext(ctx)
	if listing != "" {
		q = q.Where("listing = ?", listing)
	}

	var out []model.Game
	if err := q.Order("id").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func anyGame(ctx context.Context, db *gorm.DB, id string) (*model.Game, error) {
	var row model.Game
	err := db.WithContext(ctx).Take(&row, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g := row
	return &g, nil
}

// upsertGame writes the manifest's half of the row. The store copy is listed
// in DoUpdates by its absence: a re-fetch must not blank a tagline somebody
// wrote, nor switch off a feature somebody switched on.
func upsertGame(ctx context.Context, db *gorm.DB, g *model.Game) error {
	now := data.Now()

	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "publisher", "version", "entry", "tier", "first_party",
			"orientation", "splash", "scopes", "permissions", "room",
			"manifest", "manifest_url", "redirect_uris", "fetched_at", "updated_at",
			"held", "held_url", "held_at",
		}),
	}).Create(&model.Game{
		ID:           g.ID,
		Name:         g.Name,
		Tagline:      g.Tagline,
		Description:  g.Description,
		Publisher:    g.Publisher,
		IconKey:      g.IconKey,
		Screenshots:  g.Screenshots,
		Category:     g.Category,
		Featured:     g.Featured,
		Orientation:  g.Orientation,
		Splash:       g.Splash,
		Version:      g.Version,
		Listing:      g.Listing,
		FirstParty:   g.FirstParty,
		Tier:         g.Tier,
		ManifestURL:  g.ManifestURL,
		Entry:        g.Entry,
		Manifest:     g.Manifest,
		FetchedAt:    &now,
		Held:         g.Held,
		HeldURL:      g.HeldURL,
		HeldAt:       g.HeldAt,
		Scopes:       g.Scopes,
		Permissions:  g.Permissions,
		Room:         g.Room,
		SecretHash:   g.SecretHash,
		RedirectURIs: g.RedirectURIs,
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error
}

func updateListing(ctx context.Context, db *gorm.DB, id string, in model.Listing) error {
	fields := map[string]any{}
	if in.Tagline != nil {
		fields["tagline"] = strings.TrimSpace(*in.Tagline)
	}
	if in.Description != nil {
		fields["description"] = strings.TrimSpace(*in.Description)
	}
	if in.Category != nil {
		fields["category"] = *in.Category
	}
	if in.Featured != nil {
		fields["featured"] = *in.Featured
	}
	if len(fields) == 0 {
		return nil
	}

	fields["updated_at"] = data.Now()
	return updateGame(ctx, db, id, fields)
}

// setHeld parks a re-read beside the game. fetched_at moves too, or the daily
// round would read and park the same document every day.
func setHeld(ctx context.Context, db *gorm.DB, id string, raw model.Raw, url string, at time.Time) error {
	return db.WithContext(ctx).Model(&model.Game{}).Where("id = ?", id).
		Updates(map[string]any{"held": raw, "held_url": url, "held_at": at, "fetched_at": at, "updated_at": at}).Error
}

// setSecret is its own write because the secret is never part of a manifest
// and must survive every re-fetch of one.
func setSecret(ctx context.Context, db *gorm.DB, id, hash string) error {
	return updateGame(ctx, db, id, map[string]any{"secret_hash": hash, "updated_at": data.Now()})
}

func setState(ctx context.Context, db *gorm.DB, id string, listing enum.Listing) error {
	return updateGame(ctx, db, id, map[string]any{"listing": listing, "updated_at": data.Now()})
}

func setFeatured(ctx context.Context, db *gorm.DB, id string, on bool) error {
	return updateGame(ctx, db, id, map[string]any{"featured": on, "updated_at": data.Now()})
}

// staleGames orders nulls first, so one whose first read failed comes before
// games merely a day old. No manifest URL means nothing to re-read.
func staleGames(ctx context.Context, db *gorm.DB, before time.Time, limit int) ([]string, error) {
	var ids []string
	err := db.WithContext(ctx).
		Model(&model.Game{}).
		Where("manifest_url <> ''").
		Where("fetched_at IS NULL OR fetched_at < ?", before).
		Order("fetched_at ASC NULLS FIRST").
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// updateGame is the one write every listing change goes through. A missing
// row is an error: an admin publishing a deleted game should hear about it.
func updateGame(ctx context.Context, db *gorm.DB, id string, fields map[string]any) error {
	res := db.WithContext(ctx).Model(&model.Game{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func setIconKey(ctx context.Context, db *gorm.DB, id, key string) error {
	return db.WithContext(ctx).Model(&model.Game{}).Where("id = ?", id).
		Updates(map[string]any{"icon_key": key, "updated_at": data.Now()}).Error
}

func setScreenshots(ctx context.Context, db *gorm.DB, id string, keys model.Strings) error {
	return db.WithContext(ctx).Model(&model.Game{}).Where("id = ?", id).
		Updates(map[string]any{"screenshots": keys, "updated_at": data.Now()}).Error
}
