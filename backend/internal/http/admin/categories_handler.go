package admin

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// The store's categories (docs/CATEGORIES.md): the groups the store shows,
// kept here.

type consoleCategoryJSON struct {
	ID       string `json:"id" example:"cards"`
	NameAR   string `json:"name_ar" example:"ورق"`
	NameEN   string `json:"name_en" example:"Cards"`
	Position int    `json:"position" example:"80" doc:"The store's order, smallest first; ties by id."`
	Games    int    `json:"games" doc:"How many games carry it, in any listing."`
}

func consoleCategoryOf(c *games.CategoryRow) consoleCategoryJSON {
	return consoleCategoryJSON{ID: c.ID, NameAR: c.NameAR, NameEN: c.NameEN, Position: c.Position, Games: c.Games}
}

type consoleCategoriesResponse struct {
	Body struct {
		Categories []string              `json:"categories" doc:"The keys alone, in the store's order."`
		Items      []consoleCategoryJSON `json:"items"`
	}
}

type consoleCategoryResponse struct {
	Status int
	Body   consoleCategoryJSON
}

type categoryPath struct {
	Category string `path:"category" example:"cards"`
}

type categoryCreateRequest struct {
	Body struct {
		ID       string  `json:"id,omitempty" example:"cards" doc:"The key games carry: 2 to 32 of a-z, 0-9 and -, from a letter. It never changes. Refused: id: required, id: invalid; 409 category_exists."`
		NameAR   *string `json:"name_ar,omitempty" example:"ورق" doc:"Up to 40 characters. Refused: name_ar: required, name_ar: too_long."`
		NameEN   *string `json:"name_en,omitempty" example:"Cards" doc:"Up to 40 characters. Refused: name_en: required, name_en: too_long."`
		Position *int    `json:"position,omitempty" doc:"0 to 10000 (position: 0_to_10000). Without one it goes after every category but other."`
	}
}

type categoryEditRequest struct {
	Category string `path:"category" example:"cards"`
	Body     struct {
		NameAR   *string `json:"name_ar,omitempty" doc:"Refused: name_ar: required (sent empty), name_ar: too_long (40)."`
		NameEN   *string `json:"name_en,omitempty" doc:"Refused: name_en: required (sent empty), name_en: too_long (40)."`
		Position *int    `json:"position,omitempty" doc:"0 to 10000 (position: 0_to_10000)."`
	}
}

type categoryDeletedResponse struct {
	Body struct {
		Moved int `json:"moved" doc:"How many games moved to other."`
	}
}

func (h *Handler) registerCategories(api huma.API) {
	admin := map[string]any{authn.ScopeKey: caller.ScopeAdmin}
	write := map[string]any{authn.ScopeKey: caller.ScopeAdmin, authn.RoleKey: string(enum.AdminRoleAdmin)}

	huma.Register(api, huma.Operation{
		OperationID: "admin-categories",
		Method:      http.MethodGet,
		Path:        "/v1/admin/categories",
		Summary:     "How the store groups games",
		Description: "Every category in the store's order, with how many games carry each. " +
			"The console's pickers and labels read it here rather than guessing.",
		Tags:     []string{"admin"},
		Metadata: admin,
	}, h.categories)

	huma.Register(api, huma.Operation{
		OperationID: "admin-category-create",
		Method:      http.MethodPost,
		Path:        "/v1/admin/categories",
		Summary:     "Make a category",
		Description: "A key and its name in Arabic and English. Refused: 422 invalid_fields " +
			"(id, name_ar, name_en, position); 409 category_exists (docs/CATEGORIES.md).",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusCreated,
		Metadata:      write,
	}, h.createCategory)

	huma.Register(api, huma.Operation{
		OperationID: "admin-category-edit",
		Method:      http.MethodPatch,
		Path:        "/v1/admin/categories/{category}",
		Summary:     "Rename or move a category",
		Description: "Only what is sent is written; the key never changes. Refused: 404 " +
			"not_found; 422 invalid_fields (name_ar, name_en, position).",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.editCategory)

	huma.Register(api, huma.Operation{
		OperationID: "admin-category-delete",
		Method:      http.MethodDelete,
		Path:        "/v1/admin/categories/{category}",
		Summary:     "Delete a category",
		Description: "Every game and build that carries it moves to other first, in the same " +
			"transaction. Refused: 404 not_found; 409 category_protected for other.",
		Tags:     []string{"admin"},
		Metadata: write,
	}, h.deleteCategory)
}

func (h *Handler) categories(ctx context.Context, _ *struct{}) (*consoleCategoriesResponse, error) {
	rows, err := h.Registry.Categories(ctx)
	if err != nil {
		return nil, err
	}
	out := &consoleCategoriesResponse{}
	out.Body.Categories = make([]string, 0, len(rows))
	out.Body.Items = make([]consoleCategoryJSON, 0, len(rows))
	for i := range rows {
		out.Body.Categories = append(out.Body.Categories, rows[i].ID)
		out.Body.Items = append(out.Body.Items, consoleCategoryOf(&rows[i]))
	}
	return out, nil
}

func (h *Handler) createCategory(ctx context.Context, in *categoryCreateRequest) (*consoleCategoryResponse, error) {
	row, err := h.Registry.CreateCategory(ctx, actor(ctx), games.CategoryInput{
		ID: in.Body.ID, NameAR: in.Body.NameAR, NameEN: in.Body.NameEN, Position: in.Body.Position,
	})
	if err != nil {
		return nil, err
	}
	return &consoleCategoryResponse{Status: http.StatusCreated, Body: consoleCategoryOf(row)}, nil
}

func (h *Handler) editCategory(ctx context.Context, in *categoryEditRequest) (*consoleCategoryResponse, error) {
	row, err := h.Registry.EditCategory(ctx, actor(ctx), in.Category, games.CategoryInput{
		NameAR: in.Body.NameAR, NameEN: in.Body.NameEN, Position: in.Body.Position,
	})
	if err != nil {
		return nil, err
	}
	return &consoleCategoryResponse{Status: http.StatusOK, Body: consoleCategoryOf(row)}, nil
}

func (h *Handler) deleteCategory(ctx context.Context, in *categoryPath) (*categoryDeletedResponse, error) {
	moved, err := h.Registry.DeleteCategory(ctx, actor(ctx), in.Category)
	if err != nil {
		return nil, err
	}
	out := &categoryDeletedResponse{}
	out.Body.Moved = moved
	return out, nil
}
