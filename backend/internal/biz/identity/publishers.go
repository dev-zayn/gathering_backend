package identity

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	stderrors "errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Publishers is the studios' way into the console (docs/PUBLISHERS.md): the
// accounts an admin makes, the one-time link a publisher chooses its password
// through, and the sessions after. A publisher's session is the console's
// cookie holding a token of its own kind, which no admin route opens.
type Publishers struct {
	DB        *gorm.DB
	Minter    Minter
	Whitelist Whitelist
	Limiter   Limiter

	// Audit is the admin area: an account is the console's business, not a
	// game's.
	Audit audit.Log

	// TTL is the longest a session lasts however busy; Idle how long it
	// survives unused. The console's own, so a studio and an admin sign out
	// alike.
	TTL  time.Duration
	Idle time.Duration
}

const (
	// SetupTTL is how long a one-time link waits to be used.
	SetupTTL = 7 * 24 * time.Hour

	// The tries at one email's password: a guesser gets ten every fifteen
	// minutes, and the studio behind it is one link away from a new password.
	publisherTries  = 10
	publisherWindow = 15 * time.Minute
)

// PublisherOpened is a publisher's console session: the token for the cookie,
// and whose it is.
type PublisherOpened struct {
	Token     string
	ExpiresAt time.Time
	Publisher *model.Publisher
}

// Setup is a one-time link's token, shown once: the console makes the link
// out of it, and only its hash is kept.
type Setup struct {
	Token     string
	ExpiresAt time.Time
}

// PublisherRow is an account as the console lists it, with how many games it
// has.
type PublisherRow struct {
	Publisher model.Publisher
	Games     int
}

var (
	errWrongPassword     = errors.Unauthorized("wrong_password")
	errPublisherDisabled = errors.Forbidden("publisher_disabled")
	errInvalidSetup      = errors.Unauthorized("invalid_setup")
	errPublisherNotFound = errors.NotFound("not_found")
)

// List is every account, the newest first.
func (p *Publishers) List(ctx context.Context) ([]PublisherRow, error) {
	var rows []model.Publisher
	if err := data.Conn(ctx, p.DB).Order("created_at DESC, id").Find(&rows).Error; err != nil {
		return nil, errors.Internal(err)
	}
	counts, err := p.gameCounts(ctx)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := make([]PublisherRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, PublisherRow{Publisher: r, Games: counts[r.ID]})
	}
	return out, nil
}

// Get is one account, for the console.
func (p *Publishers) Get(ctx context.Context, id string) (*PublisherRow, error) {
	row, err := p.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := p.gameCounts(ctx)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &PublisherRow{Publisher: *row, Games: counts[row.ID]}, nil
}

// Me is the signed-in publisher's own account. One switched off since the
// token was minted is gone: its sessions were revoked with it.
func (p *Publishers) Me(ctx context.Context, id string) (*model.Publisher, error) {
	row, err := p.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.DisabledAt != nil {
		return nil, errPublisherDisabled
	}
	return row, nil
}

// Create makes an account with no password, and the one-time link its studio
// chooses one through.
func (p *Publishers) Create(ctx context.Context, actor, name, email string) (*model.Publisher, *Setup, error) {
	name, email = strings.TrimSpace(name), model.NormalizeEmail(email)
	bad := map[string]string{}
	checkName(bad, name)
	checkEmail(bad, email)
	if len(bad) > 0 {
		return nil, nil, errors.Invalid(bad)
	}
	if taken, err := p.emailTaken(ctx, email, ""); err != nil {
		return nil, nil, errors.Internal(err)
	} else if taken {
		return nil, nil, errors.Conflict("email_taken")
	}

	setup, hash := newSetup()
	now := data.Now()
	row := &model.Publisher{
		ID: ids.New(ids.Publisher), Name: name, Email: email,
		SetupHash: hash, SetupExpiresAt: &setup.ExpiresAt, CreatedBy: actor, CreatedAt: now,
	}
	if err := data.Conn(ctx, p.DB).Create(row).Error; err != nil {
		if duplicate(err) {
			return nil, nil, errors.Conflict("email_taken")
		}
		return nil, nil, errors.Internal(err)
	}
	p.record(ctx, actor, model.ActionPublisherCreated, row.ID, map[string]any{"name": name, "email": email})
	return row, setup, nil
}

// Edit renames an account or moves its sign-in to another address. A new
// name reaches its games at their next publish.
func (p *Publishers) Edit(ctx context.Context, actor, id string, name, email *string) (*model.Publisher, error) {
	row, err := p.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	set := map[string]any{}
	bad := map[string]string{}
	fields := []string{}
	if name != nil {
		n := strings.TrimSpace(*name)
		checkName(bad, n)
		if n != row.Name {
			set["name"] = n
			fields = append(fields, "name")
		}
	}
	if email != nil {
		e := model.NormalizeEmail(*email)
		checkEmail(bad, e)
		if e != row.Email {
			set["email"] = e
			fields = append(fields, "email")
		}
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	if len(set) == 0 {
		return row, nil
	}
	if e, ok := set["email"].(string); ok {
		if taken, err := p.emailTaken(ctx, e, id); err != nil {
			return nil, errors.Internal(err)
		} else if taken {
			return nil, errors.Conflict("email_taken")
		}
	}
	if err := data.Conn(ctx, p.DB).Model(&model.Publisher{}).Where("id = ?", id).Updates(set).Error; err != nil {
		if duplicate(err) {
			return nil, errors.Conflict("email_taken")
		}
		return nil, errors.Internal(err)
	}
	p.record(ctx, actor, model.ActionPublisherEdited, id, map[string]any{"fields": fields})
	return p.byID(ctx, id)
}

// NewSetup replaces the account's one-time link: a first sign-in that was
// never made, or a forgotten password. The password stays what it was until
// the link is used.
func (p *Publishers) NewSetup(ctx context.Context, actor, id string) (*Setup, error) {
	if _, err := p.byID(ctx, id); err != nil {
		return nil, err
	}
	setup, hash := newSetup()
	err := data.Conn(ctx, p.DB).Model(&model.Publisher{}).Where("id = ?", id).
		Updates(map[string]any{"setup_hash": hash, "setup_expires_at": setup.ExpiresAt}).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	p.record(ctx, actor, model.ActionPublisherSetup, id, nil)
	return setup, nil
}

// Disable switches an account off, and every session it holds ends at once.
// Its games stay as they are: a studio's players are not its account.
func (p *Publishers) Disable(ctx context.Context, actor, id string) (*model.Publisher, error) {
	row, err := p.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.DisabledAt == nil {
		err := data.Conn(ctx, p.DB).Model(&model.Publisher{}).Where("id = ?", id).
			Update("disabled_at", data.Now()).Error
		if err != nil {
			return nil, errors.Internal(err)
		}
		p.record(ctx, actor, model.ActionPublisherDisabled, id, nil)
	}
	if err := p.Whitelist.RevokeSession(ctx, id); err != nil {
		return nil, errors.Internal(err)
	}
	return p.byID(ctx, id)
}

// Enable switches an account back on. It signs in again with the password it
// had.
func (p *Publishers) Enable(ctx context.Context, actor, id string) (*model.Publisher, error) {
	row, err := p.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.DisabledAt != nil {
		err := data.Conn(ctx, p.DB).Model(&model.Publisher{}).Where("id = ?", id).
			Update("disabled_at", nil).Error
		if err != nil {
			return nil, errors.Internal(err)
		}
		p.record(ctx, actor, model.ActionPublisherEnabled, id, nil)
	}
	return p.byID(ctx, id)
}

// SignIn is an email and a password. An unknown address, a wrong password
// and an account with no password yet are one answer: which it was is not a
// guesser's to learn. A switched-off account is told so only once the
// password matched.
func (p *Publishers) SignIn(ctx context.Context, email, password string) (*PublisherOpened, error) {
	email = model.NormalizeEmail(email)
	bad := map[string]string{}
	if email == "" {
		bad["email"] = "required"
	}
	if password == "" {
		bad["password"] = "required"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	ok, retry, err := p.Limiter.Allow(ctx, "publisher:signin:"+email, publisherTries, publisherWindow)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if !ok {
		return nil, errors.TooManyRequests(retry)
	}

	row, err := p.byEmail(ctx, email)
	if err != nil {
		return nil, errors.Internal(err)
	}
	hash := []byte(nil)
	if row != nil {
		hash = []byte(row.PasswordHash)
	}
	if !passwordMatches(hash, password) {
		return nil, errWrongPassword
	}
	if row.DisabledAt != nil {
		return nil, errPublisherDisabled
	}
	return p.open(ctx, row)
}

// UseSetup is the one-time link: the password chosen, the link spent, and a
// session for the studio that used it.
func (p *Publishers) UseSetup(ctx context.Context, raw, password string) (*PublisherOpened, error) {
	if bad := checkPassword(password); len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errInvalidSetup
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.Internal(err)
	}

	var row model.Publisher
	err = data.Conn(ctx, p.DB).Where("setup_hash = ?", model.HashSecret(raw)).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errInvalidSetup
	}
	if err != nil {
		return nil, errors.Internal(err)
	}
	if row.SetupExpiresAt == nil || !data.Now().Before(*row.SetupExpiresAt) {
		return nil, errInvalidSetup
	}
	if row.DisabledAt != nil {
		return nil, errPublisherDisabled
	}

	// Spent by the update itself: two tabs using one link both pass the read,
	// and only one finds the hash still there.
	res := data.Conn(ctx, p.DB).Model(&model.Publisher{}).
		Where("id = ? AND setup_hash = ?", row.ID, row.SetupHash).
		Updates(map[string]any{"password_hash": string(hash), "setup_hash": "", "setup_expires_at": nil})
	if res.Error != nil {
		return nil, errors.Internal(res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, errInvalidSetup
	}
	row.PasswordHash, row.SetupHash, row.SetupExpiresAt = string(hash), "", nil
	return p.open(ctx, &row)
}

// ChangePassword is a signed-in publisher choosing a new password. Its other
// sessions stay: the studio is the one asking.
func (p *Publishers) ChangePassword(ctx context.Context, id, current, password string) error {
	row, err := p.Me(ctx, id)
	if err != nil {
		return err
	}
	if bad := checkPassword(password); len(bad) > 0 {
		return errors.Invalid(bad)
	}
	ok, retry, err := p.Limiter.Allow(ctx, "publisher:signin:"+row.Email, publisherTries, publisherWindow)
	if err != nil {
		return errors.Internal(err)
	}
	if !ok {
		return errors.TooManyRequests(retry)
	}
	if !passwordMatches([]byte(row.PasswordHash), current) {
		return errWrongPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return errors.Internal(err)
	}
	err = data.Conn(ctx, p.DB).Model(&model.Publisher{}).Where("id = ?", id).
		Update("password_hash", string(hash)).Error
	if err != nil {
		return errors.Internal(err)
	}
	return nil
}

// Close ends one session, at once and for every tab holding it.
func (p *Publishers) Close(ctx context.Context, jti string) error {
	if err := p.Whitelist.Revoke(ctx, jti); err != nil {
		return errors.Internal(err)
	}
	return nil
}

// open is the session itself, once the studio has proved who it is. It is
// listed under the account, so switching it off reaches every one.
func (p *Publishers) open(ctx context.Context, row *model.Publisher) (*PublisherOpened, error) {
	signed, jti, err := p.Minter.MintPublisher(row.ID, p.TTL)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if err := p.Whitelist.Allow(ctx, jti, row.ID, p.Idle); err != nil {
		return nil, errors.Internal(err)
	}

	first := row.SeenAt == nil
	now := data.Now()
	if err := data.Conn(ctx, p.DB).Model(&model.Publisher{}).Where("id = ?", row.ID).Update("seen_at", now).Error; err != nil {
		return nil, errors.Internal(err)
	}
	row.SeenAt = &now
	if first {
		p.record(ctx, row.ID, model.ActionPublisherFirstSeen, row.ID, map[string]any{"name": row.Name})
	}
	return &PublisherOpened{Token: signed, ExpiresAt: now.Add(p.TTL), Publisher: row}, nil
}

func (p *Publishers) byID(ctx context.Context, id string) (*model.Publisher, error) {
	var row model.Publisher
	err := data.Conn(ctx, p.DB).Take(&row, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errPublisherNotFound
	}
	if err != nil {
		return nil, errors.Internal(err)
	}
	return &row, nil
}

func (p *Publishers) byEmail(ctx context.Context, email string) (*model.Publisher, error) {
	var rows []model.Publisher
	if err := data.Conn(ctx, p.DB).Where("email = ?", email).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (p *Publishers) emailTaken(ctx context.Context, email, except string) (bool, error) {
	var n int64
	q := data.Conn(ctx, p.DB).Model(&model.Publisher{}).Where("email = ?", email)
	if except != "" {
		q = q.Where("id <> ?", except)
	}
	err := q.Count(&n).Error
	return n > 0, err
}

// gameCounts is how many games each account has, in one query.
func (p *Publishers) gameCounts(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		PublisherID string
		N           int
	}
	err := data.Conn(ctx, p.DB).Raw(`SELECT publisher_id, count(*) AS n FROM games
		WHERE publisher_id IS NOT NULL GROUP BY publisher_id`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.PublisherID] = r.N
	}
	return out, nil
}

func (p *Publishers) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if p.Audit == nil {
		return
	}
	if err := p.Audit.Write(ctx, model.Entry{
		Actor: actor, Action: action, Subject: subject, Detail: detail,
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "subject", subject, "err", err)
	}
}

// newSetup draws a link's token and the hash that is kept of it.
func newSetup() (*Setup, string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("identity: no randomness available: " + err.Error())
	}
	enc := base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
	raw := "set_" + enc.EncodeToString(b)
	return &Setup{Token: raw, ExpiresAt: data.Now().Add(SetupTTL)}, model.HashSecret(raw)
}

// dummyHash is compared against when there is no account, so an unknown
// address costs the same second of bcrypt as a wrong password and the two
// cannot be told apart by the clock.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("no account has this password"), bcrypt.DefaultCost)

func passwordMatches(hash []byte, password string) bool {
	if len(password) > model.MaxPasswordByte {
		return false
	}
	if len(hash) == 0 {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}

func checkPassword(password string) map[string]string {
	switch {
	case len([]rune(password)) < model.MinPassword:
		return map[string]string{"password": "too_short"}
	case len(password) > model.MaxPasswordByte:
		return map[string]string{"password": "too_long"}
	}
	return nil
}

func checkName(bad map[string]string, name string) {
	switch {
	case name == "":
		bad["name"] = "required"
	case model.TooLong(name, model.MaxName):
		bad["name"] = "too_long"
	}
}

// checkEmail asks for the shape of an address and no more: whether anybody
// reads it is the admin's to know.
func checkEmail(bad map[string]string, email string) {
	at := strings.LastIndex(email, "@")
	switch {
	case email == "":
		bad["email"] = "required"
	case model.TooLong(email, model.MaxEmail):
		bad["email"] = "too_long"
	case at < 1 || at == len(email)-1 || strings.ContainsAny(email, " \t\r\n") ||
		!strings.Contains(email[at+1:], "."):
		bad["email"] = "invalid"
	}
}

func duplicate(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}
