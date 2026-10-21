package games

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Registry is the admin's side of the catalogue. Adding a game is registering
// a URL; everything else is read from the manifest the studio publishes.
type Registry struct {
	db      *gorm.DB
	fetcher Fetcher
	audit   audit.Log
	revoker *Revoker

	// Files holds the tile pictures. Set by the server; nil in tests that
	// never upload one.
	Files files.Files

	// MaxAge is how stale a manifest may get before the daily re-read picks it
	// up; Batch caps one round, so a backlog does not fetch everything at once.
	MaxAge time.Duration
	Batch  int
}

// Fetcher reads a manifest from wherever the game publishes it. An interface
// because the document is somebody else's, and may be gone or malformed.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*model.Manifest, []byte, error)
}

func NewRegistry(db *gorm.DB, fetcher Fetcher, audit audit.Log, revoker *Revoker) *Registry {
	return &Registry{db: db, fetcher: fetcher, audit: audit, revoker: revoker}
}

func (r *Registry) List(ctx context.Context, listing string) ([]model.Game, error) {
	if listing != "" && !enum.Listing(listing).Valid() {
		return nil, errors.Invalid(map[string]string{"listing": "unknown"})
	}
	games, err := allGames(ctx, r.db, listing)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return games, nil
}

func (r *Registry) Get(ctx context.Context, id string) (*model.Game, error) {
	g, err := anyGame(ctx, r.db, id)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil {
		return nil, errNotFound
	}
	return g, nil
}

// Register fetches the manifest and creates the game unpublished. It returns
// the client secret once; nothing stores it in the clear, so a lost one is
// rolled.
func (r *Registry) Register(ctx context.Context, actor, rawURL string) (*model.Game, string, error) {
	url, ok := model.ManifestURL(rawURL)
	if !ok {
		return nil, "", errors.Invalid(map[string]string{"url": "must be an http or https URL"})
	}

	g, err := r.read(ctx, url)
	if err != nil {
		return nil, "", err
	}

	existing, err := anyGame(ctx, r.db, g.ID)
	if err != nil {
		return nil, "", errors.Internal(err)
	}
	if existing != nil {
		// The manifest names the game, so the same URL twice and two URLs
		// claiming one id are the same mistake.
		return nil, "", errors.Conflict("already_registered")
	}

	// No store copy yet: the manifest carries no tagline or tile, and inventing
	// one on the game's behalf is how store copy stops meaning anything.
	g.Listing = enum.Unpublished
	g.Category = enum.CategoryOther

	secret := model.NewSecret()
	g.SecretHash = model.HashSecret(secret)

	if err := upsertGame(ctx, r.db, g); err != nil {
		return nil, "", errors.Internal(err)
	}
	r.record(ctx, actor, model.ActionRegistered, g.ID, map[string]any{
		"url": url, "version": g.Version,
	})

	saved, err := r.Get(ctx, g.ID)
	return saved, secret, err
}

// RollSecret issues a new client secret, once. Live tokens keep working: the
// secret proves who is asking for a new one, not who already has one.
func (r *Registry) RollSecret(ctx context.Context, actor, id string) (string, error) {
	if _, err := r.Get(ctx, id); err != nil {
		return "", err
	}

	secret := model.NewSecret()
	if err := setSecret(ctx, r.db, id, model.HashSecret(secret)); err != nil {
		return "", errors.Internal(err)
	}
	r.record(ctx, actor, model.ActionSecretRolled, id, nil)
	return secret, nil
}

// Refresh re-reads the manifest for somebody who asked. A published game
// asking for more is held rather than saved: nobody is asked at install, so
// the admin reading the new document is the whole of the approval.
func (r *Registry) Refresh(ctx context.Context, actor, id string) (*model.Game, error) {
	re, err := r.refetch(ctx, id, "")
	if err != nil {
		return nil, err
	}

	// Somebody pressed the button, so the log says so either way: "nothing
	// changed" is an answer.
	r.record(ctx, actor, model.ActionRefreshed, id, re.detail())
	return re.After, nil
}

// Accept saves a held manifest as the game's own. What it asks for reaches
// every player's next token from here.
func (r *Registry) Accept(ctx context.Context, actor, id string) (*model.Game, error) {
	current, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.HeldAt == nil {
		return nil, errors.Conflict("nothing_held")
	}

	var m model.Manifest
	if err := json.Unmarshal(current.Held, &m); err != nil {
		return nil, errors.Internal(err)
	}
	next, err := r.parse(ctx, &m, current.Held, current.HeldURL)
	if err != nil {
		return nil, err
	}
	if next.ID != current.ID {
		return nil, errors.Invalid(map[string]string{"id": "the held manifest names a different game"})
	}
	merge(ctx, current, next)

	if err := upsertGame(ctx, r.db, next); err != nil {
		return nil, errors.Internal(err)
	}
	r.record(ctx, actor, model.ActionAccepted, id, map[string]any{
		"from": current.Version, "to": next.Version,
		"scopes": next.Scopes, "permissions": next.Permissions, "room": next.Room,
	})
	return r.Get(ctx, id)
}

// Move points the game at a new manifest address, for a studio that has moved
// theirs. The document there must name this game: the address changes, the
// game does not, and nothing here can turn one game into another.
func (r *Registry) Move(ctx context.Context, actor, id, rawURL string) (*model.Game, error) {
	url, ok := model.ManifestURL(rawURL)
	if !ok {
		return nil, errors.Invalid(map[string]string{"url": "must be an http or https URL"})
	}

	re, err := r.refetch(ctx, id, url)
	if err != nil {
		return nil, err
	}
	if re.Before.ManifestURL == url {
		return re.After, nil
	}
	r.record(ctx, actor, model.ActionMoved, id, map[string]any{
		"from": re.Before.ManifestURL, "to": url, "version": re.After.Version,
	})
	return re.After, nil
}

// refetched is one re-read. ClaimedTier is set only when the document asked
// to be something other than what the registry says it is. Held names the
// scopes a published game newly asked for, and Changed the blocks it changed
// — permissions, room — in which case After is Before with the document
// parked beside it.
type refetched struct {
	Before      *model.Game
	After       *model.Game
	ClaimedTier enum.Tier
	Held        []string
	Changed     []string
}

// held says whether the document was parked rather than written.
func (re *refetched) held() bool { return len(re.Held) > 0 || len(re.Changed) > 0 }

// detail is the log line's payload: where the version went, and what was
// claimed or held on the way.
func (re *refetched) detail() map[string]any {
	d := map[string]any{"from": re.Before.Version, "to": re.After.Version}
	if re.ClaimedTier != "" {
		d["claimed_tier"] = re.ClaimedTier
	}
	if len(re.Held) > 0 {
		d["held"] = re.Held
	}
	if len(re.Changed) > 0 {
		d["changed"] = re.Changed
	}
	return d
}

// refetch reads the document and writes the row, recording nothing: whether a
// re-read is worth a log line depends on who asked. The address is the game's
// own unless a move gives another. A published game asking for more scopes,
// other permissions or another room is not written: the document is held
// for an admin instead.
func (r *Registry) refetch(ctx context.Context, id, url string) (*refetched, error) {
	current, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if url == "" {
		url = current.ManifestURL
	}
	if url == "" {
		return nil, errors.Conflict("no_manifest")
	}

	next, err := r.read(ctx, url)
	if err != nil {
		return nil, err
	}
	if next.ID != current.ID {
		return nil, errors.Invalid(map[string]string{"id": "the manifest now names a different game"})
	}

	claimed := merge(ctx, current, next)

	// Players hold what a published manifest asks for, so a document asking
	// for more — or asking the phone for something else, or shaping the room
	// differently — waits for an admin. A suspended game keeps its shelves
	// and is one publish from reaching them again, so it waits too. Only a
	// game never published has nobody to reach.
	re := &refetched{Before: current, ClaimedTier: claimed}
	if current.Listing != enum.Unpublished {
		re.Held = newScopes(current.Scopes, next.Scopes)
		if !sameSet(current.Permissions, next.Permissions) {
			re.Changed = append(re.Changed, "permissions")
		}
		if current.Room != next.Room {
			re.Changed = append(re.Changed, "room")
		}
	}
	if re.held() {
		if err := setHeld(ctx, r.db, id, next.Manifest, url, data.Now()); err != nil {
			return nil, errors.Internal(err)
		}
		saved, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		re.After = saved
		return re, nil
	}

	if err := upsertGame(ctx, r.db, next); err != nil {
		return nil, errors.Internal(err)
	}

	saved, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	re.After = saved
	return re, nil
}

// merge carries onto a freshly read document everything that is ours rather
// than the manifest's, and returns the tier it claimed if that differed.
func merge(ctx context.Context, current, next *model.Game) (claimed enum.Tier) {
	// The listing state and the store copy are ours, not the manifest's.
	next.Listing = current.Listing
	next.Category = current.Category
	next.Featured = current.Featured
	next.Tagline = current.Tagline
	next.Description = current.Description
	next.IconKey = current.IconKey
	next.Screenshots = current.Screenshots
	next.SecretHash = current.SecretHash

	// So is the tier — the one field that is a claim about us. Kept, handed
	// back, an admin decides.
	if next.Tier != current.Tier {
		claimed = next.Tier
		applog.From(ctx).Warn("manifest claims a different tier",
			"game", current.ID, "stored", current.Tier, "claimed", next.Tier)
	}
	next.Tier = current.Tier
	next.FirstParty = current.FirstParty
	return claimed
}

// newScopes is what after asks for that before did not.
func newScopes(before, after []string) []string {
	had := make(map[string]bool, len(before))
	for _, s := range before {
		had[s] = true
	}
	var out []string
	for _, s := range after {
		if !had[s] {
			out = append(out, s)
		}
	}
	return out
}

// Reread picks up studios who edit their manifest and never tell us. The
// api's clock decides when to ask; this decides what is due, and writes game
// rows only.
func (r *Registry) Reread(ctx context.Context) (*Reread, error) {
	maxAge, batch := r.MaxAge, r.Batch
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	if batch <= 0 {
		batch = 20
	}

	ids, err := staleGames(ctx, r.db, time.Now().Add(-maxAge), batch)
	if err != nil {
		return nil, errors.Internal(err)
	}

	out := &Reread{}
	for _, id := range ids {
		// Unrelated documents on unrelated hosts: one unreachable server is not
		// the round's problem, and the next round is the retry.
		re, err := r.refetch(ctx, id, "")
		if err != nil {
			out.Failed++
			applog.From(ctx).Warn("manifest not re-read", "game", id, "err", err)
			continue
		}

		out.Read++
		moved, versioned := changed(re.Before, re.After)

		// A claimed tier or a held document is always worth a line; otherwise a
		// daily job writing one line per game per day buries the days something
		// happened.
		if !moved && re.ClaimedTier == "" && !re.held() {
			continue
		}

		out.Changed++
		r.record(ctx, model.ActorWorker, model.ActionRefreshed, id, re.detail())

		// Scopes moving under a version that did not is a studio forgetting to
		// bump it, which is worth a line even though nothing here depends on it.
		if moved && !versioned {
			applog.From(ctx).Warn("manifest changed without a new version",
				"game", id, "version", re.After.Version,
				"from", re.Before.Scopes, "to", re.After.Scopes)
		}
	}
	return out, nil
}

// changed says whether a re-read is worth recording, and whether the version
// moved with it. The version, the scopes, the permissions and the room are
// compared; the rest of the document is the studio's own business.
func changed(before, after *model.Game) (moved, versioned bool) {
	versioned = before.Version != after.Version
	moved = versioned ||
		!sameSet(before.Scopes, after.Scopes) ||
		!sameSet(before.Permissions, after.Permissions) ||
		before.Room != after.Room
	return moved, versioned
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	in := make(map[string]bool, len(a))
	for _, s := range a {
		in[s] = true
	}
	for _, s := range b {
		if !in[s] {
			return false
		}
	}
	return true
}

// Reread is what one round did. Changed is the subset of Read that moved.
type Reread struct {
	Read    int64
	Changed int64
	Failed  int64
}

// Publish is the human gate: nothing reaches a player without it.
func (r *Registry) Publish(ctx context.Context, actor, id string) (*model.Game, error) {
	g, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// No tagline and no icon is a broken tile in every store.
	missing := []string{}
	if strings.TrimSpace(g.Tagline) == "" {
		missing = append(missing, "tagline")
	}
	if g.IconKey == "" {
		missing = append(missing, "icon")
	}
	if len(missing) > 0 {
		return nil, errors.Incomplete(missing)
	}

	if err := setState(ctx, r.db, id, enum.Published); err != nil {
		return nil, errors.Internal(err)
	}
	r.record(ctx, actor, model.ActionPublished, id, map[string]any{"version": g.Version})
	return r.Get(ctx, id)
}

// Suspend is the kill switch. The game leaves every store and shelf at once,
// and its live tokens stop working with it.
func (r *Registry) Suspend(ctx context.Context, actor, id, reason string) (*model.Game, error) {
	if _, err := r.Get(ctx, id); err != nil {
		return nil, err
	}
	if err := setState(ctx, r.db, id, enum.Suspended); err != nil {
		return nil, errors.Internal(err)
	}

	// The kill switch reaches live tokens too. No player named: every one.
	if r.revoker != nil {
		if err := r.revoker.Revoke(ctx, id, ""); err != nil {
			applog.From(ctx).Error("live tokens not revoked", "game", id, "err", err)
		}
	}

	r.record(ctx, actor, model.ActionSuspended, id, map[string]any{"reason": reason})
	return r.Get(ctx, id)
}

// Feature puts a game at the top of the store, or takes it back down. An
// admin's call, whatever the tier.
func (r *Registry) Feature(ctx context.Context, actor, id string, on bool) (*model.Game, error) {
	if _, err := r.Get(ctx, id); err != nil {
		return nil, err
	}
	if err := setFeatured(ctx, r.db, id, on); err != nil {
		return nil, errors.Internal(err)
	}
	r.record(ctx, actor, model.ActionFeatured, id, map[string]any{"featured": on})
	return r.Get(ctx, id)
}

// Listing edits the store copy: the words a game should not be writing about
// itself, and the switch that puts it first.
func (r *Registry) Listing(ctx context.Context, actor, id string, in model.Listing) (*model.Game, error) {
	bad := map[string]string{}
	if in.Category != nil && !enum.GameCategory(*in.Category).Valid() {
		bad["category"] = "unknown"
	}
	if in.Tagline != nil && model.TooLong(*in.Tagline, model.MaxTagline) {
		bad["tagline"] = "too_long"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	if _, err := r.Get(ctx, id); err != nil {
		return nil, err
	}

	if err := updateListing(ctx, r.db, id, in); err != nil {
		return nil, errors.Internal(err)
	}
	// Which fields were written, not their contents: storing the copy twice
	// would only mean two places to correct later.
	r.record(ctx, actor, model.ActionListed, id, map[string]any{"fields": edited(in)})
	return r.Get(ctx, id)
}

// edited names the parts of the store copy this call set. Every field is
// optional, so an empty list is a call that changed nothing.
func edited(in model.Listing) []string {
	fields := []string{}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"tagline", in.Tagline != nil},
		{"description", in.Description != nil},
		{"category", in.Category != nil},
		{"featured", in.Featured != nil},
	} {
		if f.set {
			fields = append(fields, f.name)
		}
	}
	return fields
}

// read fetches a manifest and turns it into a game, or into the whole list
// of what is wrong with it.
func (r *Registry) read(ctx context.Context, url string) (*model.Game, error) {
	m, raw, err := r.fetcher.Fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	return r.parse(ctx, m, raw, url)
}

// parse is the game a manifest describes, checked against the vocabulary.
func (r *Registry) parse(ctx context.Context, m *model.Manifest, raw []byte, url string) (*model.Game, error) {
	known, err := scopeKeys(ctx, r.db)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if bad := m.Check(known); len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	now := data.Now()
	publisher := m.Publisher.Name
	if publisher == "" {
		publisher = m.Publisher.Company
	}

	return &model.Game{
		ID:           m.ID,
		Name:         m.Name,
		Publisher:    publisher,
		Orientation:  m.Orientation,
		Splash:       m.Splash,
		Version:      m.Version,
		Entry:        m.Entry,
		Tier:         m.Publisher.Tier,
		FirstParty:   m.FirstParty(),
		Scopes:       m.Scopes,
		Permissions:  m.Permissions,
		Room:         m.Room,
		RedirectURIs: m.RedirectURIs,
		Manifest:     raw,
		ManifestURL:  url,
		FetchedAt:    &now,
	}, nil
}

// Reach is how far every game has got: suspending one is a different decision
// on three shelves than on three thousand.
func (r *Registry) Reach(ctx context.Context) ([]model.Reach, int, error) {
	reach, err := gameReach(ctx, r.db)
	if err != nil {
		return nil, 0, errors.Internal(err)
	}
	players, err := playersWithGames(ctx, r.db)
	if err != nil {
		return nil, 0, errors.Internal(err)
	}
	return reach, players, nil
}

func (r *Registry) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if r.audit == nil {
		return
	}
	if err := r.audit.Write(ctx, model.Entry{
		Actor: actor, Action: action, Subject: subject, Detail: detail,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "subject", subject, "err", err)
	}
}
