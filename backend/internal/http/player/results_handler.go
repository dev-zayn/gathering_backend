package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Results, read back: the player's history, and one round's result sheet,
// which outlives the room. Both are kept for good. docs/ROOMS.md, "Results
// (M5)" and "Rounds", is the contract.

type historyRequest struct {
	Cursor string `query:"cursor" doc:"next_cursor from the page before; leave it out for the first page."`
	Limit  int    `query:"limit" default:"20" doc:"1 to 50 (limit: 1_to_50)."`
}

type historyResponse struct {
	Body *rooms.History
}

type roomResultsRequest struct {
	Room  string `path:"room" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n"`
	Round string `query:"round" example:"2" doc:"Which round, a whole number from 1 (round: must_be_positive). Leave it out for the latest round with results the caller may read."`
}

type roomResultsResponse struct {
	Body *rooms.RoomResults
}

func (h *Handler) registerResults(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "me-history", Method: http.MethodGet, Path: "/v1/me/history",
		Summary: "What I played",
		Description: "Every round the player has a result in, newest first, with the room, the round " +
			"and their own score and rank, a page at a time: hand next_cursor back as cursor until it is null.",
		Tags: []string{"me"}, Metadata: session,
	}, h.history)

	huma.Register(api, huma.Operation{
		OperationID: "room-results", Method: http.MethodGet, Path: "/v1/rooms/{room}/results",
		Summary: "A round's results",
		Description: "One round of a room, ranked, ties in seat order, names and pictures joined so a " +
			"player who left still shows; rounds lists the rounds there are to read. Every round for " +
			"anybody who was in the room while it is kept (a day after its end), and for good the " +
			"rounds a player has a result in; not_found for everything else.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.roomResults)
}

func (h *Handler) history(ctx context.Context, in *historyRequest) (*historyResponse, error) {
	page, err := h.Rooms.History(ctx, caller.FromContext(ctx).Subject, in.Cursor, in.Limit)
	if err != nil {
		return nil, err
	}
	return &historyResponse{Body: page}, nil
}

func (h *Handler) roomResults(ctx context.Context, in *roomResultsRequest) (*roomResultsResponse, error) {
	sheet, err := h.Rooms.RoomResults(ctx, caller.FromContext(ctx).Subject, in.Room, in.Round)
	if err != nil {
		return nil, err
	}
	return &roomResultsResponse{Body: sheet}, nil
}
