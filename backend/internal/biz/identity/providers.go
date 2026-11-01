package identity

// Signing in through Google or Apple (docs/SSO.md): their token in, a session
// or a signup ticket out, the same two outcomes as a code.

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// IDToken checks a provider's id token and answers the person's id there.
// Defined here so the provider package stays below this one; main adapts the
// real one with IDTokenFunc. Its errors are read with errors.Is against
// ErrSSOInvalid and ErrSSOUnavailable; anything else counts as invalid.
type IDToken interface {
	Subject(ctx context.Context, raw string) (string, error)
}

// IDTokenFunc is a function as an IDToken, for main and for tests.
type IDTokenFunc func(ctx context.Context, raw string) (string, error)

func (f IDTokenFunc) Subject(ctx context.Context, raw string) (string, error) { return f(ctx, raw) }

type ProviderSignIn struct {
	// Providers holds the ones this deployment has client ids for. One that
	// is missing answers not_supported.
	Providers map[enum.Provider]IDToken

	Players   *PlayerStore
	Minter    Minter
	Issuer    *Issuer
	Audit     audit.Log
	SignupTTL time.Duration
}

var (
	errNotSupported         = errors.New(501, "not_supported")
	errInvalidProviderToken = errors.Unauthorized("invalid_provider_token")
)

// Do trades a provider's token for a session, or for a signup ticket carrying
// the account when nobody has signed up with it yet. Nothing is written then.
func (p *ProviderSignIn) Do(ctx context.Context, provider enum.Provider, raw string) (*Verified, error) {
	check, ok := p.Providers[provider]
	if !ok || check == nil {
		return nil, errNotSupported
	}

	subject, err := check.Subject(ctx, raw)
	switch {
	case stderrors.Is(err, ErrSSOUnavailable):
		// Nothing can be checked, so nobody is let in: fail closed.
		return nil, errors.Upstream("provider_unavailable", err)
	case err != nil:
		applog.From(ctx).Debug("provider token rejected", "provider", provider, "reason", err.Error())
		return nil, errInvalidProviderToken
	case subject == "":
		return nil, errInvalidProviderToken
	}

	player, err := p.Players.ByIdentity(ctx, provider, subject)
	if err != nil {
		return nil, errors.Internal(err)
	}

	if player == nil {
		t, err := p.Minter.MintProviderSignup(string(provider), subject, p.SignupTTL)
		if err != nil {
			return nil, err
		}
		return &Verified{Ticket: Ticket{
			Scope:       caller.ScopeSignup,
			AccessToken: t,
			ExpiresIn:   int(p.SignupTTL.Seconds()),
		}}, nil
	}

	if !player.CanSignIn() {
		p.record(ctx, player.ID, model.ActionRefused, map[string]any{"reason": "disabled", "provider": provider})
		return nil, errPlayerDisabled
	}

	out, err := p.Issuer.Open(ctx, player, "")
	if err != nil {
		return nil, err
	}
	// A sighting, not a failure: a sign-in that could not be stamped still is one.
	if err := p.Players.Touch(ctx, player.ID); err != nil {
		applog.From(ctx).Error("last seen not written", "player", player.ID, "err", err)
	}
	p.record(ctx, player.ID, model.ActionSignedIn, map[string]any{"provider": provider})
	return out, nil
}

// record never fails the sign-in it describes, as Verify's does not.
func (p *ProviderSignIn) record(ctx context.Context, playerID, action string, detail map[string]any) {
	if p.Audit == nil {
		return
	}
	if err := p.Audit.Write(ctx, model.Entry{
		Actor: playerID, Action: action, Subject: playerID, Detail: detail,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "err", err)
	}
}
