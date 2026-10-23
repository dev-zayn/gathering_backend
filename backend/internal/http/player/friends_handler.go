package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/friends"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Friends: the list with presence, requests and their answers, a removal, a
// block, and a game's leaderboard among friends. docs/FRIENDS.md is the
// contract.

type friendsResponse struct {
	Body struct {
		Friends []friends.Friend `json:"friends" doc:"In a room first, then online, then offline, each by nickname."`
	}
}

type friendRequestsResponse struct {
	Body *friends.Requests
}

type friendRequestBody struct {
	Body struct {
		Player string `json:"player" required:"true" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n"`
	}
}

type friendshipResponse struct {
	// Status is 201 for a new request, 200 when it accepted theirs.
	Status int
	Body   struct {
		Friendship string `json:"friendship" enum:"requested,friends" doc:"requested, or friends when they had already asked."`
	}
}

type playerPath struct {
	Player string `path:"player" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n"`
}

type leaderboardRequest struct {
	Game  string `path:"game" maxLength:"64" example:"fixture.xo"`
	Scope string `query:"scope" example:"friends" doc:"friends, the only one in v1, and the default (scope: friends_only otherwise)."`
}

type leaderboardResponse struct {
	Body *friends.Board
}

func (h *Handler) registerFriends(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "friends", Method: http.MethodGet, Path: "/v1/friends",
		Summary: "My friends",
		Description: "Every friend with their presence: in a room (which, so the app can say where; " +
			"joining still takes its code or an invite), online or offline. In a room first, then " +
			"online, then offline, each by nickname.",
		Tags: []string{"friends"}, Metadata: session,
	}, h.friends)

	huma.Register(api, huma.Operation{
		OperationID: "friend-requests", Method: http.MethodGet, Path: "/v1/friends/requests",
		Summary:     "My requests",
		Description: "The requests made to the player (incoming) and their own (outgoing), newest first.",
		Tags:        []string{"friends"}, Metadata: session,
	}, h.friendRequests)

	huma.Register(api, huma.Operation{
		OperationID: "friend-request", Method: http.MethodPost, Path: "/v1/friends/requests",
		Summary: "Ask to be friends",
		Description: "201 requested, and a friend_request in their inbox; 200 friends when they had " +
			"already asked the caller, which accepts it. player: is_you; not_found (no such player, " +
			"or they blocked the caller); blocked (the caller blocked them); already_friends; " +
			"request_pending.",
		Tags: []string{"friends"}, Metadata: session, DefaultStatus: http.StatusCreated,
	}, h.friendRequest)

	huma.Register(api, huma.Operation{
		OperationID: "friend-request-cancel", Method: http.MethodDelete, Path: "/v1/friends/requests/{player}",
		Summary:     "Take back my request",
		Description: "The request goes, and its notification with it. request_not_found when there is none.",
		Tags:        []string{"friends"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.cancelRequest)

	huma.Register(api, huma.Operation{
		OperationID: "friend-accept", Method: http.MethodPost, Path: "/v1/friends/requests/{player}/accept",
		Summary:     "Accept a request",
		Description: "Friends, and a friend_accepted in the asker's inbox. request_not_found when they have not asked.",
		Tags:        []string{"friends"}, Metadata: session,
	}, h.acceptRequest)

	huma.Register(api, huma.Operation{
		OperationID: "friend-decline", Method: http.MethodPost, Path: "/v1/friends/requests/{player}/decline",
		Summary:     "Decline a request",
		Description: "The request goes, and its notification with it; the asker is not told. request_not_found when they have not asked.",
		Tags:        []string{"friends"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.declineRequest)

	huma.Register(api, huma.Operation{
		OperationID: "friend-remove", Method: http.MethodDelete, Path: "/v1/friends/{player}",
		Summary:     "Stop being friends",
		Description: "Nobody is told. not_friends when they were not.",
		Tags:        []string{"friends"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.removeFriend)

	huma.Register(api, huma.Operation{
		OperationID: "player-block", Method: http.MethodPost, Path: "/v1/players/{player}/block",
		Summary: "Block a player",
		Description: "Ends any friendship or request between the two, and keeps them from asking again, " +
			"inviting each other, or meeting in a friends-only room. Nobody is told. Blocking twice is " +
			"blocking once.",
		Tags: []string{"friends"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.block)

	huma.Register(api, huma.Operation{
		OperationID: "player-unblock", Method: http.MethodDelete, Path: "/v1/players/{player}/block",
		Summary:     "Lift a block",
		Description: "Restores nothing: a friendship the block ended stays ended.",
		Tags:        []string{"friends"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.unblock)

	huma.Register(api, huma.Operation{
		OperationID: "game-leaderboard", Method: http.MethodGet, Path: "/v1/games/{game}/leaderboard",
		Summary: "A game's leaderboard among friends",
		Description: "The caller and their friends who have results in the game: wins, then the best " +
			"score, then plays, each highest first; ties share a rank.",
		Tags: []string{"friends"}, Metadata: session,
	}, h.leaderboard)
}

func (h *Handler) friends(ctx context.Context, _ *struct{}) (*friendsResponse, error) {
	list, err := h.Friends.Friends(ctx, caller.FromContext(ctx).Subject)
	if err != nil {
		return nil, err
	}
	out := &friendsResponse{}
	out.Body.Friends = list
	return out, nil
}

func (h *Handler) friendRequests(ctx context.Context, _ *struct{}) (*friendRequestsResponse, error) {
	list, err := h.Friends.Requests(ctx, caller.FromContext(ctx).Subject)
	if err != nil {
		return nil, err
	}
	return &friendRequestsResponse{Body: list}, nil
}

func (h *Handler) friendRequest(ctx context.Context, in *friendRequestBody) (*friendshipResponse, error) {
	standing, created, err := h.Friends.Request(ctx, caller.FromContext(ctx).Subject, in.Body.Player)
	if err != nil {
		return nil, err
	}
	out := &friendshipResponse{Status: http.StatusOK}
	if created {
		out.Status = http.StatusCreated
	}
	out.Body.Friendship = standing
	return out, nil
}

func (h *Handler) cancelRequest(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.Friends.Cancel(ctx, caller.FromContext(ctx).Subject, in.Player)
}

func (h *Handler) acceptRequest(ctx context.Context, in *playerPath) (*friendshipResponse, error) {
	if err := h.Friends.Accept(ctx, caller.FromContext(ctx).Subject, in.Player); err != nil {
		return nil, err
	}
	out := &friendshipResponse{Status: http.StatusOK}
	out.Body.Friendship = friends.Friends
	return out, nil
}

func (h *Handler) declineRequest(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.Friends.Decline(ctx, caller.FromContext(ctx).Subject, in.Player)
}

func (h *Handler) removeFriend(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.Friends.Remove(ctx, caller.FromContext(ctx).Subject, in.Player)
}

func (h *Handler) block(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.Friends.Block(ctx, caller.FromContext(ctx).Subject, in.Player)
}

func (h *Handler) unblock(ctx context.Context, in *playerPath) (*noContent, error) {
	return nil, h.Friends.Unblock(ctx, caller.FromContext(ctx).Subject, in.Player)
}

func (h *Handler) leaderboard(ctx context.Context, in *leaderboardRequest) (*leaderboardResponse, error) {
	board, err := h.Friends.Leaderboard(ctx, caller.FromContext(ctx).Subject, in.Game, in.Scope)
	if err != nil {
		return nil, err
	}
	return &leaderboardResponse{Body: board}, nil
}
