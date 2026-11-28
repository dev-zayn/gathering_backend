package games

// The publisher's side of the registry (docs/PUBLISHERS.md): its own games,
// the builds it sends on their way, and publishing one once a reviewer
// approved it. Every call names the publisher, and a game that is not its own
// is not found.

import (
	"bytes"
	"context"
	stderrors "errors"
	"strings"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Studio is a publisher's hand in the registry. It reads manifests the way
// the registry does, through it, and writes to the same tables.
type Studio struct {
	reg *Registry
	db  *gorm.DB
	tx  *data.Tx

	// Alphabet is ROOM_CODE_ALPHABET, upper case: what an invite code is
	// drawn from.
	Alphabet string

	// Limiter counts a player's tries at a code. Nil counts nothing.
	Limiter Limiter
}

func NewStudio(reg *Registry, alphabet string) *Studio {
	return &Studio{reg: reg, db: reg.db, tx: data.NewTx(reg.db), Alphabet: strings.ToUpper(alphabet)}
}

// Account is who is asking: the publisher's id and the name its games show.
type Account struct {
	ID   string
	Name string
}

// StudioGame is one of a publisher's games as its console reads it.
type StudioGame struct {
	Game model.Game

	// Build is the open build, nil when there is none; Builds is every build,
	// newest first, read for the game's own page only.
	Build  *model.GameBuild
	Builds []model.GameBuild

	// Players is how many shelves hold it; Testers how many test it.
	Players int
	Testers int
}

// BuildCopy is the store copy a publisher writes on a build, and its notes.
// Every field is optional; only what is set is written.
type BuildCopy struct {
	Tagline     *string
	Description *string
	Category    *string
	Notes       *string
}

var (
	errBuildOpen      = errors.Conflict("build_open")
	errSuspended      = errors.Conflict("suspended")
	errInReview       = errors.Conflict("in_review")
	errNotOpen        = errors.Conflict("not_open")
	errNotSubmittable = errors.Conflict("not_submittable")
	errNotInReview    = errors.Conflict("not_in_review")
	errNotApproved    = errors.Conflict("not_approved")
)

// Games is every game the publisher has, the newest first, each with its open
// build.
func (s *Studio) Games(ctx context.Context, publisherID string) ([]StudioGame, error) {
	var games []model.Game
	err := s.db.WithContext(ctx).Where("publisher_id = ?", publisherID).
		Order("created_at DESC, id").Find(&games).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if len(games) == 0 {
		return []StudioGame{}, nil
	}
	ids := make([]string, 0, len(games))
	for _, g := range games {
		ids = append(ids, g.ID)
	}

	var open []model.GameBuild
	if err := s.db.WithContext(ctx).Where("game_id IN ? AND status IN ?", ids, enum.OpenBuilds).Find(&open).Error; err != nil {
		return nil, errors.Internal(err)
	}
	builds := make(map[string]model.GameBuild, len(open))
	for _, b := range open {
		builds[b.GameID] = b
	}
	players, testers, err := s.counts(ctx, ids)
	if err != nil {
		return nil, errors.Internal(err)
	}

	out := make([]StudioGame, 0, len(games))
	for _, g := range games {
		row := StudioGame{Game: g, Players: players[g.ID], Testers: testers[g.ID]}
		if b, ok := builds[g.ID]; ok {
			row.Build = &b
		}
		out = append(out, row)
	}
	return out, nil
}

// Game is one of the publisher's games in full: its open build and every
// build before it.
func (s *Studio) Game(ctx context.Context, publisherID, gameID string) (*StudioGame, error) {
	g, err := s.owned(ctx, publisherID, gameID)
	if err != nil {
		return nil, err
	}
	builds, err := GameBuilds(ctx, s.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	players, testers, err := s.counts(ctx, []string{gameID})
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := &StudioGame{Game: *g, Builds: builds, Players: players[gameID], Testers: testers[gameID]}
	for i := range builds {
		if builds[i].Status.Open() {
			b := builds[i]
			out.Build = &b
			break
		}
	}
	return out, nil
}

// Create registers a game for the publisher: the manifest read as the
// registry reads one, the game made unpublished with its first build in
// testing. The tier is partner whatever the document says, and the name on
// it is the account's. The client secret comes back once.
func (s *Studio) Create(ctx context.Context, who Account, rawURL string) (*StudioGame, string, error) {
	url, ok := model.ManifestURL(rawURL)
	if !ok {
		return nil, "", errors.Invalid(map[string]string{"url": "must be an http or https URL"})
	}
	g, err := s.reg.read(ctx, url)
	if err != nil {
		return nil, "", err
	}
	existing, err := anyGame(ctx, s.db, g.ID)
	if err != nil {
		return nil, "", errors.Internal(err)
	}
	if existing != nil {
		return nil, "", errors.Conflict("already_registered")
	}

	g.Listing = enum.Unpublished
	g.Category = enum.CategoryOther
	g.Tier = enum.TierPartner
	g.FirstParty = false
	g.Publisher = who.Name
	publisherID := who.ID
	g.PublisherID = &publisherID
	secret := model.NewSecret()
	g.SecretHash = model.HashSecret(secret)

	b := buildOf(g)
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := upsertGame(ctx, data.Conn(ctx, s.db), g); err != nil {
			return err
		}
		return data.Conn(ctx, s.db).Create(b).Error
	})
	if err != nil {
		if duplicateKey(err) {
			return nil, "", errors.Conflict("already_registered")
		}
		return nil, "", errors.Internal(err)
	}
	s.reg.record(ctx, who.ID, model.ActionRegistered, g.ID, map[string]any{
		"url": url, "version": g.Version, "publisher": who.ID,
	})
	s.reg.record(ctx, who.ID, model.ActionBuildCreated, g.ID, map[string]any{"build": b.ID, "version": b.Version})

	out, err := s.Game(ctx, who.ID, g.ID)
	return out, secret, err
}

// NewBuild starts the next version: a manifest read from url (the live one's
// address when empty), the store copy copied from the live game's.
func (s *Studio) NewBuild(ctx context.Context, who Account, gameID, rawURL string) (*model.GameBuild, error) {
	g, err := s.owned(ctx, who.ID, gameID)
	if err != nil {
		return nil, err
	}
	if g.Listing == enum.Suspended {
		return nil, errSuspended
	}
	open, err := OpenBuild(ctx, s.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if open != nil {
		return nil, errBuildOpen
	}

	url := g.ManifestURL
	if strings.TrimSpace(rawURL) != "" {
		var ok bool
		if url, ok = model.ManifestURL(rawURL); !ok {
			return nil, errors.Invalid(map[string]string{"url": "must be an http or https URL"})
		}
	}
	next, err := s.readFor(ctx, g, url)
	if err != nil {
		return nil, err
	}

	b := buildOf(next)
	b.Tagline, b.Description, b.Category = g.Tagline, g.Description, g.Category
	b.IconKey, b.Screenshots = g.IconKey, g.Screenshots
	if err := s.db.WithContext(ctx).Create(b).Error; err != nil {
		if duplicateKey(err) {
			return nil, errBuildOpen
		}
		return nil, errors.Internal(err)
	}
	s.reg.record(ctx, who.ID, model.ActionBuildCreated, gameID, map[string]any{"build": b.ID, "version": b.Version})
	return BuildByID(ctx, s.db, b.ID)
}

// Refresh reads the build's document again, from url when given: the build
// moves there. A document that changed takes an approved build back to
// testing.
func (s *Studio) Refresh(ctx context.Context, who Account, buildID, rawURL string) (*model.GameBuild, error) {
	b, g, err := s.ownedBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	if err := editable(b); err != nil {
		return nil, err
	}
	url := b.ManifestURL
	if strings.TrimSpace(rawURL) != "" {
		var ok bool
		if url, ok = model.ManifestURL(rawURL); !ok {
			return nil, errors.Invalid(map[string]string{"url": "must be an http or https URL"})
		}
	}
	next, err := s.readFor(ctx, g, url)
	if err != nil {
		return nil, err
	}

	changed := url != b.ManifestURL || !bytes.Equal(b.Manifest, next.Manifest)
	fields := manifestFields(next)
	if changed && b.Status == enum.BuildApproved {
		fields["status"] = enum.BuildTesting
	}
	if err := s.updateBuild(ctx, b.ID, fields); err != nil {
		return nil, err
	}
	s.reg.record(ctx, who.ID, model.ActionBuildRefreshed, g.ID, map[string]any{
		"build": b.ID, "from": b.Version, "to": next.Version, "changed": changed,
	})
	return BuildByID(ctx, s.db, b.ID)
}

// Edit writes the build's store copy and notes. A change to the copy takes
// an approved build back to testing; the notes are for testers and the
// reviewer, and do not.
func (s *Studio) Edit(ctx context.Context, who Account, buildID string, in BuildCopy) (*model.GameBuild, error) {
	bad := map[string]string{}
	if in.Tagline != nil && model.TooLong(strings.TrimSpace(*in.Tagline), model.MaxTagline) {
		bad["tagline"] = "too_long"
	}
	if in.Description != nil && model.TooLong(strings.TrimSpace(*in.Description), model.MaxDescription) {
		bad["description"] = "too_long"
	}
	if in.Category != nil {
		if err := checkCategory(ctx, s.db, *in.Category, bad); err != nil {
			return nil, errors.Internal(err)
		}
	}
	if in.Notes != nil && model.TooLong(strings.TrimSpace(*in.Notes), model.MaxBuildNotes) {
		bad["notes"] = "too_long"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	b, g, err := s.ownedBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	if err := editable(b); err != nil {
		return nil, err
	}

	set := map[string]any{}
	fields := []string{}
	copyChanged := false
	write := func(name string, value, was string, isCopy bool) {
		if value == was {
			return
		}
		set[name] = value
		fields = append(fields, name)
		copyChanged = copyChanged || isCopy
	}
	if in.Tagline != nil {
		write("tagline", strings.TrimSpace(*in.Tagline), b.Tagline, true)
	}
	if in.Description != nil {
		write("description", strings.TrimSpace(*in.Description), b.Description, true)
	}
	if in.Category != nil {
		write("category", *in.Category, string(b.Category), true)
	}
	if in.Notes != nil {
		write("notes", strings.TrimSpace(*in.Notes), b.Notes, false)
	}
	if len(set) == 0 {
		return b, nil
	}
	if copyChanged && b.Status == enum.BuildApproved {
		set["status"] = enum.BuildTesting
	}
	if err := s.updateBuild(ctx, b.ID, set); err != nil {
		return nil, err
	}
	s.reg.record(ctx, who.ID, model.ActionBuildEdited, g.ID, map[string]any{"build": b.ID, "fields": fields})
	return BuildByID(ctx, s.db, b.ID)
}

// Submit sends a build to review. A tile needs its words and its picture, as
// a publish always did; the build's own icon or the live game's.
func (s *Studio) Submit(ctx context.Context, who Account, buildID string) (*model.GameBuild, error) {
	b, g, err := s.ownedBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	switch {
	case !b.Status.Open():
		return nil, errNotOpen
	case b.Status == enum.BuildInReview, b.Status == enum.BuildApproved:
		return nil, errNotSubmittable
	}
	missing := []string{}
	if strings.TrimSpace(b.Tagline) == "" {
		missing = append(missing, "tagline")
	}
	if b.IconKey == "" && g.IconKey == "" {
		missing = append(missing, "icon")
	}
	if len(missing) > 0 {
		return nil, errors.Incomplete(missing)
	}

	now := data.Now()
	if err := s.updateBuild(ctx, b.ID, map[string]any{"status": enum.BuildInReview, "submitted_at": now}); err != nil {
		return nil, err
	}
	s.reg.record(ctx, who.ID, model.ActionBuildSubmitted, g.ID, map[string]any{"build": b.ID, "version": b.Version})
	return BuildByID(ctx, s.db, b.ID)
}

// Cancel takes a build back from review, to change it.
func (s *Studio) Cancel(ctx context.Context, who Account, buildID string) (*model.GameBuild, error) {
	b, g, err := s.ownedBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	switch {
	case !b.Status.Open():
		return nil, errNotOpen
	case b.Status != enum.BuildInReview:
		return nil, errNotInReview
	}
	if err := s.updateBuild(ctx, b.ID, map[string]any{"status": enum.BuildTesting}); err != nil {
		return nil, err
	}
	s.reg.record(ctx, who.ID, model.ActionBuildCancelled, g.ID, map[string]any{"build": b.ID, "version": b.Version})
	return BuildByID(ctx, s.db, b.ID)
}

// Publish makes an approved build the live game, in one transaction: its
// manifest the game's (a held one dropped), its store copy the game's, the
// account's name the game's publisher, the game published if it was not.
// The live game's own pictures the build no longer uses go after the commit.
func (s *Studio) Publish(ctx context.Context, who Account, buildID string) (*StudioGame, error) {
	b, g, err := s.ownedBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	switch {
	case !b.Status.Open():
		return nil, errNotOpen
	case b.Status != enum.BuildApproved:
		return nil, errNotApproved
	case g.Listing == enum.Suspended:
		return nil, errSuspended
	}

	icon := b.IconKey
	if icon == "" {
		icon = g.IconKey
	}
	shots := b.Screenshots
	if len(shots) == 0 {
		shots = g.Screenshots
	}
	now := data.Now()
	fields := manifestFieldsOf(b)
	fields["tagline"] = b.Tagline
	fields["description"] = b.Description
	fields["category"] = b.Category
	fields["icon_key"] = icon
	fields["screenshots"] = shots
	fields["publisher"] = who.Name
	fields["listing"] = enum.Published
	fields["held"] = nil
	fields["held_url"] = ""
	fields["held_at"] = nil
	fields["updated_at"] = now

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		// The build is claimed first, by its status: a second publish of the
		// same build, or a reviewer's late answer, finds it no longer approved.
		res := data.Conn(ctx, s.db).Model(&model.GameBuild{}).
			Where("id = ? AND status = ?", b.ID, enum.BuildApproved).
			Updates(map[string]any{"status": enum.BuildPublished, "published_at": now, "closed_at": now, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errNotApproved
		}
		return data.Conn(ctx, s.db).Model(&model.Game{}).Where("id = ?", g.ID).Updates(fields).Error
	})
	if err != nil {
		var e *errors.Error
		if stderrors.As(err, &e) {
			return nil, e
		}
		return nil, errors.Internal(err)
	}

	// Pictures of the live game the new one does not draw: nobody can see
	// them any more. One that will not go is an orphan in the bucket, not a
	// failed publish.
	keep := map[string]bool{icon: true}
	for _, k := range shots {
		keep[k] = true
	}
	gone := []string{}
	if g.IconKey != "" && !keep[g.IconKey] {
		gone = append(gone, g.IconKey)
	}
	for _, k := range g.Screenshots {
		if !keep[k] {
			gone = append(gone, k)
		}
	}
	s.deleteFiles(ctx, gone)

	s.reg.record(ctx, who.ID, model.ActionBuildPublished, g.ID, map[string]any{
		"build": b.ID, "from": g.Version, "to": b.Version,
	})
	if g.Listing != enum.Published {
		s.reg.record(ctx, who.ID, model.ActionPublished, g.ID, map[string]any{"version": b.Version})
	}
	return s.Game(ctx, who.ID, g.ID)
}

// Withdraw discards an open build: its testers get the live game again. The
// build's own pictures go with it.
func (s *Studio) Withdraw(ctx context.Context, who Account, buildID string) (*StudioGame, error) {
	b, g, err := s.ownedBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	if !b.Status.Open() {
		return nil, errNotOpen
	}
	now := data.Now()
	res := s.db.WithContext(ctx).Model(&model.GameBuild{}).
		Where("id = ? AND status IN ?", b.ID, enum.OpenBuilds).
		Updates(map[string]any{"status": enum.BuildWithdrawn, "closed_at": now, "updated_at": now})
	if res.Error != nil {
		return nil, errors.Internal(res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, errNotOpen
	}
	s.deleteFiles(ctx, ownFiles(b, g))
	s.reg.record(ctx, who.ID, model.ActionBuildWithdrawn, g.ID, map[string]any{"build": b.ID, "version": b.Version})
	return s.Game(ctx, who.ID, g.ID)
}

// RollSecret is the publisher's own game's new client secret, once.
func (s *Studio) RollSecret(ctx context.Context, who Account, gameID string) (string, error) {
	if _, err := s.owned(ctx, who.ID, gameID); err != nil {
		return "", err
	}
	return s.reg.RollSecret(ctx, who.ID, gameID)
}

// readFor reads a document for one of the publisher's games: it must name
// that game, and a published game's next version must be another version.
func (s *Studio) readFor(ctx context.Context, g *model.Game, url string) (*model.Game, error) {
	next, err := s.reg.read(ctx, url)
	if err != nil {
		return nil, err
	}
	if next.ID != g.ID {
		return nil, errors.Invalid(map[string]string{"id": "another_game"})
	}
	if g.Listing == enum.Published && next.Version == g.Version {
		return nil, errors.Invalid(map[string]string{"version": "same_as_live"})
	}
	return next, nil
}

// owned is the publisher's game, or not found: another studio's game is
// none of its business, and which of the two it was is not said.
func (s *Studio) owned(ctx context.Context, publisherID, gameID string) (*model.Game, error) {
	g, err := anyGame(ctx, s.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil || g.PublisherID == nil || *g.PublisherID != publisherID {
		return nil, errNotFound
	}
	return g, nil
}

// ownedBuild is a build of one of the publisher's games, with its game.
func (s *Studio) ownedBuild(ctx context.Context, publisherID, buildID string) (*model.GameBuild, *model.Game, error) {
	b, err := BuildByID(ctx, s.db, buildID)
	if err != nil {
		return nil, nil, errors.Internal(err)
	}
	if b == nil {
		return nil, nil, errNotFound
	}
	g, err := s.owned(ctx, publisherID, b.GameID)
	if err != nil {
		return nil, nil, err
	}
	return b, g, nil
}

func (s *Studio) updateBuild(ctx context.Context, id string, fields map[string]any) error {
	fields["updated_at"] = data.Now()
	if err := s.db.WithContext(ctx).Model(&model.GameBuild{}).Where("id = ?", id).Updates(fields).Error; err != nil {
		return errors.Internal(err)
	}
	return nil
}

// counts is how many shelves hold each game and how many test it.
func (s *Studio) counts(ctx context.Context, ids []string) (players, testers map[string]int, err error) {
	players, testers = map[string]int{}, map[string]int{}
	var rows []struct {
		GameID string
		N      int
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT game_id, count(*) AS n FROM library WHERE game_id IN ? GROUP BY game_id`, ids).
		Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		players[r.GameID] = r.N
	}
	rows = nil
	if err := s.db.WithContext(ctx).Raw(`SELECT game_id, count(*) AS n FROM game_testers WHERE game_id IN ? GROUP BY game_id`, ids).
		Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		testers[r.GameID] = r.N
	}
	return players, testers, nil
}

func (s *Studio) deleteFiles(ctx context.Context, keys []string) {
	if s.reg.Files == nil {
		return
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		if err := s.reg.Files.Delete(ctx, k); err != nil {
			applog.From(ctx).Warn("orphan file left behind", "key", k, "err", err)
		}
	}
}

// editable refuses a change to a build in review, or one already closed.
func editable(b *model.GameBuild) error {
	switch {
	case !b.Status.Open():
		return errNotOpen
	case b.Status == enum.BuildInReview:
		return errInReview
	}
	return nil
}

// buildOf is a new build in testing from a document read for game g.
func buildOf(g *model.Game) *model.GameBuild {
	now := data.Now()
	fetched := now
	if g.FetchedAt != nil {
		fetched = *g.FetchedAt
	}
	return &model.GameBuild{
		ID: ids.New(ids.Build), GameID: g.ID, Status: enum.BuildTesting,
		Name: g.Name, Version: g.Version, ManifestURL: g.ManifestURL, Entry: g.Entry,
		Manifest: g.Manifest, FetchedAt: fetched, Orientation: g.Orientation, Splash: g.Splash,
		Scopes: nonNilStrings(g.Scopes), Permissions: nonNilStrings(g.Permissions), Room: g.Room,
		RedirectURIs: nonNilStrings(g.RedirectURIs),
		Tagline:      g.Tagline, Description: g.Description, Category: g.Category,
		IconKey: g.IconKey, Screenshots: nonNilStrings(g.Screenshots),
		CreatedAt: now, UpdatedAt: now,
	}
}

// manifestFields is a re-read document's half of a build row.
func manifestFields(g *model.Game) map[string]any {
	fetched := data.Now()
	if g.FetchedAt != nil {
		fetched = *g.FetchedAt
	}
	return map[string]any{
		"name": g.Name, "version": g.Version, "manifest_url": g.ManifestURL, "entry": g.Entry,
		"manifest": g.Manifest, "fetched_at": fetched, "orientation": g.Orientation, "splash": g.Splash,
		"scopes": nonNilStrings(g.Scopes), "permissions": nonNilStrings(g.Permissions), "room": g.Room,
		"redirect_uris": nonNilStrings(g.RedirectURIs),
	}
}

// manifestFieldsOf is a build's manifest half as the game row's columns.
func manifestFieldsOf(b *model.GameBuild) map[string]any {
	return map[string]any{
		"name": b.Name, "version": b.Version, "manifest_url": b.ManifestURL, "entry": b.Entry,
		"manifest": b.Manifest, "fetched_at": b.FetchedAt, "orientation": b.Orientation, "splash": b.Splash,
		"scopes": nonNilStrings(b.Scopes), "permissions": nonNilStrings(b.Permissions), "room": b.Room,
		"redirect_uris": nonNilStrings(b.RedirectURIs),
	}
}

// GameBuilds is every build of a game, the newest first.
func GameBuilds(ctx context.Context, db *gorm.DB, gameID string) ([]model.GameBuild, error) {
	var out []model.GameBuild
	err := data.Conn(ctx, db).Where("game_id = ?", gameID).Order("created_at DESC, id").Find(&out).Error
	return out, err
}

// buildPrefix is where a build's own pictures live in the bucket.
func buildPrefix(gameID, buildID string) string {
	return "games/" + gameID + "/builds/" + buildID + "/"
}

// ownFiles are the build's own pictures that the live game does not draw.
func ownFiles(b *model.GameBuild, g *model.Game) []string {
	prefix := buildPrefix(b.GameID, b.ID)
	live := map[string]bool{g.IconKey: true}
	for _, k := range g.Screenshots {
		live[k] = true
	}
	out := []string{}
	for _, k := range append([]string{b.IconKey}, b.Screenshots...) {
		if strings.HasPrefix(k, prefix) && !live[k] {
			out = append(out, k)
		}
	}
	return out
}

func nonNilStrings(s model.Strings) model.Strings {
	if s == nil {
		return model.Strings{}
	}
	return s
}

func duplicateKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}
