package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
)

// The store and the shelf: the same rows, each saying whether this player
// has it. Opening one is the guest group's session mint.

type gamesListRequest struct {
	Category string `query:"category" doc:"One of speed, brain, words, luck, guess, draw, party, other. Empty is the whole store."`
}

// roomSummaryJSON is what a player reads before joining: how many, how fast,
// and whether it works on a Wi-Fi with no internet.
type roomSummaryJSON struct {
	Min  int    `json:"min"`
	Max  int    `json:"max"`
	Pace string `json:"pace" enum:"turn,realtime"`
	LAN  bool   `json:"lan"`
}

// roomJSON is the whole block, for the page in the store.
type roomJSON struct {
	roomSummaryJSON
	Host       string `json:"host" enum:"authoritative,peer"`
	State      string `json:"state" enum:"blob,none"`
	Rejoin     bool   `json:"rejoin"`
	Spectators bool   `json:"spectators"`
}

type gameJSON struct {
	ID          string          `json:"id" example:"fixture.xo"`
	Name        string          `json:"name" example:"إكس أو"`
	Tagline     string          `json:"tagline"`
	Icon        string          `json:"icon" doc:"A link that expires. Empty when the game has no tile yet."`
	Category    string          `json:"category"`
	Featured    bool            `json:"featured"`
	InLibrary   bool            `json:"in_library" doc:"On this player's shelf: opened at least once."`
	Splash      bool            `json:"splash"`
	Orientation string          `json:"orientation" enum:"portrait,landscape"`
	Room        roomSummaryJSON `json:"room"`
	Version     string          `json:"version"`
	Publisher   string          `json:"publisher"`
	Permissions []string        `json:"permissions" doc:"What the app asks the phone for on the game's behalf. On the row as well as the page, because a tile opens the game and the app asks for these before it does."`
}

type gamesListResponse struct {
	Body struct {
		Games []gameJSON `json:"games"`
	}
}

type gamePath struct {
	Game string `path:"game" example:"fixture.xo"`
}

type gameDetailJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tagline     string   `json:"tagline"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Screenshots []string `json:"screenshots"`
	Category    string   `json:"category"`
	Featured    bool     `json:"featured"`
	InLibrary   bool     `json:"in_library"`
	Plays       int      `json:"plays" doc:"How many times this player opened it."`
	LastPlayed  *string  `json:"last_played_at,omitempty"`
	Splash      bool     `json:"splash"`
	Orientation string   `json:"orientation" enum:"portrait,landscape"`
	Permissions []string `json:"permissions" doc:"What the app will ask the phone for, on the game's behalf."`
	Scopes      []string `json:"scopes" doc:"What the game's server may read of the player's record."`
	Room        roomJSON `json:"room"`
	Version     string   `json:"version"`
	Publisher   string   `json:"publisher"`
	Tier        string   `json:"tier" enum:"first-party,partner"`
}

type gameDetailResponse struct{ Body gameDetailJSON }

func (h *Handler) registerGames(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "games", Method: http.MethodGet, Path: "/v1/games",
		Summary: "The store, as it stands for this player",
		Description: "Every published game, the featured ones first, then ours, each marked " +
			"with whether it is on this player's shelf. One call for the store and the " +
			"shelf, because the two differ by one flag.",
		Tags: []string{"games"}, Metadata: session,
	}, h.listGames)

	huma.Register(api, huma.Operation{
		OperationID: "game", Method: http.MethodGet, Path: "/v1/games/{game}",
		Summary: "One game, its page in the store",
		Description: "The words and pictures the store shows, the room it is played in, what " +
			"it asks for, and whether this player has it. There is nothing to agree to: " +
			"opening it is the whole of installing it.",
		Tags: []string{"games"}, Metadata: session,
	}, h.game)

	huma.Register(api, huma.Operation{
		OperationID: "game-remove", Method: http.MethodDelete, Path: "/v1/games/{game}",
		Summary: "Take a game off the shelf",
		Description: "The tile goes and every token the game's server holds for this player " +
			"stops from that moment. Nothing inside the game is deleted — that data is " +
			"the studio's. not_in_library when it was not there.",
		Tags: []string{"games"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.removeGame)
}

func (h *Handler) listGames(ctx context.Context, in *gamesListRequest) (*gamesListResponse, error) {
	claims := caller.FromContext(ctx)
	rows, err := h.Games.List(ctx, claims.Subject, in.Category)
	if err != nil {
		return nil, err
	}

	out := &gamesListResponse{}
	out.Body.Games = make([]gameJSON, 0, len(rows))
	for i := range rows {
		out.Body.Games = append(out.Body.Games, h.gameRow(ctx, &rows[i].Game, rows[i].InLibrary))
	}
	return out, nil
}

func (h *Handler) gameRow(ctx context.Context, g *model.Game, inLibrary bool) gameJSON {
	return gameJSON{
		ID:          g.ID,
		Name:        g.Name,
		Tagline:     g.Tagline,
		Icon:        games.IconURL(ctx, h.Files, g),
		Category:    string(g.Category),
		Featured:    g.Featured,
		InLibrary:   inLibrary,
		Splash:      g.Splash,
		Orientation: string(g.Orientation),
		Room:        roomSummary(g),
		Version:     g.Version,
		Publisher:   g.Publisher,
		Permissions: nonNil(g.Permissions),
	}
}

func roomSummary(g *model.Game) roomSummaryJSON {
	return roomSummaryJSON{
		Min: g.Room.Players.Min, Max: g.Room.Players.Max, Pace: string(g.Room.Pace), LAN: g.Room.LAN,
	}
}

func (h *Handler) game(ctx context.Context, in *gamePath) (*gameDetailResponse, error) {
	claims := caller.FromContext(ctx)
	d, err := h.Games.Detail(ctx, claims.Subject, in.Game)
	if err != nil {
		return nil, err
	}

	g := &d.Game
	out := &gameDetailResponse{Body: gameDetailJSON{
		ID:          g.ID,
		Name:        g.Name,
		Tagline:     g.Tagline,
		Description: g.Description,
		Icon:        games.IconURL(ctx, h.Files, g),
		Screenshots: screenshotURLs(ctx, h.Files, g),
		Category:    string(g.Category),
		Featured:    g.Featured,
		InLibrary:   d.Library != nil,
		Splash:      g.Splash,
		Orientation: string(g.Orientation),
		Permissions: nonNil(g.Permissions),
		Scopes:      nonNil(g.Scopes),
		Room: roomJSON{
			roomSummaryJSON: roomSummary(g),
			Host:            string(g.Room.Host),
			State:           string(g.Room.State),
			Rejoin:          g.Room.Rejoin,
			Spectators:      g.Room.Spectators,
		},
		Version:   g.Version,
		Publisher: g.Publisher,
		Tier:      string(g.Tier),
	}}
	if d.Library != nil {
		out.Body.Plays = d.Library.Plays
		if d.Library.LastPlayedAt != nil {
			s := stamp(*d.Library.LastPlayedAt)
			out.Body.LastPlayed = &s
		}
	}
	return out, nil
}

func (h *Handler) removeGame(ctx context.Context, in *gamePath) (*noContent, error) {
	claims := caller.FromContext(ctx)
	return nil, h.Games.Remove(ctx, claims.Subject, in.Game)
}

// A player gets the links alone; the ids are the admin's business.
func screenshotURLs(ctx context.Context, store files.Files, g *model.Game) []string {
	out := []string{}
	for _, s := range games.ScreenshotURLs(ctx, store, g) {
		out = append(out, s.URL)
	}
	return out
}

// nonNil keeps a JSON array an array. A client should not have to tell null
// from empty.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
