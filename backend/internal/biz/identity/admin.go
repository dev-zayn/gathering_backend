package identity

import (
	"context"
	stderrors "errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// AdminStore is the console's list of names. The identity provider holds who
// the admins are and what roles they have; this only remembers the name behind
// each id, so the audit log reads as people rather than numbers. Nobody is
// added or removed here: a row appears the first time somebody opens the
// console.
type AdminStore struct{ db *gorm.DB }

func NewAdminStore(db *gorm.DB) *AdminStore { return &AdminStore{db: db} }

func (a *AdminStore) ByID(ctx context.Context, id string) (*model.Admin, error) {
	return a.one(ctx, "id = ?", id)
}

func (a *AdminStore) one(ctx context.Context, where string, args ...any) (*model.Admin, error) {
	var out model.Admin
	err := data.Conn(ctx, a.db).Where(where, args...).Take(&out).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// List is everybody who has ever opened the console, first comer first.
func (a *AdminStore) List(ctx context.Context) ([]model.Admin, error) {
	var out []model.Admin
	if err := data.Conn(ctx, a.db).Order("created_at, id").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Enroll is the row behind a provider's subject, made the first time that
// person opens the console and found every time after. A new row has no
// SeenAt yet: that is how the console tells a first visit, before Seen stamps
// it. The name and address are written only on the way in; Seen keeps them
// current.
func (a *AdminStore) Enroll(ctx context.Context, subject, name, email string) (*model.Admin, error) {
	if found, err := a.one(ctx, "sso_subject = ?", subject); err != nil || found != nil {
		return found, err
	}

	row := &model.Admin{
		ID: ids.New(ids.Admin), Subject: subject, Name: name, Email: email, CreatedAt: data.Now(),
	}
	res := data.Conn(ctx, a.db).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 1 {
		return row, nil
	}
	// Two first requests at once: the other one made the row.
	return a.one(ctx, "sso_subject = ?", subject)
}

// Seen stamps a sign-in and writes the latest name and address. An empty name
// or address keeps what was there: the provider not answering must not blank
// a name that was right yesterday.
func (a *AdminStore) Seen(ctx context.Context, id, name, email string) error {
	set := map[string]any{"seen_at": data.Now()}
	if name != "" {
		set["name"] = name
	}
	if email != "" {
		set["email"] = email
	}
	return data.Conn(ctx, a.db).Model(&model.Admin{}).Where("id = ?", id).Updates(set).Error
}
