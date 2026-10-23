package admin

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// Rooms, as the console sees them: every room a page at a time, one room
// with who is in it, and the close. docs/CONSOLE.md, "Rooms", is the contract.

type adminRoomsRequest struct {
	Status string `query:"status" example:"open" doc:"open (the default: lobby and playing), lobby, playing, ended, or all (status: unknown)."`
	Game   string `query:"game" example:"fixture.xo" doc:"One game's rooms."`
	Cursor string `query:"cursor" doc:"next_cursor from the page before; leave it out for the first page (cursor: invalid)."`
	Limit  int    `query:"limit" default:"50" doc:"1 to 100 (limit: 1_to_100)."`
}

type adminRoomsResponse struct {
	Body *rooms.ConsoleRooms
}

type adminRoomPath struct {
	Room string `path:"room" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n"`
}

type adminRoomResponse struct {
	Body *rooms.ConsoleRoomDetail
}

func (h *Handler) registerRooms(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)}

	huma.Register(api, huma.Operation{
		OperationID: "admin-rooms", Method: http.MethodGet, Path: "/v1/admin/rooms",
		Summary: "Rooms",
		Description: "Newest first, a page at a time: hand next_cursor back as cursor until it is null. " +
			"live counts the open rooms by status across the platform, whatever the filters.",
		Tags: []string{"admin"}, Metadata: admin,
	}, h.rooms)

	huma.Register(api, huma.Operation{
		OperationID: "admin-room", Method: http.MethodGet, Path: "/v1/admin/rooms/{room}",
		Summary: "One room",
		Description: "Its members and who is connected, its room block, the latest round's results " +
			"and its last 50 events. not_found once the room row is gone, a day after its end.",
		Tags: []string{"admin"}, Metadata: admin,
	}, h.room)

	huma.Register(api, huma.Operation{
		OperationID: "admin-room-close", Method: http.MethodPost, Path: "/v1/admin/rooms/{room}/close",
		Summary: "Close a room for everybody in it",
		Description: "Ends an open room as its host's end would, a round under way included, with the " +
			"reason admin and nobody named: ended carries by null, and every socket closes with 4004. " +
			"room_closed for a room that has ended. Admins only; written to the log.",
		Tags: []string{"admin"}, Metadata: write,
	}, h.closeRoom)
}

func (h *Handler) rooms(ctx context.Context, in *adminRoomsRequest) (*adminRoomsResponse, error) {
	page, err := h.Rooms.List(ctx, rooms.ConsoleQuery{Status: in.Status, Game: in.Game, Cursor: in.Cursor, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	return &adminRoomsResponse{Body: page}, nil
}

func (h *Handler) room(ctx context.Context, in *adminRoomPath) (*adminRoomResponse, error) {
	r, err := h.Rooms.Detail(ctx, in.Room)
	if err != nil {
		return nil, err
	}
	return &adminRoomResponse{Body: r}, nil
}

func (h *Handler) closeRoom(ctx context.Context, in *adminRoomPath) (*adminRoomResponse, error) {
	r, err := h.Rooms.Close(ctx, caller.FromContext(ctx).Subject, in.Room)
	if err != nil {
		return nil, err
	}
	return &adminRoomResponse{Body: r}, nil
}
