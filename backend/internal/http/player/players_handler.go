package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Other players: the card and the search. Friendship joins the card with the
// friends milestone.

type playerCardRequest struct {
	Handle string `path:"handle" example:"zayn_4821"`
}

type playerCardResponse struct {
	Body struct {
		playerJSON
		Friendship string `json:"friendship" enum:"none,requested,incoming,friends,blocked" doc:"Where the caller stands with them."`
	}
}

type searchRequest struct {
	Q     string `query:"q" required:"true" minLength:"3" maxLength:"24" doc:"The start of a handle, with or without the @."`
	Limit int    `query:"limit" default:"20" minimum:"1" maximum:"50"`
}

type searchResponse struct {
	Body struct {
		Players []playerJSON `json:"players"`
	}
}

func (h *Handler) registerPlayers(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "players-search", Method: http.MethodGet, Path: "/v1/players/search",
		Summary:     "Find a player by handle",
		Description: "The start of a handle, three characters at least. Disabled players are not listed.",
		Tags:        []string{"players"}, Metadata: session,
	}, h.search)

	huma.Register(api, huma.Operation{
		OperationID: "player", Method: http.MethodGet, Path: "/v1/players/{handle}",
		Summary:     "A player's card",
		Description: "What anybody signed in may see of a player: nickname, handle, avatar, and where the caller stands with them.",
		Tags:        []string{"players"}, Metadata: session,
	}, h.card)
}

func (h *Handler) search(ctx context.Context, in *searchRequest) (*searchResponse, error) {
	found, err := h.Profiles.Search(ctx, in.Q, in.Limit)
	if err != nil {
		return nil, err
	}
	out := &searchResponse{}
	out.Body.Players = make([]playerJSON, 0, len(found))
	for i := range found {
		out.Body.Players = append(out.Body.Players, card(&found[i]))
	}
	return out, nil
}

func (h *Handler) card(ctx context.Context, in *playerCardRequest) (*playerCardResponse, error) {
	p, err := h.Profiles.Card(ctx, in.Handle)
	if err != nil {
		return nil, err
	}
	out := &playerCardResponse{}
	out.Body.playerJSON = card(p)
	out.Body.Friendship = "none"
	return out, nil
}
