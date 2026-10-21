package identity

import (
	"context"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/token"
)

// Identities answers who is calling, once per request. It answers and does not
// refuse: whether no session is a problem depends on the operation. A bearer
// is always a token of ours; there is no other kind of credential.
type Identities struct {
	Signer    *token.Signer
	Whitelist Whitelist

	// AdminIdle is how long a console session survives unused. Every request
	// moves it on; a session nobody has touched for this long is over.
	AdminIdle time.Duration
}

// revocable are the kinds that outlive minting. An otp or signup token is spent
// within minutes and never listed, so asking would always come back no.
var revocable = map[string]bool{
	caller.ScopeSession: true,
	caller.ScopeGame:    true,
}

// errRoomTicket is a room ticket sent here. It is a good token of ours, made
// for a game's server to verify, and it opens nothing on this API: a 403, not
// a 401, so the client does not go and sign in again.
var errRoomTicket = errors.Forbidden("wrong_token")

// Answer is what comes back. Session is nil exactly when Reason is set.
type Answer struct {
	Session *caller.Session
	Reason  string
}

// Resolve is the authn middleware's Resolver: who is calling, or the reason
// nobody is. A 4xx *errors.Error is an answer to send as it is; any other
// error means the revocation list could not be read — fail closed.
func (i *Identities) Resolve(ctx context.Context, authorization string) (*caller.Session, string, error) {
	answer, err := i.Of(ctx, authorization)
	if err != nil {
		return nil, "", err
	}
	return answer.Session, answer.Reason, nil
}

func (i *Identities) Of(ctx context.Context, authorization string) (Answer, error) {
	raw, ok := token.Bearer(authorization)
	if !ok {
		return Answer{Reason: "missing_token"}, nil
	}

	claims, err := i.Signer.Parse(raw)
	if err != nil {
		// Parse already tells expired from malformed, and the difference is one
		// a client acts on: refresh, or sign in again.
		if err == token.ErrExpiredToken {
			return Answer{Reason: "expired_token"}, nil
		}
		return Answer{Reason: "invalid_token"}, nil
	}

	kind := claims.Kind
	if kind == "" {
		kind = claims.Scope
	}

	if kind == caller.ScopeRoom {
		return Answer{}, errRoomTicket
	}

	// A console session ends when it sits unused, so asking about it also
	// moves it on. One with no roles is not one we minted.
	if kind == caller.ScopeAdmin {
		if len(claims.Roles) == 0 {
			return Answer{Reason: "invalid_token"}, nil
		}
		live, err := i.Whitelist.Touch(ctx, claims.ID, i.AdminIdle)
		if err != nil {
			return Answer{}, err
		}
		if !live {
			return Answer{Reason: "revoked_token"}, nil
		}
	}

	if revocable[kind] {
		valid, err := i.Whitelist.Valid(ctx, claims.ID)
		if err != nil {
			return Answer{}, err
		}
		if !valid {
			return Answer{Reason: "revoked_token"}, nil
		}
	}

	return Answer{Session: &caller.Session{
		ID:       claims.ID,
		Subject:  claims.Subject,
		Scope:    claims.Scope,
		Kind:     claims.Kind,
		Audience: caller.Audience(claims.Audience),
		Phone:    claims.Phone,
		Room:     claims.Room,
		Role:     claims.Role,
		Seat:     claims.Seat,
		Roles:    claims.Roles,
		Session:  claims.Session,
		Live:     claims.Live,
	}}, nil
}
