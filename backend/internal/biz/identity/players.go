package identity

// Where the records actually live. GORM is used here and nowhere else; the
// schema itself belongs to the migrations.

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

type PlayerStore struct{ db *gorm.DB }

func NewPlayerStore(db *gorm.DB) *PlayerStore { return &PlayerStore{db: db} }

// ByPhone returns nil, nil when nobody has that number. Not finding someone is
// an answer sign-in needs, not a failure.
func (s *PlayerStore) ByPhone(ctx context.Context, phone string) (*model.Player, error) {
	return s.one(ctx, "phone = ?", phone)
}

func (s *PlayerStore) ByID(ctx context.Context, id string) (*model.Player, error) {
	return s.one(ctx, "id = ?", id)
}

// ByHandle is case-blind: the column is lowercase, and what a person typed
// need not be.
func (s *PlayerStore) ByHandle(ctx context.Context, handle string) (*model.Player, error) {
	return s.one(ctx, "handle = ?", strings.ToLower(strings.TrimSpace(handle)))
}

func (s *PlayerStore) one(ctx context.Context, where string, args ...any) (*model.Player, error) {
	var out model.Player
	err := data.Conn(ctx, s.db).Where(where, args...).Take(&out).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PlayerStore) Create(ctx context.Context, p *model.Player) error {
	if p.Language == "" {
		p.Language = enum.Arabic
	}
	p.Handle = strings.ToLower(p.Handle)
	now := data.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	return data.Conn(ctx, s.db).Create(p).Error
}

// Update writes what a player may change about themselves. The phone and the
// switch are not among them: the phone is proven, not edited, and the switch
// is an admin's (SetDisabled).
func (s *PlayerStore) Update(ctx context.Context, p *model.Player) error {
	p.UpdatedAt = data.Now()
	p.Handle = strings.ToLower(p.Handle)
	return data.Conn(ctx, s.db).Model(&model.Player{ID: p.ID}).Updates(map[string]any{
		"nickname":          p.Nickname,
		"handle":            p.Handle,
		"handle_changed_at": p.HandleChangedAt,
		"avatar_key":        p.AvatarKey,
		"bio":               p.Bio,
		"language":          string(p.Language),
		"updated_at":        p.UpdatedAt,
	}).Error
}

// Touch is a sighting: the player used the app just now.
func (s *PlayerStore) Touch(ctx context.Context, id string) error {
	return data.Conn(ctx, s.db).Model(&model.Player{}).Where("id = ?", id).
		Update("last_seen_at", data.Now()).Error
}

// SetDisabled is the admin's switch. Their sessions are the caller's to end;
// this only stops the next sign-in.
func (s *PlayerStore) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return data.Conn(ctx, s.db).Model(&model.Player{}).Where("id = ?", id).
		Updates(map[string]any{"disabled": disabled, "updated_at": data.Now()}).Error
}

// Search is the handles that start with what was typed, lowercase, shortest
// first. A disabled player is not found: there is nothing to do with them.
func (s *PlayerStore) Search(ctx context.Context, handlePrefix string, limit int) ([]model.Player, error) {
	prefix := strings.ToLower(strings.TrimSpace(handlePrefix))
	if prefix == "" {
		return []model.Player{}, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var out []model.Player
	err := data.Conn(ctx, s.db).
		Where("handle LIKE ? AND NOT disabled", likePrefix(prefix)).
		Order("handle").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// likePrefix is the LIKE pattern for a prefix, with the term's own wildcards
// escaped: a handle holds underscores, and "ali_" must not match "alix".
func likePrefix(term string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(term) + "%"
}

func (s *PlayerStore) HandleTaken(ctx context.Context, handle string) (bool, error) {
	var n int64
	err := data.Conn(ctx, s.db).Model(&model.Player{}).
		Where("handle = ?", strings.ToLower(strings.TrimSpace(handle))).Count(&n).Error
	return n > 0, err
}

var (
	// ErrPhoneTaken is one number already belonging to somebody. A phone is
	// unique across the platform, not just within a game.
	ErrPhoneTaken = errors.Conflict("phone_taken")
	// ErrHandleTaken is a handle somebody else already holds.
	ErrHandleTaken = errors.Conflict("handle_taken")
)

// IsPhoneConflict reads the unique index on players.phone. Two people
// registering the same number at the same moment is the case it exists for.
func IsPhoneConflict(err error) bool { return isConflict(err, "phone") }

// IsHandleConflict reads the unique index on players.handle the same way.
func IsHandleConflict(err error) bool { return isConflict(err, "handle") }

func isConflict(err error, column string) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "duplicate key") && strings.Contains(s, column)
}

// nowFn exists so a test can stand still.
var nowFn = time.Now
