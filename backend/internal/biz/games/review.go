package games

// The reviewer's side (docs/PUBLISHERS.md, "Review"): the queue of builds
// sent to review, one build in full beside the live game, and the two
// answers.

import (
	"context"
	"slices"
	"strings"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Review is the admin's hand in the builds publishers send.
type Review struct{ s *Studio }

func NewReview(s *Studio) *Review { return &Review{s: s} }

// ReviewRow is one build with its game and its publisher.
type ReviewRow struct {
	Build     model.GameBuild
	Game      model.Game
	Publisher *model.Publisher
}

// ReviewBuild is one build in full for the reviewer: its game, its publisher
// and the game's live codes, to test it with.
type ReviewBuild struct {
	ReviewRow
	Invites []model.TesterInvite
}

// reviewPage is the most the queue answers at once.
const reviewPage = 100

// Queue is the builds in one review status: the oldest submission first for
// those waiting, the newest decision first for the rest. waiting is how many
// are in review, whatever was asked.
func (r *Review) Queue(ctx context.Context, status string) ([]ReviewRow, int, error) {
	q := data.Conn(ctx, r.s.db).Model(&model.GameBuild{})
	switch status {
	case "", string(enum.BuildInReview):
		q = q.Where("status = ?", enum.BuildInReview).Order("submitted_at, id")
	case string(enum.BuildApproved), string(enum.BuildRejected):
		q = q.Where("status = ?", status).Order("reviewed_at DESC, id")
	case "all":
		q = q.Where("submitted_at IS NOT NULL").Order("COALESCE(reviewed_at, submitted_at) DESC, id")
	default:
		return nil, 0, errors.Invalid(map[string]string{"status": "unknown"})
	}
	var builds []model.GameBuild
	if err := q.Limit(reviewPage).Find(&builds).Error; err != nil {
		return nil, 0, errors.Internal(err)
	}
	var waiting int64
	if err := data.Conn(ctx, r.s.db).Model(&model.GameBuild{}).Where("status = ?", enum.BuildInReview).
		Count(&waiting).Error; err != nil {
		return nil, 0, errors.Internal(err)
	}
	rows, err := r.rows(ctx, builds)
	if err != nil {
		return nil, 0, err
	}
	return rows, int(waiting), nil
}

// Build is one build in full.
func (r *Review) Build(ctx context.Context, buildID string) (*ReviewBuild, error) {
	b, err := BuildByID(ctx, r.s.db, buildID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if b == nil {
		return nil, errNotFound
	}
	rows, err := r.rows(ctx, []model.GameBuild{*b})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	invites, err := Invites(ctx, r.s.db, b.GameID, true)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &ReviewBuild{ReviewRow: rows[0], Invites: invites}, nil
}

// Approve is the reviewer's yes: the publisher publishes it when ready.
func (r *Review) Approve(ctx context.Context, adminID, buildID, note string) (*ReviewBuild, error) {
	return r.decide(ctx, adminID, buildID, note, enum.BuildApproved)
}

// Reject sends a build back with the reviewer's words, which are required:
// a studio told no without a reason has nothing to fix.
func (r *Review) Reject(ctx context.Context, adminID, buildID, note string) (*ReviewBuild, error) {
	if strings.TrimSpace(note) == "" {
		return nil, errors.Invalid(map[string]string{"note": "required"})
	}
	return r.decide(ctx, adminID, buildID, note, enum.BuildRejected)
}

func (r *Review) decide(ctx context.Context, adminID, buildID, note string, to enum.BuildStatus) (*ReviewBuild, error) {
	note = strings.TrimSpace(note)
	if model.TooLong(note, model.MaxReviewNote) {
		return nil, errors.Invalid(map[string]string{"note": "too_long"})
	}
	b, err := BuildByID(ctx, r.s.db, buildID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if b == nil {
		return nil, errNotFound
	}
	now := data.Now()
	// Decided by the status in the update: a publisher who took it back in
	// the same second, or another reviewer's answer, wins or loses whole.
	res := data.Conn(ctx, r.s.db).Model(&model.GameBuild{}).
		Where("id = ? AND status = ?", b.ID, enum.BuildInReview).
		Updates(map[string]any{
			"status": to, "review_note": note, "reviewed_by": adminID, "reviewed_at": now, "updated_at": now,
		})
	if res.Error != nil {
		return nil, errors.Internal(res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, errNotInReview
	}
	action := model.ActionBuildApproved
	if to == enum.BuildRejected {
		action = model.ActionBuildRejected
	}
	r.s.reg.record(ctx, adminID, action, b.GameID, map[string]any{"build": b.ID, "version": b.Version})
	return r.Build(ctx, buildID)
}

// Invite is a code for the reviewer to play the build with: one use, a week,
// labelled so the publisher sees whose it was.
func (r *Review) Invite(ctx context.Context, adminID, buildID string) (*model.TesterInvite, error) {
	b, err := BuildByID(ctx, r.s.db, buildID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if b == nil {
		return nil, errNotFound
	}
	return r.s.newInvite(ctx, adminID, b.GameID, "Review", 1, 7)
}

// GameBuilds is every build of one game, for its page in the console.
func (r *Review) GameBuilds(ctx context.Context, gameID string) ([]model.GameBuild, error) {
	g, err := anyGame(ctx, r.s.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil {
		return nil, errNotFound
	}
	out, err := GameBuilds(ctx, r.s.db, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return out, nil
}

// Owner is who a game belongs to and its open build, for the registry's
// pages.
type Owner struct {
	Publisher *model.Publisher
	Build     *model.GameBuild
}

// Owners is every game's publisher and open build, by game, in two queries.
func (r *Review) Owners(ctx context.Context) (map[string]Owner, error) {
	var games []struct {
		ID          string
		PublisherID string
	}
	if err := data.Conn(ctx, r.s.db).Raw(`SELECT id, publisher_id FROM games WHERE publisher_id IS NOT NULL`).
		Scan(&games).Error; err != nil {
		return nil, errors.Internal(err)
	}
	out := map[string]Owner{}
	if len(games) == 0 {
		return out, nil
	}
	pubIDs := []string{}
	for _, g := range games {
		if !slices.Contains(pubIDs, g.PublisherID) {
			pubIDs = append(pubIDs, g.PublisherID)
		}
	}
	pubs, err := r.publishers(ctx, pubIDs)
	if err != nil {
		return nil, errors.Internal(err)
	}
	var open []model.GameBuild
	if err := data.Conn(ctx, r.s.db).Where("status IN ?", enum.OpenBuilds).Find(&open).Error; err != nil {
		return nil, errors.Internal(err)
	}
	builds := map[string]model.GameBuild{}
	for _, b := range open {
		builds[b.GameID] = b
	}
	for _, g := range games {
		o := Owner{}
		if p, ok := pubs[g.PublisherID]; ok {
			o.Publisher = &p
		}
		if b, ok := builds[g.ID]; ok {
			o.Build = &b
		}
		out[g.ID] = o
	}
	return out, nil
}

// PublisherGames is one publisher's games, for its page in the console, each
// with its open build.
func (r *Review) PublisherGames(ctx context.Context, publisherID string) ([]StudioGame, error) {
	return r.s.Games(ctx, publisherID)
}

func (r *Review) rows(ctx context.Context, builds []model.GameBuild) ([]ReviewRow, error) {
	if len(builds) == 0 {
		return []ReviewRow{}, nil
	}
	gameIDs := []string{}
	for _, b := range builds {
		if !slices.Contains(gameIDs, b.GameID) {
			gameIDs = append(gameIDs, b.GameID)
		}
	}
	var games []model.Game
	if err := data.Conn(ctx, r.s.db).Where("id IN ?", gameIDs).Find(&games).Error; err != nil {
		return nil, errors.Internal(err)
	}
	byID := map[string]model.Game{}
	pubIDs := []string{}
	for _, g := range games {
		byID[g.ID] = g
		if g.PublisherID != nil && !slices.Contains(pubIDs, *g.PublisherID) {
			pubIDs = append(pubIDs, *g.PublisherID)
		}
	}
	pubs, err := r.publishers(ctx, pubIDs)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := make([]ReviewRow, 0, len(builds))
	for _, b := range builds {
		g, ok := byID[b.GameID]
		if !ok {
			continue
		}
		row := ReviewRow{Build: b, Game: g}
		if g.PublisherID != nil {
			if p, ok := pubs[*g.PublisherID]; ok {
				row.Publisher = &p
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (r *Review) publishers(ctx context.Context, ids []string) (map[string]model.Publisher, error) {
	out := map[string]model.Publisher{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.Publisher
	if err := data.Conn(ctx, r.s.db).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, p := range rows {
		out[p.ID] = p
	}
	return out, nil
}

// Changes is what a build would change about the live game, by name, and the
// scopes the live game did not ask for. A game never published has nothing
// to compare with: both are empty.
func Changes(b *model.GameBuild, g *model.Game) (changes, newScopes []string) {
	changes, newScopes = []string{}, []string{}
	if g.Listing == enum.Unpublished {
		return changes, newScopes
	}
	add := func(name string, differs bool) {
		if differs {
			changes = append(changes, name)
		}
	}
	add("name", b.Name != g.Name)
	add("entry", b.Entry != g.Entry)
	add("scopes", !sameSet(b.Scopes, g.Scopes))
	add("permissions", !sameSet(b.Permissions, g.Permissions))
	add("room", b.Room != g.Room)
	add("orientation", b.Orientation != g.Orientation)
	add("splash", b.Splash != g.Splash)
	add("tagline", b.Tagline != g.Tagline)
	add("description", b.Description != g.Description)
	add("category", b.Category != g.Category)
	add("icon", b.IconKey != "" && b.IconKey != g.IconKey)
	add("screenshots", len(b.Screenshots) > 0 && !slices.Equal(b.Screenshots, g.Screenshots))
	newScopes = append(newScopes, newScopesOf(g.Scopes, b.Scopes)...)
	return changes, newScopes
}

func newScopesOf(before, after []string) []string {
	out := newScopes(before, after)
	if out == nil {
		return []string{}
	}
	return out
}
