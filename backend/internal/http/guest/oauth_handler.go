package guest

import (
	"context"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/guests"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// The token flow a game's server goes through: a code that survives one
// redirect, traded for a credential of its own.
type authorizeRequest struct {
	ClientID        string `query:"client_id" required:"true" example:"fixture.xo"`
	RedirectURI     string `query:"redirect_uri" required:"true"`
	ResponseType    string `query:"response_type" required:"true" enum:"code"`
	Challenge       string `query:"code_challenge" required:"true"`
	ChallengeMethod string `query:"code_challenge_method" required:"true" enum:"S256"`
	State           string `query:"state"`

	// Resource is RFC 8707. The app does not send it, so it is optional.
	Resource string `query:"resource"`
}

type redirectResponse struct {
	Location string `header:"Location"`
}

type tokenRequest struct {
	RawBody []byte
}

type tokenResponse struct {
	Body struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type" example:"Bearer"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token,omitempty" doc:"Only when a code was spent. A renewal keeps the one it used."`
		Scope        string `json:"scope"`
	}
}

type userinfoResponse struct {
	Body struct {
		Sub      string `json:"sub"`
		Name     string `json:"name,omitempty" doc:"The nickname, with identity:read."`
		Username string `json:"preferred_username,omitempty" doc:"The handle, with identity:read."`
		Picture  string `json:"picture,omitempty" doc:"A link to an uploaded avatar, with identity:read."`
		Game     string `json:"game" doc:"The client this token was minted for."`
	}
}

func (h *Handler) registerOAuth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "oauth-authorize",
		Method:      http.MethodGet,
		Path:        "/oauth/authorize",
		Summary:     "Authorization code, with PKCE",
		Description: "For a player signing in to a studio's own site with their Gathering " +
			"session. No consent screen: a published game is the player's to play, and " +
			"signing in here counts as their first open.",
		Tags:          []string{"launch"},
		DefaultStatus: http.StatusFound,
		Metadata:      map[string]any{authn.ScopeKey: caller.ScopeSession},
	}, h.authorize)

	huma.Register(api, huma.Operation{
		OperationID: "oauth-token",
		Method:      http.MethodPost,
		Path:        "/oauth/token",
		Summary:     "Spend a code, or renew",
		Description: "Form-encoded, as the standard says. Two grants: authorization_code and " +
			"refresh_token. The refresh token lives exactly as long as the game stays on the " +
			"player's shelf — removing it is the revocation, and there is no separate call.",
		Tags: []string{"launch"},
	}, h.token)

	huma.Register(api, huma.Operation{
		OperationID: "oauth-userinfo",
		Method:      http.MethodGet,
		Path:        "/oauth/userinfo",
		Summary:     "Standard OIDC claims",
		Description: "So an off-the-shelf client library works end to end without " +
			"special-casing us. A thin alias for GET /v1/bridge/context — the same " +
			"answer under the standard names, filtered by the same scopes.",
		Tags:     []string{"launch"},
		Metadata: map[string]any{authn.ScopeKey: caller.ScopeGame},
	}, h.userinfo)
}

func (h *Handler) authorize(ctx context.Context, in *authorizeRequest) (*redirectResponse, error) {
	res, err := h.Launch.Authorize(ctx, launcher(ctx), guests.AuthorizeRequest{
		ClientID:        in.ClientID,
		RedirectURI:     in.RedirectURI,
		ResponseType:    in.ResponseType,
		Challenge:       in.Challenge,
		ChallengeMethod: in.ChallengeMethod,
		State:           in.State,
		Resource:        in.Resource,
	})
	if err != nil {
		return nil, err
	}
	return &redirectResponse{Location: res.Redirect}, nil
}

// token is form-encoded because that is what every OAuth library sends. The
// one endpoint here that does not look like the rest of the API.
func (h *Handler) token(ctx context.Context, in *tokenRequest) (*tokenResponse, error) {
	form, err := url.ParseQuery(string(in.RawBody))
	if err != nil {
		return nil, errors.BadRequest("invalid_request")
	}

	clientID := form.Get("client_id")
	secret := form.Get("client_secret")

	var issued *guests.Issued
	switch form.Get("grant_type") {
	case "authorization_code":
		issued, err = h.Launch.Spend(ctx, clientID, secret,
			form.Get("code"), form.Get("code_verifier"), form.Get("redirect_uri"))
	case "refresh_token":
		issued, err = h.Launch.Renew(ctx, clientID, secret, form.Get("refresh_token"))
	default:
		return nil, errors.BadRequest("unsupported_grant_type")
	}
	if err != nil {
		return nil, err
	}

	out := &tokenResponse{}
	out.Body.AccessToken = issued.AccessToken
	out.Body.TokenType = "Bearer"
	out.Body.ExpiresIn = issued.ExpiresIn
	out.Body.RefreshToken = issued.RefreshToken
	out.Body.Scope = issued.Scope
	return out, nil
}

// userinfo is the bridge's answer wearing standard names, through the same
// rule so a client library cannot see more than the game was granted.
func (h *Handler) userinfo(ctx context.Context, _ *struct{}) (*userinfoResponse, error) {
	c := guest(ctx)
	who, err := h.Bridge.Who(ctx, c)
	if err != nil {
		return nil, err
	}

	out := &userinfoResponse{}
	out.Body.Sub = who.Player.ID
	out.Body.Name = who.Player.Nickname
	out.Body.Username = who.Player.Handle
	out.Body.Picture = who.Player.AvatarURL
	out.Body.Game = c.GameID
	return out, nil
}
