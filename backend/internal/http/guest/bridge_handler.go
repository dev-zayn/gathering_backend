package guest

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

type bridgePlayerJSON struct {
	ID        string `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname  string `json:"nickname,omitempty" example:"زين"`
	Handle    string `json:"handle,omitempty" example:"zayn_4821"`
	Avatar    string `json:"avatar,omitempty" doc:"A bundled avatar's id, when the player picked one."`
	AvatarURL string `json:"avatar_url,omitempty" doc:"A link that expires, when the player uploaded a picture."`
}

type bridgeRoomJSON struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Status string `json:"status"`
	Role   string `json:"role"`
	Seat   int    `json:"seat"`
}

type contextResponse struct {
	Body struct {
		Player bridgePlayerJSON `json:"player"`
		Room   *bridgeRoomJSON  `json:"room" doc:"The room the game was opened into. Null until the rooms arrive, and null for a game opened outside one."`
		Scope  []string         `json:"scope"`
		Live   bool             `json:"live" doc:"A person was present when this token was minted. A renewal cannot invent one."`
	}
}

func (h *Handler) registerBridge(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "bridge-context",
		Method:      http.MethodGet,
		Path:        "/v1/bridge/context",
		Summary:     "What this token means",
		Description: "Who is playing, in which room, with which scopes. One call rather " +
			"than two, because \"who am I talking to\" is one question. Fetch it on every " +
			"launch rather than caching it — a player who changes their nickname should " +
			"not have to wait for a webhook. Filtered by the granted scopes: without " +
			"identity:read the player is an id and nothing else. The phone number is " +
			"never here, under any scope.",
		Tags:     []string{"bridge"},
		Metadata: map[string]any{authn.ScopeKey: caller.ScopeGame},
	}, h.context)
}

func (h *Handler) context(ctx context.Context, _ *struct{}) (*contextResponse, error) {
	who, err := h.Bridge.Who(ctx, guest(ctx))
	if err != nil {
		return nil, err
	}

	out := &contextResponse{}
	out.Body.Player = bridgePlayerJSON{
		ID: who.Player.ID, Nickname: who.Player.Nickname, Handle: who.Player.Handle,
		Avatar: who.Player.Avatar, AvatarURL: who.Player.AvatarURL,
	}
	if who.Room != nil {
		out.Body.Room = &bridgeRoomJSON{
			ID: who.Room.ID, Code: who.Room.Code, Status: who.Room.Status, Role: who.Room.Role, Seat: who.Room.Seat,
		}
	}

	// Never null: a game that was granted nothing still reads a list.
	out.Body.Scope = who.Scopes
	if out.Body.Scope == nil {
		out.Body.Scope = []string{}
	}
	out.Body.Live = who.Live
	return out, nil
}

// guest reads the game token once. The audience is the game: a token names
// exactly one, so a game cannot speak for another.
func guest(ctx context.Context) guests.Caller {
	c := caller.FromContext(ctx)

	return guests.Caller{
		PlayerID: c.Subject,
		GameID:   c.Game(),
		RoomID:   c.Room,
		Scopes:   strings.Fields(c.Scope),
		Live:     c.Live,
	}
}
