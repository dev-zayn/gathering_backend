package guest

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
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
	ID      string             `json:"id" example:"room_7f3k9d2m8q1x4b6n"`
	Code    string             `json:"code" example:"K7M2QX"`
	Status  string             `json:"status" enum:"lobby,playing,ended"`
	Round   int                `json:"round" doc:"How many rounds have started: the round being played, or the one just finished; 0 before the first."`
	Role    string             `json:"role" enum:"host,player,spectator"`
	Seat    int                `json:"seat"`
	Members []bridgeMemberJSON `json:"members" doc:"The current members, by seat."`

	Tournament *rooms.MatchBrief `json:"tournament" doc:"The tournament match the room plays (docs/TOURNAMENTS.md), its tally by player id; null for any other room."`
}

type bridgeMemberJSON struct {
	ID       string `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname string `json:"nickname,omitempty" doc:"With identity:read only."`
	Role     string `json:"role" enum:"host,player,spectator"`
	Seat     int    `json:"seat"`
}

type contextResponse struct {
	Body struct {
		Player bridgePlayerJSON `json:"player"`
		Room   *bridgeRoomJSON  `json:"room" doc:"The room the game was opened into, while the player is still a current member of it. Null for a game opened outside one."`
		Scope  []string         `json:"scope"`
		Live   bool             `json:"live" doc:"A person was present when this token was minted. A renewal cannot invent one."`
	}
}

type bridgeScoreJSON struct {
	Player string  `json:"player" required:"true" maxLength:"24" example:"ply_7f3k9d2m8q1x4b6n"`
	Score  float64 `json:"score" required:"true"`
	Rank   *int    `json:"rank,omitempty" doc:"At least 1. Leave it out and it is computed by score, highest first, ties sharing a rank (1, 1, 3)."`
}

type resultsRequest struct {
	Body struct {
		Room   string            `json:"room" required:"true" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n"`
		Round  *int              `json:"round,omitempty" example:"2" doc:"Which round, from 1 to the room's round (round: unknown). Leave it out for the room's round: the one playing, or the one just finished."`
		Scores []bridgeScoreJSON `json:"scores" required:"true" maxItems:"256" doc:"Each for a current or former member of the room, once."`
	}
}

type bridgeResultJSON struct {
	Player      string  `json:"player" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname    string  `json:"nickname,omitempty" doc:"With identity:read only."`
	Avatar      string  `json:"avatar,omitempty" doc:"With identity:read only: a bundled avatar's id."`
	AvatarURL   string  `json:"avatar_url,omitempty" doc:"With identity:read only: a link that expires, when the player uploaded a picture."`
	Score       float64 `json:"score"`
	Rank        int     `json:"rank"`
	SubmittedBy string  `json:"submitted_by" enum:"host,game"`
}

type resultsResponse struct {
	Body struct {
		Round   int                `json:"round" doc:"The round written."`
		Results []bridgeResultJSON `json:"results" doc:"The round's results as they now stand: ranked, ties in seat order."`
	}
}

type notifyRequest struct {
	Body struct {
		Title          string `json:"title" required:"true" example:"دورك يا بطل" doc:"1 to 60 characters (title: 1_to_60)."`
		Body           string `json:"body,omitempty" doc:"Up to 160 characters (body: up_to_160)."`
		DeepLink       string `json:"deep_link,omitempty" example:"/match/42" doc:"A path inside the game, as the launch's path; the app opens the game there (deep_link: must_be_a_path)."`
		IdempotencyKey string `json:"idempotency_key,omitempty" example:"turn:42:7" doc:"Up to 64 (idempotency_key: up_to_64). A key already used for this player updates that notification, back at the top and unread, and answers 200 with its id."`
	}
}

type notifyResponse struct {
	// Status is 201 for a new notification, 200 for a key already used.
	Status int
	Body   struct {
		ID string `json:"id" example:"ntf_7f3k9d2m8q1x4b6n"`
	}
}

func (h *Handler) registerBridge(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "bridge-results",
		Method:      http.MethodPost,
		Path:        "/v1/bridge/results",
		Summary:     "Write a round's results",
		Description: "The game's own server says how a round of its game came out: round, or the room's " +
			"round when it is left out (the one playing, or the one just finished). Its word is the " +
			"stronger: the round's results are replaced whole, and the host's finish or end leaves " +
			"them be. The room must have started a round (room_not_started before the first), be " +
			"this game's (room: another_game), and the token's player must have been in it " +
			"(not_member); round is from 1 to the room's round (round: unknown), and each score is " +
			"for a current or former member, once, with a rank of at least 1 when one is given. A " +
			"round under way keeps playing: the host still finishes it. Needs results:write; names " +
			"and pictures come back with identity:read only.",
		Tags: []string{"bridge"},
		Metadata: map[string]any{
			authn.ScopeKey: caller.ScopeGame,
			authn.PermKey:  guests.ScopeResultsWrite,
		},
	}, h.results)

	huma.Register(api, huma.Operation{
		OperationID: "bridge-notify",
		Method:      http.MethodPost,
		Path:        "/v1/bridge/notifications",
		Summary:     "Put a notification in the player's inbox",
		Description: "The game's own server tells the player whose token it holds something: a turn " +
			"to take, a match that ended. Kind game, from this game, opening it at deep_link. At " +
			"most 10 a player an hour for each game, every call counting, a reused key included " +
			"(too_many_requests with retry_after). Needs notify:send.",
		Tags: []string{"bridge"},
		Metadata: map[string]any{
			authn.ScopeKey: caller.ScopeGame,
			authn.PermKey:  guests.ScopeNotifySend,
		},
		DefaultStatus: http.StatusCreated,
	}, h.notify)

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
		room := &bridgeRoomJSON{
			ID: who.Room.ID, Code: who.Room.Code, Status: who.Room.Status, Round: who.Room.Round,
			Role: who.Room.Role, Seat: who.Room.Seat,
			Members:    make([]bridgeMemberJSON, 0, len(who.Room.Members)),
			Tournament: who.Room.Tournament,
		}
		for _, m := range who.Room.Members {
			room.Members = append(room.Members, bridgeMemberJSON{ID: m.ID, Nickname: m.Nickname, Role: m.Role, Seat: m.Seat})
		}
		out.Body.Room = room
	}

	// Never null: a game that was granted nothing still reads a list.
	out.Body.Scope = who.Scopes
	if out.Body.Scope == nil {
		out.Body.Scope = []string{}
	}
	out.Body.Live = who.Live
	return out, nil
}

func (h *Handler) results(ctx context.Context, in *resultsRequest) (*resultsResponse, error) {
	scores := make([]rooms.ScoreIn, 0, len(in.Body.Scores))
	for _, s := range in.Body.Scores {
		scores = append(scores, rooms.ScoreIn{Player: s.Player, Score: s.Score, Rank: s.Rank})
	}
	written, err := h.Bridge.Results(ctx, guest(ctx), in.Body.Room, in.Body.Round, scores)
	if err != nil {
		return nil, err
	}

	out := &resultsResponse{}
	out.Body.Round = written.Round
	out.Body.Results = make([]bridgeResultJSON, 0, len(written.Lines))
	for _, l := range written.Lines {
		out.Body.Results = append(out.Body.Results, bridgeResultJSON{
			Player: l.Player, Nickname: l.Nickname, Avatar: l.Avatar, AvatarURL: l.AvatarURL,
			Score: l.Score, Rank: l.Rank, SubmittedBy: l.SubmittedBy,
		})
	}
	return out, nil
}

func (h *Handler) notify(ctx context.Context, in *notifyRequest) (*notifyResponse, error) {
	raised, err := h.Bridge.Notify(ctx, guest(ctx), guests.Notice{
		Title: in.Body.Title, Body: in.Body.Body, DeepLink: in.Body.DeepLink, Key: in.Body.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	out := &notifyResponse{Status: http.StatusOK}
	if raised.Created {
		out.Status = http.StatusCreated
	}
	out.Body.ID = raised.ID
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
