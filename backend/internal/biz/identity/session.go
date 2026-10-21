package identity

import (
	"context"
	stderrors "errors"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Issuer hands out the pair a signed-in device holds. Verifying and rotating both
// end here, so the two cannot disagree about what a session token contains.
type Issuer struct {
	Sessions  *SessionStore
	Tokens    *RefreshStore
	Minter    Minter
	Whitelist Whitelist

	SessionTTL time.Duration
	RefreshTTL time.Duration
}

// Open starts a new device session. Live is true: a person typed a code a second
// ago, which is what makes elevated permissions available.
func (i *Issuer) Open(ctx context.Context, player *model.Player, device string) (*Verified, error) {
	sess := &model.Session{
		ID:        ids.New(ids.Session),
		PlayerID:  player.ID,
		Device:    device,
		ExpiresAt: nowFn().Add(i.RefreshTTL),
	}
	if err := i.Sessions.Create(ctx, sess); err != nil {
		return nil, errors.Internal(err)
	}

	refresh := ids.New(ids.Refresh)
	if err := i.Tokens.Create(ctx, &model.Refresh{
		Hash:      model.HashRefresh(refresh),
		SessionID: sess.ID,
	}); err != nil {
		return nil, errors.Internal(err)
	}

	return i.mint(ctx, sess, player, refresh, true)
}

// Rotate exchanges a refresh token for the next pair. Live is false: nobody was
// asked anything, so it cannot carry elevated permissions.
func (i *Issuer) Rotate(ctx context.Context, sess *model.Session, player *model.Player, spentHash string) (*Verified, error) {
	refresh := ids.New(ids.Refresh)

	if err := i.Tokens.Spend(ctx, spentHash, model.HashRefresh(refresh)); err != nil {
		// Losing the race means another request already exchanged this token.
		return nil, errInvalidRefresh
	}
	if err := i.Sessions.Touch(ctx, sess.ID); err != nil {
		return nil, errors.Internal(err)
	}

	return i.mint(ctx, sess, player, refresh, false)
}

func (i *Issuer) mint(ctx context.Context, sess *model.Session, player *model.Player, refresh string, live bool) (*Verified, error) {
	access, jti, err := i.Minter.MintSession(player.ID, sess.ID, live, i.SessionTTL)
	if err != nil {
		return nil, err
	}
	if err := i.Whitelist.Allow(ctx, jti, sess.ID, i.SessionTTL); err != nil {
		return nil, errors.Internal(err)
	}

	return &Verified{
		Ticket: Ticket{
			Scope:       caller.ScopeSession,
			AccessToken: access,
			ExpiresIn:   int(i.SessionTTL.Seconds()),
		},
		RefreshToken: refresh,
		Player:       player,
	}, nil
}

type SessionStore struct{ db *gorm.DB }

func NewSessionStore(db *gorm.DB) *SessionStore { return &SessionStore{db: db} }

func (s *SessionStore) ByID(ctx context.Context, id string) (*model.Session, error) {
	var out model.Session
	err := data.Conn(ctx, s.db).Take(&out, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListByPlayer is the live sessions, most recently used first.
func (s *SessionStore) ListByPlayer(ctx context.Context, playerID string) ([]model.Session, error) {
	var out []model.Session
	if err := data.Conn(ctx, s.db).
		Where("player_id = ? AND revoked_at IS NULL", playerID).
		Order("last_seen_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SessionStore) Create(ctx context.Context, sess *model.Session) error {
	now := data.Now()
	sess.CreatedAt, sess.LastSeenAt = now, now
	return data.Conn(ctx, s.db).Create(sess).Error
}

func (s *SessionStore) Touch(ctx context.Context, id string) error {
	return data.Conn(ctx, s.db).Model(&model.Session{}).
		Where("id = ?", id).Update("last_seen_at", data.Now()).Error
}

func (s *SessionStore) Revoke(ctx context.Context, id string) error {
	return data.Conn(ctx, s.db).Model(&model.Session{}).
		Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", data.Now()).Error
}

// RevokeAll ends every session one player holds, on every phone: what
// disabling them, or their own "sign out everywhere", needs. The ids come back
// so the whitelist can be cleared for each.
func (s *SessionStore) RevokeAll(ctx context.Context, playerID string) ([]string, error) {
	var ended []string
	err := data.Conn(ctx, s.db).Model(&model.Session{}).
		Where("player_id = ? AND revoked_at IS NULL", playerID).
		Pluck("id", &ended).Error
	if err != nil || len(ended) == 0 {
		return nil, err
	}
	err = data.Conn(ctx, s.db).Model(&model.Session{}).
		Where("id IN ?", ended).Update("revoked_at", data.Now()).Error
	return ended, err
}

// errInvalidRefresh is the only answer refresh gives: not found, spent, revoked
// and expired look alike, or an attacker learns which token they hold.
var errInvalidRefresh = errors.Unauthorized("invalid_refresh_token")

type Refresh struct {
	Tokens    *RefreshStore
	Sessions  *SessionStore
	Players   *PlayerStore
	Whitelist Whitelist
	Issuer    *Issuer
}

// Do exchanges a refresh token for a new pair. An already-spent one means a copy
// is loose, so the whole session dies rather than the request simply failing.
func (r *Refresh) Do(ctx context.Context, plain string) (*Verified, error) {
	hash := model.HashRefresh(plain)

	rt, err := r.Tokens.ByHash(ctx, hash)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if rt == nil {
		return nil, errInvalidRefresh
	}

	if rt.Spent() {
		// Somebody is holding a token we already exchanged. Which of the two is
		// the real device is unknowable, so neither keeps the session.
		applog.From(ctx).Warn("refresh token replayed", "session", rt.SessionID)
		if err := r.kill(ctx, rt.SessionID); err != nil {
			return nil, errors.Internal(err)
		}
		return nil, errInvalidRefresh
	}

	sess, err := r.Sessions.ByID(ctx, rt.SessionID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if sess == nil || !sess.Active(nowFn()) {
		return nil, errInvalidRefresh
	}

	player, err := r.Players.ByID(ctx, sess.PlayerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if player == nil || !player.CanSignIn() {
		// Removed or switched off since they last used the app. The session goes
		// with them.
		if err := r.kill(ctx, sess.ID); err != nil {
			return nil, errors.Internal(err)
		}
		return nil, errInvalidRefresh
	}

	out, err := r.Issuer.Rotate(ctx, sess, player, hash)
	if err != nil {
		return nil, err
	}
	if err := r.Players.Touch(ctx, player.ID); err != nil {
		applog.From(ctx).Error("last seen not written", "player", player.ID, "err", err)
	}
	return out, nil
}

// Logout ends one device. The session record is what has to die: dropping the
// token on the device would leave the server still willing to refresh it.
func (r *Refresh) Logout(ctx context.Context, sessionID string) error {
	if err := r.kill(ctx, sessionID); err != nil {
		return errors.Internal(err)
	}
	return nil
}

func (r *Refresh) kill(ctx context.Context, sessionID string) error {
	if err := r.Sessions.Revoke(ctx, sessionID); err != nil {
		return err
	}
	return r.Whitelist.RevokeSession(ctx, sessionID)
}

type RefreshStore struct{ db *gorm.DB }

func NewRefreshStore(db *gorm.DB) *RefreshStore { return &RefreshStore{db: db} }

func (r *RefreshStore) ByHash(ctx context.Context, hash string) (*model.Refresh, error) {
	var out model.Refresh
	err := data.Conn(ctx, r.db).Take(&out, "hash = ?", hash).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *RefreshStore) Create(ctx context.Context, t *model.Refresh) error {
	t.CreatedAt = data.Now()
	return data.Conn(ctx, r.db).Create(t).Error
}

// Spend is the whole of rotation. The update only matches an unused token, so
// two requests racing with the same one cannot both win.
func (r *RefreshStore) Spend(ctx context.Context, oldHash, newHash string) error {
	return data.Conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.Refresh{}).
			Where("hash = ? AND used_at IS NULL", oldHash).
			Update("used_at", data.Now())
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		var old model.Refresh
		if err := tx.Take(&old, "hash = ?", oldHash).Error; err != nil {
			return err
		}
		return tx.Create(&model.Refresh{
			Hash:      newHash,
			SessionID: old.SessionID,
			CreatedAt: data.Now(),
		}).Error
	})
}
