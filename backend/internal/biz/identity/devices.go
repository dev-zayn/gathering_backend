package identity

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// Devices is which signed-in phones can be pushed to. The app hands its
// Firebase token over after signing in, and again whenever Firebase gives it
// a new one.
type Devices struct{ db *gorm.DB }

func NewDevices(db *gorm.DB) *Devices { return &Devices{db: db} }

// MaxToken is longer than any Firebase token seen, and short enough that a
// request cannot park a novel in the table.
const MaxToken = 4096

// Register ties a token to this session. The same token on another session is
// moved here: it is one app install, now signed in as somebody else.
func (d *Devices) Register(ctx context.Context, sessionID, playerID string, platform enum.Platform, token string) (*model.Device, error) {
	token = strings.TrimSpace(token)
	bad := map[string]string{}
	if token == "" {
		bad["token"] = "required"
	} else if len(token) > MaxToken {
		bad["token"] = "too_long"
	}
	if !platform.Valid() {
		bad["platform"] = "unknown"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	now := data.Now()
	dev := &model.Device{
		ID: ids.New(ids.Device), SessionID: sessionID, PlayerID: playerID,
		Platform: platform, FCMToken: token, CreatedAt: now, LastSeenAt: now,
	}
	err := d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ? OR fcm_token = ?", sessionID, token).Delete(&model.Device{}).Error; err != nil {
			return err
		}
		return tx.Create(dev).Error
	})
	if err != nil {
		return nil, errors.Internal(err)
	}
	return dev, nil
}

// Forget stops pushes to this session's phone: the person turned them off,
// or is signing out.
func (d *Devices) Forget(ctx context.Context, sessionID string) error {
	if err := d.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&model.Device{}).Error; err != nil {
		return errors.Internal(err)
	}
	return nil
}

// Drop removes tokens Firebase said are dead: the app was uninstalled.
func (d *Devices) Drop(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	return d.db.WithContext(ctx).Where("fcm_token IN ?", tokens).Delete(&model.Device{}).Error
}

// Sweep removes phones whose session ended: revoked, or past its time.
func (d *Devices) Sweep(ctx context.Context, now time.Time) (int64, error) {
	ended := d.db.Model(&model.Session{}).Select("id").Where("revoked_at IS NOT NULL OR expires_at < ?", now)
	res := d.db.WithContext(ctx).Where("session_id IN (?)", ended).Delete(&model.Device{})
	return res.RowsAffected, res.Error
}
