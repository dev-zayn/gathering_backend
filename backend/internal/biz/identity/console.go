package identity

import (
	"context"
	stderrors "errors"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Console is how admins get a session here. The identity provider says who
// they are and which of our roles they hold, once, the way "Sign in with
// Google" does; after that the console holds a token of ours, which signing
// out and sitting idle both end at once. The provider's token is never kept.
type Console struct {
	SSO       SSO
	Admins    AdminRows
	Minter    Minter
	Whitelist Whitelist
	Audit     audit.Log

	// TTL is the longest a session lasts however busy; Idle is how long it
	// survives unused.
	TTL  time.Duration
	Idle time.Duration

	// DevAllowed lets OpenDev mint an admin with no provider at all. A laptop
	// setting behind DEBUG; main never sets it anywhere else.
	DevAllowed bool

	// Password is the bcrypt hash of the console's one password
	// (CONSOLE_PASSWORD_HASH), for a team with no identity provider: whoever
	// knows it signs in as an admin under the name they type. Empty, there is
	// no such sign-in. Limiter counts the tries, every one of them, before the
	// hash is asked: a guess costs the server a second of bcrypt.
	Password []byte
	Limiter  Limiter
}

// The tries at the password, all of them together: nothing tells one guesser
// from another here, so a flood locks everybody out for the window rather
// than letting it through.
const (
	passwordTries  = 20
	passwordWindow = 15 * time.Minute
)

// Person is who the provider says somebody is, and which of our roles they
// hold, bare: "admin", not "<service>.admin". The provider package keeps only
// this service's and strips the prefix; roles we do not know are dropped here.
type Person struct {
	Subject string
	Name    string
	Email   string
	Roles   []string
}

// SSO checks a token the identity provider signed. Defined here rather than
// imported so the provider package stays below this one; main adapts the real
// verifier with SSOFunc. The errors it returns are read with errors.Is against
// ErrSSOInvalid and ErrSSOUnavailable; anything else counts as invalid.
type SSO interface {
	Verify(ctx context.Context, raw string) (*Person, error)
}

// SSOFunc is a function as an SSO, for main and for tests.
type SSOFunc func(ctx context.Context, raw string) (*Person, error)

func (f SSOFunc) Verify(ctx context.Context, raw string) (*Person, error) { return f(ctx, raw) }

var (
	// ErrSSOInvalid is a token the provider does not stand behind: 401. The
	// provider package wraps its refusals with it.
	ErrSSOInvalid = stderrors.New("identity: the provider refused the token")

	// ErrSSOUnavailable is the provider out of reach, so nothing can be
	// checked: 502, and nobody is let in meanwhile.
	ErrSSOUnavailable = stderrors.New("identity: the provider could not be reached")
)

// AdminRows is the console's list of names. *AdminStore is the real one.
type AdminRows interface {
	Enroll(ctx context.Context, subject, name, email string) (*model.Admin, error)
	Seen(ctx context.Context, id, name, email string) error
	ByID(ctx context.Context, id string) (*model.Admin, error)
}

// Opened is a console session: the token to send, and whose it is.
type Opened struct {
	Token     string
	ExpiresAt time.Time
	Admin     *model.Admin
	Roles     []string
}

// Open trades the provider's access token for a session of ours. A genuine
// token holding none of our roles is a 403: it is who they say; they just have
// nothing here, and nothing is written down about them.
func (c *Console) Open(ctx context.Context, raw string) (*Opened, error) {
	id, err := c.SSO.Verify(ctx, raw)
	switch {
	case stderrors.Is(err, ErrSSOUnavailable):
		// Nothing can be checked, so nothing is let through: fail closed.
		return nil, errors.Upstream("identity_unavailable", err)
	case err != nil:
		// The provider package may already have decided the status; anything
		// else, ErrSSOInvalid included, is a token we do not stand behind.
		var e *errors.Error
		if stderrors.As(err, &e) && e.Status < 500 {
			return nil, e
		}
		applog.From(ctx).Debug("sso token rejected", "reason", err.Error())
		return nil, errors.Unauthorized("invalid_token")
	}

	roles := known(id.Roles)
	if len(roles) == 0 {
		applog.From(ctx).Info("admin access denied", "reason", "no role here", "sub", id.Subject)
		return nil, errors.Forbidden("no_role")
	}
	return c.open(ctx, id.Subject, id.Name, id.Email, roles)
}

// OpenDev is a laptop's sign-in: a name typed into the console and an admin
// session for it, with no provider asked. Anywhere else it does not exist.
func (c *Console) OpenDev(ctx context.Context, name string) (*Opened, error) {
	if !c.DevAllowed {
		return nil, errors.NotFound("not_found")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.Invalid(map[string]string{"name": "required"})
	}
	if model.TooLong(name, model.MaxName) {
		return nil, errors.Invalid(map[string]string{"name": "too_long"})
	}
	return c.open(ctx, "dev:"+name, name, "", []string{string(enum.AdminRoleAdmin)})
}

// OpenPassword is the sign-in for a console with no provider: the console's
// password and a name, which is who the activity log says did each thing.
// The same name is the same admin every time. With no password set it does
// not exist.
func (c *Console) OpenPassword(ctx context.Context, name, password string) (*Opened, error) {
	if len(c.Password) == 0 {
		return nil, errors.NotFound("not_found")
	}
	name = strings.TrimSpace(name)
	fields := map[string]string{}
	switch {
	case name == "":
		fields["name"] = "required"
	case model.TooLong(name, model.MaxName):
		fields["name"] = "too_long"
	}
	if password == "" {
		fields["password"] = "required"
	}
	if len(fields) > 0 {
		return nil, errors.Invalid(fields)
	}
	// bcrypt reads 72 bytes and refuses more: no such password is the one.
	if len(password) > 72 {
		return nil, errors.Unauthorized("wrong_password")
	}

	ok, retry, err := c.Limiter.Allow(ctx, "console:password", passwordTries, passwordWindow)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if !ok {
		return nil, errors.TooManyRequests(retry)
	}
	if bcrypt.CompareHashAndPassword(c.Password, []byte(password)) != nil {
		applog.From(ctx).Info("console password refused", "name", clip(name, model.MaxName))
		return nil, errors.Unauthorized("wrong_password")
	}
	return c.open(ctx, "password:"+strings.ToLower(name), name, "", []string{string(enum.AdminRoleAdmin)})
}

// open is the session itself, once somebody has said who the person is.
func (c *Console) open(ctx context.Context, subject, name, email string, roles []string) (*Opened, error) {
	name, email = clip(strings.TrimSpace(name), model.MaxName), clip(model.NormalizeEmail(email), model.MaxEmail)

	admin, err := c.Admins.Enroll(ctx, subject, name, email)
	if err != nil || admin == nil {
		return nil, errors.Internal(err)
	}
	first := admin.SeenAt == nil
	if err := c.Admins.Seen(ctx, admin.ID, name, email); err != nil {
		return nil, errors.Internal(err)
	}
	if first {
		c.record(ctx, admin.ID, model.ActionAdminFirstSeen, map[string]any{"subject": subject, "name": name})
	}

	signed, jti, err := c.Minter.MintAdmin(admin.ID, roles, c.TTL)
	if err != nil {
		return nil, errors.Internal(err)
	}
	// Listed under the admin, so EndAll can find every session they hold.
	if err := c.Whitelist.Allow(ctx, jti, admin.ID, c.Idle); err != nil {
		return nil, errors.Internal(err)
	}

	admin, err = c.Admins.ByID(ctx, admin.ID)
	if err != nil || admin == nil {
		return nil, errors.Internal(err)
	}
	return &Opened{Token: signed, ExpiresAt: nowFn().Add(c.TTL), Admin: admin, Roles: roles}, nil
}

// known keeps the roles this console has, sorted and once each. The provider
// package hands over our roles bare, "admin" not "<service>.admin", so a
// spelling it did not strip is not ours and is dropped like any other.
func known(roles []string) []string {
	var out []string
	for _, r := range roles {
		if enum.AdminRole(r).Valid() && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

// Close ends one session, at once and for every tab holding it.
func (c *Console) Close(ctx context.Context, jti string) error {
	if err := c.Whitelist.Revoke(ctx, jti); err != nil {
		return errors.Internal(err)
	}
	return nil
}

// EndAll signs one admin out of every console session they hold, on every
// device. Roles are read at sign-in, so taking one away at the provider
// reaches a session already open only when it ends; this is how it ends now.
func (c *Console) EndAll(ctx context.Context, actor, adminID string) error {
	admin, err := c.Admins.ByID(ctx, adminID)
	if err != nil {
		return errors.Internal(err)
	}
	if admin == nil {
		return errors.NotFound("not_found")
	}
	if err := c.Whitelist.RevokeSession(ctx, adminID); err != nil {
		return errors.Internal(err)
	}
	if err := c.Audit.Write(ctx, model.Entry{
		Actor: actor, Action: model.ActionAdminSessionsEnded, Subject: adminID,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", model.ActionAdminSessionsEnded, "subject", adminID, "err", err)
	}
	return nil
}

// record logs a failed write rather than failing the sign-in.
func (c *Console) record(ctx context.Context, actor, action string, detail map[string]any) {
	if err := c.Audit.Write(ctx, model.Entry{
		Actor: actor, Action: action, Subject: actor, Detail: detail,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "subject", actor, "err", err)
	}
}

// clip keeps a value inside its column, counted in characters as varchar does.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
