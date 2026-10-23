package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Rooms: making one, joining one by code, and everything the host does with
// it. Each change is also published to the room's sockets; the socket itself
// carries no commands. docs/ROOMS.md is the contract.

type roomResponse struct {
	Body *rooms.Room
}

type roomPath struct {
	Room string `path:"room" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n"`
}

type roomCreateRequest struct {
	Body struct {
		Game       string `json:"game" required:"true" maxLength:"64" example:"fixture.xo"`
		LAN        bool   `json:"lan,omitempty" doc:"Ask for a Wi-Fi relay. Refused for a game whose manifest says room.lan is false."`
		Visibility string `json:"visibility,omitempty" enum:"private,friends" doc:"private (the default: anybody with the code), or friends (the host's friends alone: friends_only for anybody else)."`
	}
}

type roomsMineResponse struct {
	Body struct {
		Rooms []rooms.Room `json:"rooms" doc:"The open room the player is in: none, or one."`
	}
}

type roomJoinRequest struct {
	Body struct {
		Code string `json:"code" required:"true" example:"K7M-2QX" doc:"Six symbols. Case, spaces and dashes do not count."`
		Game string `json:"game,omitempty" maxLength:"64" example:"fixture.xo" doc:"The game the caller expects, from a game's page outside a room: a code another game's room holds is another_game, for a member of that room too. Left out, the code may be any game's."`
	}
}

type roomPlayerRequest struct {
	Room string `path:"room" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n"`
	Body struct {
		Player string `json:"player" required:"true" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n"`
	}
}

type scoreJSON struct {
	Player string  `json:"player" required:"true" maxLength:"24"`
	Score  float64 `json:"score" required:"true"`
	Rank   *int    `json:"rank,omitempty" doc:"At least 1 (scores.N.rank: must_be_positive). Leave it out and it is computed by score, highest first, ties sharing a rank."`
}

// roomScoresRequest is a finish's body, and an end's: the scores, if any.
type roomScoresRequest struct {
	Room string `path:"room" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n"`
	Body *struct {
		Scores []scoreJSON `json:"scores,omitempty" maxItems:"256" doc:"Each for a current or former member."`
	}
}

type roomInviteRequest struct {
	Room string `path:"room" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n"`
	Body struct {
		Players []string `json:"players" required:"true" doc:"1 to 20 player ids (players: 1_to_20), each the caller's friend and not in the room."`
	}
}

type roomInviteResponse struct {
	Body struct {
		Invited []string `json:"invited" doc:"The players invited, each once."`
	}
}

type roomTicketResponse struct {
	Body struct {
		Token     string `json:"token" doc:"A JWT: sub the player, aud the game, room, role, seat, round. Verify it against /.well-known/jwks.json."`
		ExpiresIn int    `json:"expires_in" example:"600" doc:"Seconds."`
	}
}

func (h *Handler) registerRooms(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "room-create", Method: http.MethodPost, Path: "/v1/rooms",
		Summary: "Make a room",
		Description: "A room for a published game, with the caller as its host in seat 0 and a " +
			"six-symbol code to share. Its shape is the game's manifest room block, copied. " +
			"in_another_room, naming that room, for a player still in another open one.",
		Tags: []string{"rooms"}, Metadata: session, DefaultStatus: http.StatusCreated,
	}, h.createRoom)

	huma.Register(api, huma.Operation{
		OperationID: "rooms-mine", Method: http.MethodGet, Path: "/v1/rooms/mine",
		Summary:     "The room I am in",
		Description: "The open room the player is a current member of: none, or one. Read it on launch to go back to a room.",
		Tags:        []string{"rooms"}, Metadata: session,
	}, h.myRooms)

	huma.Register(api, huma.Operation{
		OperationID: "room", Method: http.MethodGet, Path: "/v1/rooms/{room}",
		Summary: "One room",
		Description: "For anybody who was ever in it; not_found for everybody else. you is null " +
			"for a caller who has left. The app reads it before every reconnect.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.room)

	huma.Register(api, huma.Operation{
		OperationID: "room-join", Method: http.MethodPost, Path: "/v1/rooms/join",
		Summary: "Join a room by code",
		Description: "Case, spaces and dashes do not count. another_game when the body names a game and " +
			"the code is another game's room, right after the code is found and for a member too. " +
			"A current member gets the room back. " +
			"room_full, room_started (unless the game allows a rejoin and the player stepped out), " +
			"room_closed, kicked (for good), friends_only (a friends-only room, and the caller not " +
			"the host's friend), in_another_room.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.joinRoom)

	huma.Register(api, huma.Operation{
		OperationID: "room-leave", Method: http.MethodPost, Path: "/v1/rooms/{room}/leave",
		Summary: "Leave a room",
		Description: "Frees the seat and closes the leaver's socket. A host leaving hands the room " +
			"to the member in the lowest seat; the last member leaving ends it.",
		Tags: []string{"rooms"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.leaveRoom)

	huma.Register(api, huma.Operation{
		OperationID: "room-kick", Method: http.MethodPost, Path: "/v1/rooms/{room}/kick",
		Summary:     "Remove a member",
		Description: "The host's, never of themselves. The kicked player can never rejoin this room.",
		Tags:        []string{"rooms"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.kick)

	huma.Register(api, huma.Operation{
		OperationID: "room-start", Method: http.MethodPost, Path: "/v1/rooms/{room}/start",
		Summary: "Start a round",
		Description: "The host's, from the lobby, with at least the game's minimum in the room: the first " +
			"round or the next. round counts on, the shared state starts again at version 0, and every " +
			"member hears started with the round.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.start)

	huma.Register(api, huma.Operation{
		OperationID: "room-finish", Method: http.MethodPost, Path: "/v1/rooms/{room}/finish",
		Summary: "Finish the round",
		Description: "The host's, during a round, with the scores if there are any: they are the round's " +
			"results, unless the game's server wrote this round's first. The room goes back to its lobby " +
			"with the same code, members and sockets, and every member hears finished with the round's " +
			"ranked results; no socket closes. room_not_started in the lobby.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.finish)

	huma.Register(api, huma.Operation{
		OperationID: "room-end", Method: http.MethodPost, Path: "/v1/rooms/{room}/end",
		Summary: "Close the room",
		Description: "The host's, for good. During a round it finishes that round too, with the scores if " +
			"there are any; in the lobby it takes none (scores: not_playing) and carries the latest " +
			"round's results. Every member hears ended with the round and its results, and every socket closes.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.end)

	huma.Register(api, huma.Operation{
		OperationID: "room-host", Method: http.MethodPost, Path: "/v1/rooms/{room}/host",
		Summary:     "Hand over the host",
		Description: "The host's, to a current member.",
		Tags:        []string{"rooms"}, Metadata: session,
	}, h.handOver)

	huma.Register(api, huma.Operation{
		OperationID: "room-invite", Method: http.MethodPost, Path: "/v1/rooms/{room}/invites",
		Summary: "Invite friends",
		Description: "A current member of an open room asks friends to it: each gets a room_invite " +
			"whose link, /r/{code}, joins by the room's code. It holds no seat. players.N: " +
			"already_member or not_friends (a block either way reads the same), every problem at " +
			"once and nobody invited unless all pass.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.invite)

	huma.Register(api, huma.Operation{
		OperationID: "room-ticket", Method: http.MethodGet, Path: "/v1/rooms/{room}/ticket",
		Summary: "A room ticket",
		Description: "A short-lived JWT naming the player, the room, their role and seat, and the room's " +
			"round, for the game's own server to verify against /.well-known/jwks.json. It opens " +
			"nothing on this API.",
		Tags: []string{"rooms"}, Metadata: session,
	}, h.ticket)
}

func (h *Handler) createRoom(ctx context.Context, in *roomCreateRequest) (*roomResponse, error) {
	r, err := h.Rooms.Create(ctx, caller.FromContext(ctx).Subject, rooms.CreateRequest{
		Game: in.Body.Game, LAN: in.Body.LAN, Visibility: in.Body.Visibility,
	})
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

func (h *Handler) myRooms(ctx context.Context, _ *struct{}) (*roomsMineResponse, error) {
	list, err := h.Rooms.Mine(ctx, caller.FromContext(ctx).Subject)
	if err != nil {
		return nil, err
	}
	out := &roomsMineResponse{}
	out.Body.Rooms = list
	return out, nil
}

func (h *Handler) room(ctx context.Context, in *roomPath) (*roomResponse, error) {
	r, err := h.Rooms.Get(ctx, caller.FromContext(ctx).Subject, in.Room)
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

func (h *Handler) joinRoom(ctx context.Context, in *roomJoinRequest) (*roomResponse, error) {
	r, err := h.Rooms.Join(ctx, caller.FromContext(ctx).Subject, in.Body.Code, in.Body.Game)
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

func (h *Handler) leaveRoom(ctx context.Context, in *roomPath) (*noContent, error) {
	return nil, h.Rooms.Leave(ctx, caller.FromContext(ctx).Subject, in.Room)
}

func (h *Handler) kick(ctx context.Context, in *roomPlayerRequest) (*noContent, error) {
	return nil, h.Rooms.Kick(ctx, caller.FromContext(ctx).Subject, in.Room, in.Body.Player)
}

func (h *Handler) start(ctx context.Context, in *roomPath) (*roomResponse, error) {
	r, err := h.Rooms.Start(ctx, caller.FromContext(ctx).Subject, in.Room)
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

func (h *Handler) finish(ctx context.Context, in *roomScoresRequest) (*roomResponse, error) {
	r, err := h.Rooms.Finish(ctx, caller.FromContext(ctx).Subject, in.Room, in.scores())
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

func (h *Handler) end(ctx context.Context, in *roomScoresRequest) (*roomResponse, error) {
	r, err := h.Rooms.End(ctx, caller.FromContext(ctx).Subject, in.Room, in.scores())
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

// scores is the body's scores as the rules take them; none without a body.
func (in *roomScoresRequest) scores() []rooms.ScoreIn {
	var out []rooms.ScoreIn
	if in.Body != nil {
		for _, s := range in.Body.Scores {
			out = append(out, rooms.ScoreIn{Player: s.Player, Score: s.Score, Rank: s.Rank})
		}
	}
	return out
}

func (h *Handler) handOver(ctx context.Context, in *roomPlayerRequest) (*roomResponse, error) {
	r, err := h.Rooms.Host(ctx, caller.FromContext(ctx).Subject, in.Room, in.Body.Player)
	if err != nil {
		return nil, err
	}
	return &roomResponse{Body: r}, nil
}

func (h *Handler) invite(ctx context.Context, in *roomInviteRequest) (*roomInviteResponse, error) {
	invited, err := h.Rooms.Invite(ctx, caller.FromContext(ctx).Subject, in.Room, in.Body.Players)
	if err != nil {
		return nil, err
	}
	out := &roomInviteResponse{}
	out.Body.Invited = invited
	return out, nil
}

func (h *Handler) ticket(ctx context.Context, in *roomPath) (*roomTicketResponse, error) {
	t, err := h.Rooms.Ticket(ctx, caller.FromContext(ctx).Subject, in.Room)
	if err != nil {
		return nil, err
	}
	out := &roomTicketResponse{}
	out.Body.Token, out.Body.ExpiresIn = t.Token, t.ExpiresIn
	return out, nil
}
