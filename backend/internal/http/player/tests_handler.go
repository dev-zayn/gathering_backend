package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// A player testing a publisher's games (docs/PUBLISHERS.md, "The player's
// routes"): joining by a code, the list of what they test, and leaving.

type testJoinRequest struct {
	Body struct {
		Code string `json:"code" required:"true" maxLength:"64" example:"K7M2-QX4P" doc:"The tester's code the publisher gave, typed however it was read out: case, spaces and dashes do not count."`
	}
}

type testGameJSON struct {
	ID   string `json:"id" example:"yourstudio.game"`
	Name string `json:"name"`
	Icon string `json:"icon" doc:"A link that expires; empty when the game has no tile yet."`
}

type testJoinResponse struct {
	Body struct {
		Game    testGameJSON `json:"game"`
		Version string       `json:"version" doc:"The build's while one is open, else the live one's; empty when there is nothing to play yet."`
		Testing bool         `json:"testing" doc:"A build is open: the player gets it in place of the live game."`
		Already bool         `json:"already" doc:"The player was a tester already; nothing changed."`
	}
}

type myTestJSON struct {
	Game      testGameJSON `json:"game"`
	Publisher string       `json:"publisher"`
	Version   string       `json:"version"`
	Testing   bool         `json:"testing"`
	JoinedAt  string       `json:"joined_at"`
}

type myTestsResponse struct {
	Body struct {
		Items []myTestJSON `json:"items"`
	}
}

func (h *Handler) registerTests(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "test-join", Method: http.MethodPost, Path: "/v1/tests",
		Summary: "Test a game with its publisher's code",
		Description: "The player tests the code's game from now on: its open build in place " +
			"of the live game, in the store, on its page, at every open and in every room " +
			"they make. Refused, in order: 422 code: must_be_8_symbols; 429 after 20 tries " +
			"an hour; 404 invite_not_found (no such code, revoked, expired, used up, or the " +
			"game suspended); 409 testers_full (500 testers).",
		Tags: []string{"games"}, Metadata: session,
	}, h.joinTest)

	huma.Register(api, huma.Operation{
		OperationID: "my-tests", Method: http.MethodGet, Path: "/v1/me/tests",
		Summary:     "The games this player tests",
		Description: "The newest first. Suspended games are left out.",
		Tags:        []string{"games"}, Metadata: session,
	}, h.myTests)

	huma.Register(api, huma.Operation{
		OperationID: "test-leave", Method: http.MethodDelete, Path: "/v1/me/tests/{game}",
		Summary: "Stop testing a game",
		Description: "The live game from now on; nothing on the shelf is removed. " +
			"404 not_found when the player was not a tester.",
		Tags: []string{"games"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.leaveTest)
}

func (h *Handler) joinTest(ctx context.Context, in *testJoinRequest) (*testJoinResponse, error) {
	joined, err := h.Studio.JoinTest(ctx, caller.FromContext(ctx).Subject, in.Body.Code)
	if err != nil {
		return nil, err
	}
	out := &testJoinResponse{}
	out.Body.Game = h.testGame(ctx, &joined.Game)
	out.Body.Version = joined.Game.Version
	out.Body.Testing = joined.Testing
	out.Body.Already = joined.Already
	return out, nil
}

func (h *Handler) testGame(ctx context.Context, g *model.Game) testGameJSON {
	return testGameJSON{ID: g.ID, Name: g.Name, Icon: games.IconURL(ctx, h.Files, g)}
}

func (h *Handler) myTests(ctx context.Context, _ *struct{}) (*myTestsResponse, error) {
	rows, err := h.Studio.PlayerTests(ctx, caller.FromContext(ctx).Subject)
	if err != nil {
		return nil, err
	}
	out := &myTestsResponse{}
	out.Body.Items = make([]myTestJSON, 0, len(rows))
	for i := range rows {
		g := &rows[i].Game
		out.Body.Items = append(out.Body.Items, myTestJSON{
			Game:      h.testGame(ctx, g),
			Publisher: g.Publisher,
			Version:   g.Version,
			Testing:   rows[i].Testing,
			JoinedAt:  stamp(rows[i].JoinedAt),
		})
	}
	return out, nil
}

func (h *Handler) leaveTest(ctx context.Context, in *gamePath) (*noContent, error) {
	return nil, h.Studio.LeaveTest(ctx, caller.FromContext(ctx).Subject, in.Game)
}
