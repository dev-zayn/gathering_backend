package games

// A build and a publisher's game as the console reads them
// (docs/PUBLISHERS.md). One shape for the publisher's routes and the
// reviewer's, so the two never describe the same build differently. The
// names are unique: huma names a schema by its bare type name.

import (
	"context"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
)

type BuildView struct {
	ID           string       `json:"id" example:"bld_7f3k9d2m8q1x4b6n"`
	Game         string       `json:"game" example:"yourstudio.game"`
	Status       string       `json:"status" enum:"testing,in_review,approved,rejected,published,withdrawn"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	ManifestURL  string       `json:"manifest_url"`
	Entry        string       `json:"entry"`
	FetchedAt    string       `json:"fetched_at"`
	Orientation  string       `json:"orientation" enum:"portrait,landscape"`
	Splash       bool         `json:"splash"`
	Scopes       []string     `json:"scopes"`
	Permissions  []string     `json:"permissions"`
	RedirectURIs []string     `json:"redirect_uris"`
	Room         BuildRoom    `json:"room"`
	Store        BuildStore   `json:"store"`
	Notes        string       `json:"notes" doc:"The publisher's words for testers and the reviewer."`
	Review       *BuildReview `json:"review" doc:"The last decision, while the build is approved or rejected."`
	Changes      []string     `json:"changes" doc:"What differs from the live game; empty for a game never published."`
	NewScopes    []string     `json:"new_scopes" doc:"Scopes the live game did not ask for."`
	CreatedAt    string       `json:"created_at"`
	SubmittedAt  *string      `json:"submitted_at" nullable:"true"`
	PublishedAt  *string      `json:"published_at" nullable:"true"`
	ClosedAt     *string      `json:"closed_at" nullable:"true"`
}

// BuildRoom is the build's room block, flat.
type BuildRoom struct {
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Host       string `json:"host" enum:"authoritative,peer"`
	Pace       string `json:"pace" enum:"turn,realtime"`
	LAN        bool   `json:"lan"`
	State      string `json:"state" enum:"blob,none"`
	Rejoin     bool   `json:"rejoin"`
	Spectators bool   `json:"spectators"`
	Chat       bool   `json:"chat"`
}

// BuildStore is the store copy the build will have.
type BuildStore struct {
	Tagline     string            `json:"tagline"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Icon        string            `json:"icon" doc:"A link that expires: the build's own icon, or the live game's it inherited."`
	Screenshots []BuildScreenshot `json:"screenshots"`
}

type BuildScreenshot struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type BuildReview struct {
	Note string `json:"note"`
	At   string `json:"at"`
	By   string `json:"by" doc:"The admin, adm_…; empty to the publisher."`
}

// BuildBrief is one line of a game's builds.
type BuildBrief struct {
	ID          string  `json:"id"`
	Version     string  `json:"version"`
	Status      string  `json:"status" enum:"testing,in_review,approved,rejected,published,withdrawn"`
	Note        string  `json:"note" doc:"The reviewer's words on its last decision."`
	CreatedAt   string  `json:"created_at"`
	SubmittedAt *string `json:"submitted_at" nullable:"true"`
	ReviewedAt  *string `json:"reviewed_at" nullable:"true"`
	PublishedAt *string `json:"published_at" nullable:"true"`
}

// StudioGameView is one of a publisher's games.
type StudioGameView struct {
	ID        string       `json:"id" example:"yourstudio.game"`
	Name      string       `json:"name"`
	Listing   string       `json:"listing" enum:"published,unpublished,suspended"`
	Version   string       `json:"version" doc:"The live game's; empty before the first publish."`
	Icon      string       `json:"icon" doc:"The live game's tile, a link that expires."`
	Tagline   string       `json:"tagline"`
	Category  string       `json:"category"`
	Manifest  string       `json:"manifest_url" doc:"Where the live manifest was read from; a next build reads here unless told otherwise."`
	Players   int          `json:"players" doc:"How many shelves hold it."`
	Testers   int          `json:"testers"`
	CreatedAt string       `json:"created_at"`
	Build     *BuildView   `json:"build" doc:"The open build, in full; null when there is none."`
	Builds    []BuildBrief `json:"builds" doc:"Every build, newest first; only on the game's own page."`
}

// ViewBuild draws a build of g, its pictures signed for this request. by is
// whether the reviewer's id is shown: an admin's read, not a publisher's.
func ViewBuild(ctx context.Context, store files.Files, b *model.GameBuild, g *model.Game, by bool) BuildView {
	over := b.Over(*g)
	shots := []BuildScreenshot{}
	for _, s := range ScreenshotURLs(ctx, store, &over) {
		shots = append(shots, BuildScreenshot(s))
	}
	changes, fresh := Changes(b, g)
	v := BuildView{
		ID: b.ID, Game: b.GameID, Status: string(b.Status), Name: b.Name, Version: b.Version,
		ManifestURL: b.ManifestURL, Entry: b.Entry, FetchedAt: stampAt(b.FetchedAt),
		Orientation: string(b.Orientation), Splash: b.Splash,
		Scopes: nonNilStrings(b.Scopes), Permissions: nonNilStrings(b.Permissions),
		RedirectURIs: nonNilStrings(b.RedirectURIs),
		Room: BuildRoom{
			Min: b.Room.Players.Min, Max: b.Room.Players.Max, Host: string(b.Room.Host), Pace: string(b.Room.Pace),
			LAN: b.Room.LAN, State: string(b.Room.State), Rejoin: b.Room.Rejoin, Spectators: b.Room.Spectators,
			Chat: b.Room.ChatOn(),
		},
		Store: BuildStore{
			Tagline: b.Tagline, Description: b.Description, Category: string(b.Category),
			Icon: IconURL(ctx, store, &over), Screenshots: shots,
		},
		Notes: b.Notes, Changes: changes, NewScopes: fresh,
		CreatedAt:   stampAt(b.CreatedAt),
		SubmittedAt: stampPtr(b.SubmittedAt), PublishedAt: stampPtr(b.PublishedAt), ClosedAt: stampPtr(b.ClosedAt),
	}
	if b.ReviewedAt != nil && (b.Status == enum.BuildApproved || b.Status == enum.BuildRejected) {
		v.Review = &BuildReview{Note: b.ReviewNote, At: stampAt(*b.ReviewedAt)}
		if by {
			v.Review.By = b.ReviewedBy
		}
	}
	return v
}

// BriefOf is one line of a game's builds.
func BriefOf(b *model.GameBuild) BuildBrief {
	return BuildBrief{
		ID: b.ID, Version: b.Version, Status: string(b.Status), Note: b.ReviewNote,
		CreatedAt: stampAt(b.CreatedAt), SubmittedAt: stampPtr(b.SubmittedAt),
		ReviewedAt: stampPtr(b.ReviewedAt), PublishedAt: stampPtr(b.PublishedAt),
	}
}

// ViewStudioGame draws one of a publisher's games.
func ViewStudioGame(ctx context.Context, store files.Files, sg *StudioGame, by bool) StudioGameView {
	g := &sg.Game
	v := StudioGameView{
		ID: g.ID, Name: g.Name, Listing: string(g.Listing), Tagline: g.Tagline, Category: string(g.Category),
		Manifest: g.ManifestURL, Players: sg.Players, Testers: sg.Testers, CreatedAt: stampAt(g.CreatedAt),
		Builds: []BuildBrief{},
	}
	// Before the first publish the row's name and version are the first
	// build's, not anything live: the open build names the game then.
	if g.Listing != enum.Unpublished {
		v.Version = g.Version
		v.Icon = IconURL(ctx, store, g)
	}
	if sg.Build != nil {
		b := ViewBuild(ctx, store, sg.Build, g, by)
		v.Build = &b
		if g.Listing == enum.Unpublished {
			v.Name = sg.Build.Name
		}
	}
	for i := range sg.Builds {
		v.Builds = append(v.Builds, BriefOf(&sg.Builds[i]))
	}
	return v
}

// InviteView is a tester's code as the console reads it.
type InviteView struct {
	Code      string  `json:"code" example:"K7M2QX4P"`
	Label     string  `json:"label"`
	Uses      int     `json:"uses"`
	MaxUses   int     `json:"max_uses" doc:"0: no limit."`
	ExpiresAt string  `json:"expires_at"`
	RevokedAt *string `json:"revoked_at" nullable:"true"`
	CreatedAt string  `json:"created_at"`
}

func ViewInvite(i *model.TesterInvite) InviteView {
	return InviteView{
		Code: i.Code, Label: i.Label, Uses: i.Uses, MaxUses: i.MaxUses,
		ExpiresAt: stampAt(i.ExpiresAt), RevokedAt: stampPtr(i.RevokedAt), CreatedAt: stampAt(i.CreatedAt),
	}
}

func stampAt(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func stampPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := stampAt(*t)
	return &s
}
