package guest

import (
	"context"
	_ "embed"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// What a client reads before it has anything: RFC 9728, RFC 7517 and the OIDC
// spelling. No logic — every answer is built out of configuration.
type jwksResponse struct {
	Body struct {
		Keys []token.JWK `json:"keys"`
	}
}

// resourceResponse is RFC 9728, reached from the WWW-Authenticate header on a
// 401. It is where a client learns which authorization server to talk to.
type resourceResponse struct {
	Body struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		JWKSURI              string   `json:"jwks_uri"`
		ScopesSupported      []string `json:"scopes_supported"`
		BearerMethods        []string `json:"bearer_methods_supported"`
		ResourceName         string   `json:"resource_name"`
		ResourceDocs         string   `json:"resource_documentation,omitempty"`
	}
}

type discoveryResponse struct {
	Body struct {
		Issuer                string   `json:"issuer"`
		JWKSURI               string   `json:"jwks_uri"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		UserinfoEndpoint      string   `json:"userinfo_endpoint"`
		ResponseTypes         []string `json:"response_types_supported"`
		GrantTypes            []string `json:"grant_types_supported"`
		SubjectTypes          []string `json:"subject_types_supported"`
		SigningAlgs           []string `json:"id_token_signing_alg_values_supported"`
		CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
		ScopesSupported       []string `json:"scopes_supported"`
	}
}

func (h *Handler) registerWellKnown(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "jwks",
		Method:      http.MethodGet,
		Path:        "/.well-known/jwks.json",
		Summary:     "Our public signing keys",
		Description: "So a game's server verifies our tokens — and a room ticket — locally " +
			"instead of calling us per request. The private half never leaves the api.",
		Tags: []string{"well-known"},
	}, h.jwks)

	huma.Register(api, huma.Operation{
		OperationID: "oauth-protected-resource",
		Method:      http.MethodGet,
		Path:        "/.well-known/oauth-protected-resource",
		Summary:     "What this resource is and who guards it",
		Description: "RFC 9728. A client is sent here by the WWW-Authenticate header on a " +
			"401, and reads authorization_servers to find where to get a token.",
		Tags: []string{"well-known"},
	}, h.resource)

	huma.Register(api, huma.Operation{
		OperationID: "openid-configuration",
		Method:      http.MethodGet,
		Path:        "/.well-known/openid-configuration",
		Summary:     "Discovery",
		Description: "So a game's server integrates with a standard OIDC library. The " +
			"three /oauth endpoints named here arrive with the games milestone.",
		Tags: []string{"well-known"},
	}, h.discovery)
}

func (h *Handler) jwks(_ context.Context, _ *struct{}) (*jwksResponse, error) {
	out := &jwksResponse{}
	out.Body.Keys = h.Signer.JWKS()
	return out, nil
}

// resource is the protected-resource document for the whole API.
func (h *Handler) resource(_ context.Context, _ *struct{}) (*resourceResponse, error) {
	out := &resourceResponse{}
	out.Body.Resource = h.PublicURL

	// We are our own authorization server. Naming it rather than assuming it is
	// the point of this document, and one day it may not be us.
	out.Body.AuthorizationServers = []string{h.Signer.Issuer()}
	out.Body.JWKSURI = h.PublicURL + "/.well-known/jwks.json"

	// The least a token needs to be worth having. Anything more is in the
	// manifest and inherited, not asked for.
	out.Body.ScopesSupported = []string{"identity:read", "friends:read"}
	out.Body.BearerMethods = []string{"header"}
	out.Body.ResourceName = "Gathering"
	out.Body.ResourceDocs = h.DocsURL
	return out, nil
}

func (h *Handler) discovery(_ context.Context, _ *struct{}) (*discoveryResponse, error) {
	out := &discoveryResponse{}
	out.Body.Issuer = h.Signer.Issuer()
	out.Body.JWKSURI = h.PublicURL + "/.well-known/jwks.json"
	out.Body.AuthorizationEndpoint = h.PublicURL + "/oauth/authorize"
	out.Body.TokenEndpoint = h.PublicURL + "/oauth/token"
	out.Body.UserinfoEndpoint = h.PublicURL + "/oauth/userinfo"
	out.Body.ResponseTypes = []string{"code"}
	out.Body.GrantTypes = []string{"authorization_code", "refresh_token"}
	out.Body.SubjectTypes = []string{"public"}
	out.Body.SigningAlgs = []string{"ES256"}

	// PKCE only. A game's code is spent by its backend, and a public client
	// without PKCE is a code anybody who sees the redirect can spend.
	out.Body.CodeChallengeMethods = []string{"S256"}
	out.Body.ScopesSupported = []string{"identity:read", "friends:read", "notify:send", "results:write"}
	return out, nil
}
