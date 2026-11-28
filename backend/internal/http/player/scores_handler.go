package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/scores"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Scores: a game played alone keeping the player's best on its boards, and a
// board among friends. docs/SCORES.md is the contract.

type scoreRequest struct {
	Game string `path:"game" maxLength:"64" example:"arcade.reflex"`
	Body struct {
		Board string   `json:"board,omitempty" maxLength:"32" example:"reaction" doc:"One of the boards the game's manifest declares (board: required, unknown)."`
		Score *float64 `json:"score,omitempty" example:"231" doc:"Within the board's min and max when it has them (score: required, below_min, above_max)."`
	}
}

type scoreResponse struct {
	Body *scores.Sent
}

type scoreBoardRequest struct {
	Game  string `path:"game" maxLength:"64" example:"arcade.reflex"`
	Board string `path:"board" maxLength:"32" example:"reaction"`
	Scope string `query:"scope" example:"friends" doc:"friends, the only one in v1, and the default (scope: friends_only otherwise)."`
}

type scoreBoardResponse struct {
	Body *scores.ScoreBoard
}

func (h *Handler) registerScores(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "game-score", Method: http.MethodPost, Path: "/v1/games/{game}/scores",
		Summary: "Send a score from a game played alone",
		Description: "Keeps the player's best on one of the game's boards (its manifest's scores block) " +
			"and counts a play. not_found (no such game in the store), invalid_fields (board: required " +
			"or unknown, score: required, below_min or above_max), too_many_requests (200 an hour per game).",
		Tags: []string{"scores"}, Metadata: session,
	}, h.score)

	huma.Register(api, huma.Operation{
		OperationID: "game-scores", Method: http.MethodGet, Path: "/v1/games/{game}/scores/{board}",
		Summary: "A game's board among friends",
		Description: "The caller and their friends with a score on the board, ranked by best in the " +
			"board's order; equal bests share a rank. not_found for a game not in the store or a board " +
			"it does not declare.",
		Tags: []string{"scores"}, Metadata: session,
	}, h.scoreBoard)
}

func (h *Handler) score(ctx context.Context, in *scoreRequest) (*scoreResponse, error) {
	sent, err := h.Scores.Submit(ctx, caller.FromContext(ctx).Subject, in.Game, in.Body.Board, in.Body.Score)
	if err != nil {
		return nil, err
	}
	return &scoreResponse{Body: sent}, nil
}

func (h *Handler) scoreBoard(ctx context.Context, in *scoreBoardRequest) (*scoreBoardResponse, error) {
	board, err := h.Scores.Board(ctx, caller.FromContext(ctx).Subject, in.Game, in.Board, in.Scope)
	if err != nil {
		return nil, err
	}
	return &scoreBoardResponse{Body: board}, nil
}
