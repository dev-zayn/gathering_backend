package games

// The store's categories (docs/CATEGORIES.md): the groups the store shows,
// kept by an admin. A game and a build carry a category's key; the table
// holds its name in each language and its place in the store's order.

import (
	"context"
	stderrors "errors"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

var (
	errCategoryExists    = errors.Conflict("category_exists")
	errCategoryProtected = errors.Conflict("category_protected")
)

// categoryKey is a key as the games carry it: 2 to 32 of a-z, 0-9 and dashes,
// from a letter.
var categoryKey = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// CategoryRow is a category with how many games carry it, in any listing.
type CategoryRow struct {
	model.Category
	Games int
}

// CategoryInput is what an admin sends. On an edit every field is optional
// and only what is set is written; ID is a new category's key alone.
type CategoryInput struct {
	ID       string
	NameAR   *string
	NameEN   *string
	Position *int
}

// listCategories is the table in the store's order.
func listCategories(ctx context.Context, db *gorm.DB) ([]model.Category, error) {
	var rows []model.Category
	err := data.Conn(ctx, db).Order("position, id").Find(&rows).Error
	return rows, err
}

// knownCategory says whether a key is in the table.
func knownCategory(ctx context.Context, db *gorm.DB, id string) (bool, error) {
	var n int64
	err := data.Conn(ctx, db).Model(&model.Category{}).Where("id = ?", id).Count(&n).Error
	return n > 0, err
}

// checkCategory is the field problem for a category a game or a build would
// carry: none for one in the table.
func checkCategory(ctx context.Context, db *gorm.DB, id string, bad map[string]string) error {
	ok, err := knownCategory(ctx, db, id)
	if err != nil {
		return err
	}
	if !ok {
		bad["category"] = "unknown"
	}
	return nil
}

// Categories is the store's list for a player, in the store's order.
func (c *Catalogue) Categories(ctx context.Context) ([]model.Category, error) {
	rows, err := listCategories(ctx, c.db)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return rows, nil
}

// Categories is the list for a studio's picker.
func (s *Studio) Categories(ctx context.Context) ([]model.Category, error) {
	rows, err := listCategories(ctx, s.db)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return rows, nil
}

// Categories is the console's list: each with how many games carry it.
func (r *Registry) Categories(ctx context.Context) ([]CategoryRow, error) {
	rows, err := listCategories(ctx, r.db)
	if err != nil {
		return nil, errors.Internal(err)
	}
	counts, err := categoryCounts(ctx, r.db)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := make([]CategoryRow, 0, len(rows))
	for _, c := range rows {
		out = append(out, CategoryRow{Category: c, Games: counts[c.ID]})
	}
	return out, nil
}

func categoryCounts(ctx context.Context, db *gorm.DB) (map[string]int, error) {
	var rows []struct {
		Category string
		N        int
	}
	err := data.Conn(ctx, db).Model(&model.Game{}).Select("category, count(*) AS n").
		Group("category").Scan(&rows).Error
	out := map[string]int{}
	for _, r := range rows {
		out[r.Category] = r.N
	}
	return out, err
}

// category is one row with its count; nil when there is none.
func (r *Registry) category(ctx context.Context, id string) (*CategoryRow, error) {
	var c model.Category
	err := data.Conn(ctx, r.db).Where("id = ?", id).Take(&c).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var n int64
	if err := data.Conn(ctx, r.db).Model(&model.Game{}).Where("category = ?", id).Count(&n).Error; err != nil {
		return nil, err
	}
	return &CategoryRow{Category: c, Games: int(n)}, nil
}

// checkNames gathers the problems of the names and the position as sent.
// required says a name must be sent (a new category).
func checkNames(in CategoryInput, required bool, bad map[string]string) {
	for _, f := range []struct {
		name string
		v    *string
	}{{"name_ar", in.NameAR}, {"name_en", in.NameEN}} {
		switch {
		case f.v == nil && required, f.v != nil && strings.TrimSpace(*f.v) == "":
			bad[f.name] = "required"
		case f.v != nil && model.TooLong(strings.TrimSpace(*f.v), model.MaxCategoryName):
			bad[f.name] = "too_long"
		}
	}
	if in.Position != nil && (*in.Position < 0 || *in.Position > model.MaxCategoryPlace) {
		bad["position"] = "0_to_10000"
	}
}

// CreateCategory makes a category. Without a position it goes after every
// category but Other.
func (r *Registry) CreateCategory(ctx context.Context, actor string, in CategoryInput) (*CategoryRow, error) {
	bad := map[string]string{}
	switch {
	case in.ID == "":
		bad["id"] = "required"
	case !categoryKey.MatchString(in.ID):
		bad["id"] = "invalid"
	}
	checkNames(in, true, bad)
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	position := 0
	if in.Position != nil {
		position = *in.Position
	} else {
		var last *int
		if err := data.Conn(ctx, r.db).Model(&model.Category{}).
			Where("position < (SELECT position FROM game_categories WHERE id = ?)", enum.CategoryOther).
			Select("max(position)").Scan(&last).Error; err != nil {
			return nil, errors.Internal(err)
		}
		if last != nil {
			position = min(*last+10, model.MaxCategoryPlace)
		}
	}

	now := data.Now()
	row := model.Category{
		ID: in.ID, NameAR: strings.TrimSpace(*in.NameAR), NameEN: strings.TrimSpace(*in.NameEN),
		Position: position, CreatedAt: now, UpdatedAt: now,
	}
	res := data.Conn(ctx, r.db).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return nil, errors.Internal(res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, errCategoryExists
	}

	r.record(ctx, actor, model.ActionCategoryCreated, row.ID, map[string]any{
		"name_ar": row.NameAR, "name_en": row.NameEN, "position": row.Position,
	})
	return &CategoryRow{Category: row}, nil
}

// EditCategory writes what was sent of a category's names and position.
func (r *Registry) EditCategory(ctx context.Context, actor, id string, in CategoryInput) (*CategoryRow, error) {
	current, err := r.category(ctx, id)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if current == nil {
		return nil, errNotFound
	}
	bad := map[string]string{}
	checkNames(in, false, bad)
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	fields := map[string]any{}
	sent := []string{}
	if in.NameAR != nil {
		fields["name_ar"] = strings.TrimSpace(*in.NameAR)
		sent = append(sent, "name_ar")
	}
	if in.NameEN != nil {
		fields["name_en"] = strings.TrimSpace(*in.NameEN)
		sent = append(sent, "name_en")
	}
	if in.Position != nil {
		fields["position"] = *in.Position
		sent = append(sent, "position")
	}
	if len(fields) == 0 {
		return current, nil
	}
	fields["updated_at"] = data.Now()
	if err := data.Conn(ctx, r.db).Model(&model.Category{}).Where("id = ?", id).Updates(fields).Error; err != nil {
		return nil, errors.Internal(err)
	}

	r.record(ctx, actor, model.ActionCategoryEdited, id, map[string]any{"fields": sent})
	row, err := r.category(ctx, id)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return row, nil
}

// DeleteCategory moves every game and build that carries it to Other, then
// deletes it, in one transaction. It answers how many games moved.
func (r *Registry) DeleteCategory(ctx context.Context, actor, id string) (int, error) {
	moved := 0
	err := data.NewTx(r.db).Do(ctx, func(ctx context.Context) error {
		var c model.Category
		err := data.Conn(ctx, r.db).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", id).Take(&c).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if c.ID == string(enum.CategoryOther) {
			return errCategoryProtected
		}

		now := data.Now()
		res := data.Conn(ctx, r.db).Model(&model.Game{}).Where("category = ?", id).
			Updates(map[string]any{"category": enum.CategoryOther, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		moved = int(res.RowsAffected)
		if err := data.Conn(ctx, r.db).Model(&model.GameBuild{}).Where("category = ?", id).
			Updates(map[string]any{"category": enum.CategoryOther, "updated_at": now}).Error; err != nil {
			return err
		}
		return data.Conn(ctx, r.db).Where("id = ?", id).Delete(&model.Category{}).Error
	})
	if err != nil {
		var e *errors.Error
		if stderrors.As(err, &e) {
			return 0, e
		}
		return 0, errors.Internal(err)
	}

	r.record(ctx, actor, model.ActionCategoryDeleted, id, map[string]any{"moved": moved})
	return moved, nil
}
