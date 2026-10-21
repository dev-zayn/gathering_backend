package identity

// Signing in: the code out, the code back, and the pace a number may ask at.

import (
	"context"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
	"github.com/jamaa/gathering/backend/internal/pkg/phone"
)

// SignInPolicy is the part of sign-in that is a decision rather than a rule.
type SignInPolicy struct {
	CodeTTL  time.Duration // how long a code stays usable
	TokenTTL time.Duration // how long the otp token lasts; the client sees this

	// Cooldown is the wait between two codes to one number, Hourly the ceiling for
	// that number in an hour.
	Cooldown time.Duration
	Hourly   int

	// Ceiling is what the whole deployment may send in a minute. The limits above
	// are per number and cannot see a caller rotating through them.
	Ceiling int

	// Resends is how many times one attempt may be repeated before the player
	// has to start again. It is discovered by hitting it.
	Resends int
}

type SignIn struct {
	Codes   OTPStore
	Sender  OTPSender
	Minter  Minter
	Limiter Limiter
	Policy  SignInPolicy

	// Fixed, when set, is the code every sign-in gets instead of a random one,
	// so a sweep can sign in without reading the log. A laptop setting behind
	// DEBUG; main never sets it anywhere else.
	Fixed string
}

// Ticket is what signing in returns at every step before there is a session:
// a scope saying what the token is for, the token, and how long it lasts.
type Ticket struct {
	Scope       string
	AccessToken string
	ExpiresIn   int
}

var errSendFailed = errors.Upstream("otp_send_failed", nil)

// Start sends a code to a number in whatever form it was typed. Known or not
// makes no difference here: answering differently would say who is registered.
func (s *SignIn) Start(ctx context.Context, raw string) (*Ticket, error) {
	e164, err := phone.Normalize(raw)
	if err != nil {
		return nil, err
	}
	if err := s.allowed(ctx, e164); err != nil {
		return nil, err
	}
	return s.deliver(ctx, e164, false)
}

// Resend is the same for somebody already holding an otp token. The number comes
// from the token, so it cannot be swapped.
func (s *SignIn) Resend(ctx context.Context, e164, jti string) (*Ticket, error) {
	// The cooldown is checked before the cap is spent. Tapping "send again"
	// twice in five seconds should cost a wait, not one of the two resends.
	if err := s.allowed(ctx, e164); err != nil {
		return nil, err
	}

	ok, _, err := s.Limiter.Allow(ctx, "otp:resend:"+jti, s.Policy.Resends, s.Policy.TokenTTL)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if !ok {
		return nil, errNoResends
	}
	return s.deliver(ctx, e164, true)
}

var errNoResends = errors.New(429, "no_resends_left")

// deliver is the part that costs money: a code out, a code stored, a token back.
// Everything that could refuse the request has already refused it.
func (s *SignIn) deliver(ctx context.Context, e164 string, resend bool) (*Ticket, error) {
	code := s.code()

	// The vendor first: a code stored before a failed send locks the number out of
	// a retry for its whole life.
	if err := s.Sender.Send(ctx, e164, code); err != nil {
		applog.From(ctx).Error("otp send failed", "phone", phone.Mask(e164), "err", err)
		return nil, errSendFailed.Wrap(err)
	}

	// A resend keeps the wrong-guess counter: it is the same attempt with a new
	// code, not a fresh five guesses.
	store := s.Codes.Put
	if resend {
		store = s.Codes.Replace
	}
	if err := store(ctx, e164, model.HashCode(code), s.Policy.CodeTTL); err != nil {
		return nil, errors.Internal(err)
	}

	t, err := s.Minter.MintOTP(e164, s.Policy.TokenTTL)
	if err != nil {
		return nil, err
	}

	return &Ticket{
		Scope:       caller.ScopeOTP,
		AccessToken: t,
		ExpiresIn:   int(s.Policy.TokenTTL.Seconds()),
	}, nil
}

// code is random unless a laptop pinned it.
func (s *SignIn) code() string {
	if s.Fixed != "" {
		return s.Fixed
	}
	return model.NewCode()
}

// allowed is three limits: two on the number, and one on the whole deployment.
// They answer with the seconds left, which is what the client shows.
func (s *SignIn) allowed(ctx context.Context, e164 string) error {
	ok, retry, err := s.Limiter.Allow(ctx, "otp:cooldown:"+e164, 1, s.Policy.Cooldown)
	if err != nil {
		return errors.Internal(err)
	}
	if !ok {
		return errors.TooManyRequests(retry)
	}

	ok, retry, err = s.Limiter.Allow(ctx, "otp:hourly:"+e164, s.Policy.Hourly, time.Hour)
	if err != nil {
		return errors.Internal(err)
	}
	if !ok {
		return errors.TooManyRequests(retry)
	}

	// Last, so the bucket counts messages rather than attempts: what reaches here
	// is what would reach the vendor. Zero switches it off, which only a test wants.
	if s.Policy.Ceiling > 0 {
		ok, retry, err = s.Limiter.Allow(ctx, "otp:ceiling", s.Policy.Ceiling, time.Minute)
		if err != nil {
			return errors.Internal(err)
		}
		if !ok {
			return errors.TooManyRequests(retry)
		}
	}
	return nil
}

type VerifyPolicy struct {
	MaxAttempts int
	SignupTTL   time.Duration
}

type Verify struct {
	Codes   OTPStore
	Audit   audit.Log
	Players *PlayerStore
	Minter  Minter
	Issuer  *Issuer
	Policy  VerifyPolicy
}

// Verified is one of two outcomes: a known number brings a session and the
// player behind it, an unknown one only a signup ticket, with nothing written.
type Verified struct {
	Ticket

	RefreshToken string
	Player       *model.Player
}

var (
	errCodeExpired    = errors.Unauthorized("code_expired")
	errPlayerDisabled = errors.Forbidden("player_disabled")
)

// Do checks the code against the number inside the otp token, never the request:
// nobody verifies a code for a phone they do not hold.
func (v *Verify) Do(ctx context.Context, phone, code string) (*Verified, error) {
	pending, found, err := v.Codes.Get(ctx, phone)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if !found {
		v.record(ctx, phone, model.ActionRefused, phone, map[string]any{"reason": "code_expired"})
		return nil, errCodeExpired
	}

	// The guess is counted before it is compared. Counted after, every request in
	// flight together would be compared against the live code before any of them
	// reached the limit, and five guesses would become as many as fit in that gap.
	used, err := v.Codes.Attempt(ctx, phone)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if used > v.Policy.MaxAttempts {
		// Past the limit: not compared at all, right code or not.
		if err := v.Codes.Clear(ctx, phone); err != nil {
			return nil, errors.Internal(err)
		}
		v.record(ctx, phone, model.ActionRefused, phone, map[string]any{
			"reason":        "wrong_code",
			"attempts_left": 0,
		})
		return nil, errors.WrongCode(0)
	}

	if !model.CodeMatches(code, pending.Hash) {
		left := v.Policy.MaxAttempts - used
		if left <= 0 {
			left = 0
			// Out of guesses. The code is gone and the otp token is now worthless;
			// the player starts again at /signin.
			if err := v.Codes.Clear(ctx, phone); err != nil {
				return nil, errors.Internal(err)
			}
		}
		v.record(ctx, phone, model.ActionRefused, phone, map[string]any{
			"reason":        "wrong_code",
			"attempts_left": left,
		})
		return nil, errors.WrongCode(left)
	}

	// Right code. It is spent whatever happens next.
	if err := v.Codes.Clear(ctx, phone); err != nil {
		return nil, errors.Internal(err)
	}

	player, err := v.Players.ByPhone(ctx, phone)
	if err != nil {
		return nil, errors.Internal(err)
	}

	if player == nil {
		t, err := v.Minter.MintSignup(phone, v.Policy.SignupTTL)
		if err != nil {
			return nil, err
		}
		return &Verified{Ticket: Ticket{
			Scope:       caller.ScopeSignup,
			AccessToken: t,
			ExpiresIn:   int(v.Policy.SignupTTL.Seconds()),
		}}, nil
	}

	if !player.CanSignIn() {
		v.record(ctx, player.ID, model.ActionRefused, player.ID, map[string]any{"reason": "disabled"})
		return nil, errPlayerDisabled
	}

	out, err := v.Issuer.Open(ctx, player, "")
	if err != nil {
		return nil, err
	}
	// A sighting, not a failure: a sign-in that could not be stamped still is one.
	if err := v.Players.Touch(ctx, player.ID); err != nil {
		applog.From(ctx).Error("last seen not written", "player", player.ID, "err", err)
	}
	v.record(ctx, player.ID, model.ActionSignedIn, player.ID, nil)
	return out, nil
}

// record never fails the sign-in it describes. A refusal that was not logged is
// bad; refusing somebody who got the code right because of it would be worse.
func (v *Verify) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if v.Audit == nil {
		return
	}
	if err := v.Audit.Write(ctx, model.Entry{
		Actor: actor, Action: action, Subject: subject, Detail: detail,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "err", err)
	}
}
