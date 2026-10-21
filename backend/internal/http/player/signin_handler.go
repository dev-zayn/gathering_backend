package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

type phoneRequest struct {
	Body struct {
		Phone string `json:"phone" required:"true" minLength:"7" maxLength:"20" example:"07701234567" doc:"Iraqi mobile number. Local, international and 00-prefixed forms all land on the same number."`
	}
}

type ticketResponse struct {
	Body struct {
		Scope       string `json:"scope" example:"otp" doc:"What the token is for, not what it may read."`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in" example:"300" doc:"Seconds."`
	}
}

func (h *Handler) registerSignin(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-signin",
		Method:      http.MethodPost,
		Path:        "/v1/auth/signin",
		Summary:     "Send a code",
		Description: "Sends a code to the number and answers with a short-lived otp token. " +
			"The answer is the same whether or not the number is known — telling a stranger " +
			"which numbers are registered is not something sign-in should do.",
		Tags: []string{"auth"},
	}, h.signin)

	huma.Register(api, huma.Operation{
		OperationID: "auth-sso",
		Method:      http.MethodPost,
		Path:        "/v1/auth/sso/{provider}",
		Summary:     "Sign in with a provider",
		Description: "The seam for Google and Apple sign-in. The route exists so the app can " +
			"wire the buttons; until a provider ships it answers not_supported.",
		Tags: []string{"auth"},
	}, h.sso)
}

func (h *Handler) signin(ctx context.Context, in *phoneRequest) (*ticketResponse, error) {
	t, err := h.SignIn.Start(ctx, in.Body.Phone)
	if err != nil {
		return nil, err
	}

	out := &ticketResponse{}
	out.Body.Scope = t.Scope
	out.Body.AccessToken = t.AccessToken
	out.Body.ExpiresIn = t.ExpiresIn
	return out, nil
}

type ssoRequest struct {
	Provider string `path:"provider" enum:"google,apple"`
	Body     struct {
		Token string `json:"token" required:"true" doc:"The provider's id token."`
	}
}

func (h *Handler) sso(_ context.Context, _ *ssoRequest) (*ticketResponse, error) {
	return nil, errors.New(http.StatusNotImplemented, "not_supported")
}

type codeRequest struct {
	Body struct {
		Code string `json:"code" required:"true" pattern:"^[0-9]{6}$" example:"418302" doc:"The six digits from the message. Text, not a number — leading zeros count."`
	}
}

type verifyResponse struct {
	Body struct {
		Scope        string  `json:"scope" enum:"session,signup"`
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token,omitempty"`
		ExpiresIn    int     `json:"expires_in"`
		Player       *meJSON `json:"player,omitempty" doc:"Present with a session; absent with a signup token."`
	}
}

func (h *Handler) registerVerify(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-otp-verify",
		Method:      http.MethodPost,
		Path:        "/v1/auth/otp/verify",
		Summary:     "Check the code",
		Description: "A known number gets a session. One nobody has registered gets a signup " +
			"token and nothing is written. Dies after five wrong codes, not just on expiry.",
		Tags:     []string{"auth"},
		Metadata: map[string]any{authn.ScopeKey: caller.ScopeOTP},
	}, h.verify)

	huma.Register(api, huma.Operation{
		OperationID: "auth-otp-resend",
		Method:      http.MethodPost,
		Path:        "/v1/auth/otp/resend",
		Summary:     "Send the code again",
		Description: "The same attempt with a new code. Capped per token, and it does not " +
			"forgive wrong guesses already made.",
		Tags:     []string{"auth"},
		Metadata: map[string]any{authn.ScopeKey: caller.ScopeOTP},
	}, h.resend)
}

func (h *Handler) verify(ctx context.Context, in *codeRequest) (*verifyResponse, error) {
	claims := caller.FromContext(ctx)

	v, err := h.Verify.Do(ctx, claims.Phone, in.Body.Code)
	if err != nil {
		return nil, err
	}

	out := &verifyResponse{}
	out.Body.Scope = v.Scope
	out.Body.AccessToken = v.AccessToken
	out.Body.ExpiresIn = v.ExpiresIn
	out.Body.RefreshToken = v.RefreshToken
	if v.Player != nil {
		m := me(v.Player)
		out.Body.Player = &m
	}
	return out, nil
}

func (h *Handler) resend(ctx context.Context, _ *struct{}) (*ticketResponse, error) {
	claims := caller.FromContext(ctx)

	t, err := h.SignIn.Resend(ctx, claims.Phone, claims.ID)
	if err != nil {
		return nil, err
	}

	out := &ticketResponse{}
	out.Body.Scope = t.Scope
	out.Body.AccessToken = t.AccessToken
	out.Body.ExpiresIn = t.ExpiresIn
	return out, nil
}

type signupRequest struct {
	Body struct {
		Nickname string `json:"nickname" required:"true" minLength:"2" maxLength:"20" example:"زين" doc:"What sits under the avatar in a seat."`
		Avatar   string `json:"avatar,omitempty" maxLength:"32" doc:"One of the bundled avatar ids. A photo is uploaded after, through PUT /v1/me/avatar."`
		Language string `json:"language,omitempty" enum:"ar,en" doc:"The app's language. Arabic by default."`
	}
}

type signupResponse struct {
	Body struct {
		Scope        string `json:"scope" example:"session"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Player       meJSON `json:"player"`
	}
}

func (h *Handler) registerSignup(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-signup",
		Method:      http.MethodPost,
		Path:        "/v1/auth/signup",
		Summary:     "Become a player, in one call",
		Description: "A nickname and an avatar: that is the whole sign-up. The phone comes from " +
			"the token, never the body, and the @name is made from the nickname. Answers " +
			"with a session, so the next screen is the app.",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusCreated,
		Metadata:      map[string]any{authn.ScopeKey: caller.ScopeSignup},
	}, h.signup)
}

func (h *Handler) signup(ctx context.Context, in *signupRequest) (*signupResponse, error) {
	claims := caller.FromContext(ctx)

	v, err := h.Signup.Do(ctx, biz.SignupRequest{
		Phone:    claims.Phone,
		Nickname: in.Body.Nickname,
		Avatar:   in.Body.Avatar,
		Language: in.Body.Language,
	})
	if err != nil {
		return nil, err
	}

	out := &signupResponse{}
	out.Body.Scope = v.Scope
	out.Body.AccessToken = v.AccessToken
	out.Body.RefreshToken = v.RefreshToken
	out.Body.ExpiresIn = v.ExpiresIn
	out.Body.Player = me(v.Player)
	return out, nil
}

type refreshRequest struct {
	Body struct {
		RefreshToken string `json:"refresh_token" required:"true" example:"rft_k4c35br6mqmfpmgf"`
	}
}

type refreshResponse struct {
	Body struct {
		Scope        string `json:"scope"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token" doc:"A new one. The token sent is now spent."`
		ExpiresIn    int    `json:"expires_in"`
	}
}

func (h *Handler) registerSessions(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-refresh",
		Method:      http.MethodPost,
		Path:        "/v1/auth/refresh",
		Summary:     "Rotate an expiring session",
		Description: "Refresh tokens are single-use, and replaying a spent one revokes the " +
			"whole chain — a stolen token is therefore worth using once, loudly, rather " +
			"than quietly forever.",
		Tags: []string{"auth"},
	}, h.refreshSession)

	huma.Register(api, huma.Operation{
		OperationID:   "auth-logout",
		Method:        http.MethodPost,
		Path:          "/v1/auth/logout",
		Summary:       "End this session",
		Description:   "Kills the record on the server. Dropping the token on the device would leave us still willing to refresh it.",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
		Metadata:      map[string]any{authn.ScopeKey: caller.ScopeSession},
	}, h.signOut)
}

func (h *Handler) refreshSession(ctx context.Context, in *refreshRequest) (*refreshResponse, error) {
	v, err := h.Refresh.Do(ctx, in.Body.RefreshToken)
	if err != nil {
		return nil, err
	}

	out := &refreshResponse{}
	out.Body.Scope = v.Scope
	out.Body.AccessToken = v.AccessToken
	out.Body.RefreshToken = v.RefreshToken
	out.Body.ExpiresIn = v.ExpiresIn
	return out, nil
}

func (h *Handler) signOut(ctx context.Context, _ *struct{}) (*noContent, error) {
	claims := caller.FromContext(ctx)
	if err := h.Refresh.Logout(ctx, claims.Session); err != nil {
		return nil, err
	}
	// Offline at once: the heartbeat that kept them online is this session's.
	if h.Presence != nil {
		_ = h.Presence.Clear(ctx, claims.Subject)
	}
	return nil, nil
}
