package guest

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// The tap inside the app: a one-time code on the game's entry, and how the
// frame should open.
type openRequest struct {
	Game string `path:"game" example:"fixture.xo"`
	Room string `query:"room" maxLength:"24" example:"room_7f3k9d2m8q1x4b6n" doc:"The room the game opens into, when it does: one the player is a current member of, still open, and this game's. It rides on the code into the game's token."`
	Path string `query:"path" maxLength:"512" example:"/board" doc:"Where inside the game to land: a notification's deep_link. A path on the game's own origin; anything else is refused."`
}

type openResponse struct {
	Body struct {
		URL         string `json:"url" doc:"The entry with ?code= appended, and &path= when one was given."`
		Mode        string `json:"mode" enum:"embedded"`
		Splash      bool   `json:"splash" doc:"Cover the frame until the page says it is ready."`
		Orientation string `json:"orientation" enum:"portrait,landscape"`
		ExpiresIn   int    `json:"expires_in" doc:"One minute, one use. It only has to survive a redirect."`
	}
}

func (h *Handler) registerLaunch(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "game-session",
		Method:      http.MethodPost,
		Path:        "/v1/games/{game}/session",
		Summary:     "Open a game",
		Description: "The first open of a game puts it on the player's shelf: the tile stays " +
			"from then on. Mints a one-time code and returns the URL to load in the frame. " +
			"The code is the only thing that touches the frame — the tokens are minted on " +
			"the far side, backend to backend. A path, when given, rides beside the code " +
			"so a notification lands the player on the right screen; a room, when given, " +
			"rides on the code into the game's token — not_member, room_closed, or " +
			"invalid_fields (room: another_game) when it cannot.",
		Tags:     []string{"launch"},
		Metadata: map[string]any{authn.ScopeKey: caller.ScopeSession},
	}, h.open)
}

func (h *Handler) open(ctx context.Context, in *openRequest) (*openResponse, error) {
	opened, err := h.Launch.Open(ctx, launcher(ctx), in.Game, in.Room, in.Path)
	if err != nil {
		return nil, err
	}

	out := &openResponse{}
	out.Body.URL = opened.URL
	out.Body.Mode = opened.Mode
	out.Body.Splash = opened.Splash
	out.Body.Orientation = opened.Orientation
	out.Body.ExpiresIn = opened.ExpiresIn
	return out, nil
}

// launcher is who is tapping, from the session token and nowhere else.
func launcher(ctx context.Context) guests.Launcher {
	c := caller.FromContext(ctx)
	return guests.Launcher{PlayerID: c.Subject, Live: c.Live}
}
