// Package authn decides whether a request may reach a handler: is this token
// ours and live, and is it the kind, role and permission the route declares.
package authn

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// ScopeKey is what an operation puts in its metadata to say which kind of token
// opens it. An operation without it is public.
const ScopeKey = "scope"

// RoleKey narrows that further: the operation needs a particular role as well as
// the right kind of token: "host" on a room ticket; an admin's roles come from
// the identity provider, and no route narrows by them yet.
const RoleKey = "role"

// PermKey narrows a game's token the same way: one named permission out of what
// its manifest asks for. Here, so a missing scope always reads alike.
const PermKey = "permission"

// Resolver turns an Authorization header into who is calling, or the reason
// nobody is. A function, so this package never imports what mints tokens. A 4xx
// *errors.Error is an answer to send as it is, a 403 for a good token that holds
// nothing here; any other error means the answer could not be found.
type Resolver func(ctx context.Context, authorization string) (*caller.Session, string, error)

// Auth checks that whoever is calling may run this operation. public is the
// address every refusal names, so a client can find out where to get a token.
// A request with no Authorization header may carry the console's session in
// its cookie instead, from one of the cookie's origins only.
func Auth(api huma.API, public string, who Resolver, cookie Cookie) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		want, err := declared(ctx, ScopeKey)
		if err != nil {
			fail(api, ctx, err)
			return
		}
		if want == "" {
			next(ctx)
			return
		}

		metadata := public + "/.well-known/oauth-protected-resource"

		authorization := ctx.Header("Authorization")
		if authorization == "" {
			if v := cookie.Read(ctx.Header("Cookie")); v != "" {
				if !cookie.From(ctx.Header("Origin"), ctx.Method()) {
					refuse(api, ctx, metadata, apperr.Forbidden("bad_origin"))
					return
				}
				authorization = "Bearer " + v
			}
		}
		session, reason, err := who(ctx.Context(), authorization)
		// A refusal decided while resolving: a good token that holds nothing here.
		if e, ok := err.(*apperr.Error); ok && e.Status < http.StatusInternalServerError {
			refuse(api, ctx, metadata, e)
			return
		}
		if err != nil {
			// The revocation list is unreadable, so we cannot say this token was
			// not revoked. Fail closed.
			refuse(api, ctx, metadata, apperr.Upstream("identity_unavailable", err))
			return
		}
		if session == nil {
			refuse(api, ctx, metadata, apperr.Unauthorized(reason))
			return
		}

		// The wrong kind is not a weaker token: an otp token opens two calls only.
		if !session.Is(want) {
			refuse(api, ctx, metadata, apperr.Forbidden("wrong_token"))
			return
		}

		role, err := declared(ctx, RoleKey)
		if err != nil {
			fail(api, ctx, err)
			return
		}
		if role != "" && !session.HasRole(role) {
			refuse(api, ctx, metadata, apperr.Forbidden(role+"_only"))
			return
		}

		perm, err := declared(ctx, PermKey)
		if err != nil {
			fail(api, ctx, err)
			return
		}
		if perm != "" && !session.Has(perm) {
			refuse(api, ctx, metadata, apperr.ScopeRequired(perm))
			return
		}

		next(huma.WithContext(ctx, caller.NewContext(ctx.Context(), session)))
	}
}

// declared is what the route put under key: "" when it put nothing. Anything
// there that is not a name, an enum.Role for one, is a route written wrong, and
// a route written wrong refuses everybody rather than letting everybody through.
func declared(ctx huma.Context, key string) (string, error) {
	v, present := ctx.Operation().Metadata[key]
	if !present {
		return "", nil
	}
	if s, ok := v.(string); ok && s != "" {
		return s, nil
	}
	return "", apperr.Internal(fmt.Errorf("operation %s declares %s as %T %v, not a plain string",
		ctx.Operation().OperationID, key, v, v))
}

// refuse answers with the RFC 6750 challenge alongside our error body: the body
// is what a partner's log shows, the header is what an MCP client acts on.
func refuse(api huma.API, ctx huma.Context, metadata string, err error) {
	e, ok := err.(*apperr.Error)
	if !ok {
		fail(api, ctx, err)
		return
	}

	// Order follows the specification: what went wrong, what would have opened it,
	// then where to go about it.
	parts := []string{}
	switch {
	case e.Status == http.StatusForbidden && e.Scope != "":
		// Ours and current but never granted this. Asking for the scope is a
		// different move from signing in again, so it reads differently.
		parts = append(parts, `error="insufficient_scope"`, `scope="`+e.Scope+`"`)
	case e.Status == http.StatusUnauthorized:
		parts = append(parts, `error="invalid_token"`)
	}
	parts = append(parts, `resource_metadata="`+metadata+`"`)

	ctx.SetHeader("WWW-Authenticate", "Bearer "+strings.Join(parts, ", "))
	fail(api, ctx, e)
}

func fail(api huma.API, ctx huma.Context, err error) {
	status := http.StatusUnauthorized
	if e, ok := err.(*apperr.Error); ok {
		status = e.Status
	}
	_ = huma.WriteErr(api, ctx, status, "", err)
}
