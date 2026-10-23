package guest

// The invite link (docs/JOINING.md): the page a room's link opens wherever
// the app does not catch it, the same page for a tournament's link
// (docs/TOURNAMENTS.md, "The link"), and the two documents a phone reads
// before it lets the app catch either. Pages and documents, not the JSON
// API: each sets its own content type, and none of them answers with the
// API's error body.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/biz/tournaments"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// AppLinks is how the phones claim the link host as the app's, and where a
// phone without the app goes to get it. An empty field is a deployment that
// has not said yet: no fingerprint or no team id serves that document's
// empty form, and the page shows no key for a store it has no address for.
type AppLinks struct {
	AndroidPackage string
	SHA256         []string

	AppleTeamID string
	IOSBundle   string

	AppStoreURL  string
	PlayStoreURL string
}

// documentCache is how long a phone may keep either document. They change
// when the app's signing or ids do, which is rarely and never in a hurry.
const documentCache = "max-age=3600"

type inviteRequest struct {
	Code     string `path:"code" example:"K7M2QX" doc:"The room's code, read like a typed one: case, spaces and dashes do not count."`
	Language string `header:"Accept-Language" doc:"English when it prefers en over ar; Arabic otherwise, and when it is absent."`
}

type tournamentLinkRequest struct {
	Code     string `path:"code" example:"T4K9QX" doc:"The tournament's code, read like a typed one: case, spaces and dashes do not count."`
	Language string `header:"Accept-Language" doc:"English when it prefers en over ar; Arabic otherwise, and when it is absent."`
}

// pageResponse is an HTML page, written whole. Every form of it carries the
// same headers.
type pageResponse struct {
	Status         int
	ContentType    string `header:"Content-Type"`
	CacheControl   string `header:"Cache-Control"`
	Vary           string `header:"Vary"`
	CSP            string `header:"Content-Security-Policy"`
	ReferrerPolicy string `header:"Referrer-Policy"`
	NoSniff        string `header:"X-Content-Type-Options"`
	Body           []byte
}

// assetLink is one statement of Android's Digital Asset Links: this host's
// links may open the app signed with these certificates.
type assetLink struct {
	Relation []string    `json:"relation" example:"[\"delegate_permission/common.handle_all_urls\"]"`
	Target   assetTarget `json:"target"`
}

type assetTarget struct {
	Namespace    string   `json:"namespace" enum:"android_app"`
	PackageName  string   `json:"package_name" example:"app.gathering.jamaa"`
	Fingerprints []string `json:"sha256_cert_fingerprints" doc:"Every APP_LINKS_SHA256, in the order configured."`
}

type assetLinksResponse struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         []assetLink
}

// siteAssociation is Apple's apple-app-site-association: which app may open
// which paths of this host. Only the room and tournament links.
type siteAssociation struct {
	AppLinks siteAppLinks `json:"applinks"`
}

type siteAppLinks struct {
	Details []siteAppDetail `json:"details"`
}

type siteAppDetail struct {
	AppIDs     []string        `json:"appIDs" doc:"APPLE_TEAM_ID.APP_IOS_BUNDLE"`
	Components []sitePathMatch `json:"components"`
}

type sitePathMatch struct {
	Path string `json:"/" example:"/r/*"`
}

type siteAssociationResponse struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         siteAssociation
}

func (h *Handler) registerLinks(api huma.API) {
	html := func(what string) *huma.Response {
		return &huma.Response{
			Description: what,
			Content:     map[string]*huma.MediaType{"text/html": {Schema: &huma.Schema{Type: "string"}}},
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "invite-link",
		Method:      http.MethodGet,
		Path:        "/r/{code}",
		Summary:     "A room's invite link",
		Description: "The page behind a room's link, for whoever opens it where the app does not " +
			"catch it. The game and the code with a copy key, the stores' keys, and nothing else " +
			"about the room. Arabic, or English when Accept-Language prefers en over ar. " +
			"Always a page, never the API's error body.",
		Tags: []string{"links"},
		Responses: map[string]*huma.Response{
			"200": html("An open room holds the code: its game and its code."),
			"404": html("The page's not-found form: no open room holds the code, it ended, or it cannot be a code."),
			"500": html("The page's try-again form: the room could not be read."),
		},
	}, h.invite)

	huma.Register(api, huma.Operation{
		OperationID: "tournament-link",
		Method:      http.MethodGet,
		Path:        "/t/{code}",
		Summary:     "A tournament's link",
		Description: "The page behind a tournament's link, for whoever opens it where the app does not " +
			"catch it: the tournament's name, its game and its code with a copy key, and the stores' " +
			"keys. Arabic, or English when Accept-Language prefers en over ar. Always a page, never " +
			"the API's error body.",
		Tags: []string{"links"},
		Responses: map[string]*huma.Response{
			"200": html("An open or running tournament holds the code: its name, its game and its code."),
			"404": html("The page's not-found form: no live tournament holds the code, it is over, or it cannot be a code."),
			"500": html("The page's try-again form: the tournament could not be read."),
		},
	}, h.tournamentLink)

	huma.Register(api, huma.Operation{
		OperationID: "assetlinks",
		Method:      http.MethodGet,
		Path:        "/.well-known/assetlinks.json",
		Summary:     "Android's claim on the link host",
		Description: "What an Android phone reads before it lets the app open /r/ and /t/ links on " +
			"its own: the app's package and its signing certificates. An empty list until " +
			"APP_LINKS_SHA256 is set.",
		Tags: []string{"well-known"},
	}, h.assetLinks)

	huma.Register(api, huma.Operation{
		OperationID: "apple-app-site-association",
		Method:      http.MethodGet,
		Path:        "/.well-known/apple-app-site-association",
		Summary:     "iOS's claim on the link host",
		Description: "What an iPhone reads before it lets the app open /r/ and /t/ links on its own: " +
			"the app's id and the paths it takes. No details until APPLE_TEAM_ID is set.",
		Tags: []string{"well-known"},
	}, h.siteAssociation)
}

// invite is the room link's page in whichever form the code calls for: the
// room, not found, or try again.
func (h *Handler) invite(ctx context.Context, in *inviteRequest) (*pageResponse, error) {
	w := wordsFor(in.Language)
	card, err := h.linkCard(ctx, in.Code)
	var shown *shownCard
	if card != nil {
		shown = &shownCard{
			card:  linkCard{Heading: card.Game.Name, CodeLabel: w.CodeLabel, Code: card.Code},
			title: strings.Replace(w.Join, "{game}", card.Game.Name, 1),
			named: card.Game.Name != "",
			icon:  card.Game.Icon,
		}
	}
	return h.page(ctx, w, "/r/{code}", shown, err, w.OpenIt, w.NotFound)
}

// tournamentLink is the tournament link's page, in the same forms.
func (h *Handler) tournamentLink(ctx context.Context, in *tournamentLinkRequest) (*pageResponse, error) {
	w := wordsFor(in.Language)
	var (
		card *tournaments.LinkCard
		err  error
	)
	if h.Tournaments == nil {
		err = apperr.New(http.StatusServiceUnavailable, "tournaments_unavailable")
	} else {
		card, err = h.Tournaments.LinkCard(ctx, in.Code)
	}
	var shown *shownCard
	if card != nil {
		shown = &shownCard{
			card:  linkCard{Heading: card.Name, Sub: card.Game.Name, CodeLabel: w.TourCodeLabel, Code: card.Code},
			title: strings.Replace(w.TourJoin, "{name}", card.Name, 1),
			named: true,
			icon:  card.Game.Icon,
		}
	}
	return h.page(ctx, w, "/t/{code}", shown, err, w.TourOpenIt, w.TourNotFound)
}

// wordsFor is the page's language, by Accept-Language.
func wordsFor(language string) words {
	if prefersEnglish(language) {
		return english
	}
	return arabic
}

// shownCard is what a link found: its card, the page's title, whether the
// title names it, and the game's icon.
type shownCard struct {
	card  linkCard
	title string
	named bool
	icon  string
}

// page writes a link's page in whichever form the lookup calls for: the
// card, not found (nothing found), or try again (it could not be read).
func (h *Handler) page(ctx context.Context, w words, path string, shown *shownCard, err error,
	openIt, notFound string) (*pageResponse, error) {
	v := pageView{W: w, Title: w.Brand, Style: pageStyle, Stores: h.stores()}

	status := http.StatusOK
	switch {
	case err != nil:
		// Not the API's error body: the page says try again, and the log
		// keeps the reason, as every other 500 does.
		cause := err
		var e *apperr.Error
		if errors.As(err, &e) && e.Cause != nil {
			cause = e.Cause
		}
		applog.From(ctx).Error("request failed", "path", path, "status", http.StatusInternalServerError, "err", cause)
		status, v.Message = http.StatusInternalServerError, w.TryAgain
	case shown == nil:
		status, v.Message = http.StatusNotFound, notFound
	default:
		card := shown.card
		v.Card, v.Script, v.OpenIt = &card, copyScript, openIt
		if shown.named {
			v.Title = shown.title
		}
		// The icon is drawn only from a host the policy can name.
		if from := origin(shown.icon); from != "" {
			v.Icon, v.Images = shown.icon, from
		}
	}

	var body bytes.Buffer
	if err := page.Execute(&body, v); err != nil {
		return nil, apperr.Internal(err)
	}
	return &pageResponse{
		Status:         status,
		ContentType:    "text/html; charset=utf-8",
		CacheControl:   "no-store",
		Vary:           "Accept-Language",
		CSP:            policy(v.Card != nil, v.Images),
		ReferrerPolicy: "no-referrer",
		NoSniff:        "nosniff",
		Body:           body.Bytes(),
	}, nil
}

// linkCard asks the rooms. No rooms is a process wired for the spec alone,
// which serves nothing; it says try again rather than fall over.
func (h *Handler) linkCard(ctx context.Context, code string) (*rooms.LinkCard, error) {
	if h.Rooms == nil {
		return nil, apperr.New(http.StatusServiceUnavailable, "rooms_unavailable")
	}
	return h.Rooms.LinkCard(ctx, code)
}

// stores is a key for each store with an address, App Store first.
func (h *Handler) stores() []storeKey {
	var out []storeKey
	if h.AppLinks.AppStoreURL != "" {
		out = append(out, storeKey{Name: "App Store", URL: h.AppLinks.AppStoreURL})
	}
	if h.AppLinks.PlayStoreURL != "" {
		out = append(out, storeKey{Name: "Google Play", URL: h.AppLinks.PlayStoreURL})
	}
	return out
}

func (h *Handler) assetLinks(context.Context, *struct{}) (*assetLinksResponse, error) {
	out := &assetLinksResponse{ContentType: "application/json", CacheControl: documentCache, Body: []assetLink{}}
	if len(h.AppLinks.SHA256) == 0 {
		return out, nil
	}
	out.Body = append(out.Body, assetLink{
		Relation: []string{"delegate_permission/common.handle_all_urls"},
		Target: assetTarget{
			Namespace:    "android_app",
			PackageName:  h.AppLinks.AndroidPackage,
			Fingerprints: append([]string(nil), h.AppLinks.SHA256...),
		},
	})
	return out, nil
}

func (h *Handler) siteAssociation(context.Context, *struct{}) (*siteAssociationResponse, error) {
	out := &siteAssociationResponse{ContentType: "application/json", CacheControl: documentCache}
	out.Body.AppLinks.Details = []siteAppDetail{}
	if h.AppLinks.AppleTeamID == "" {
		return out, nil
	}
	out.Body.AppLinks.Details = append(out.Body.AppLinks.Details, siteAppDetail{
		AppIDs:     []string{h.AppLinks.AppleTeamID + "." + h.AppLinks.IOSBundle},
		Components: []sitePathMatch{{Path: "/r/*"}, {Path: "/t/*"}},
	})
	return out, nil
}
