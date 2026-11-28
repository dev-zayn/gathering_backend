//go:build e2e

package e2e

import (
	"strconv"
	"testing"
	"time"
)

type storeCategoryJSON struct {
	ID     string `json:"id"`
	NameAR string `json:"name_ar"`
	NameEN string `json:"name_en"`
}

type consoleCategoryJSON struct {
	storeCategoryJSON
	Position int `json:"position"`
	Games    int `json:"games"`
}

// The store's categories (docs/CATEGORIES.md) and a game's publisher
// (docs/PUBLISHERS.md, "A game's publisher"): the console makes a category,
// files the sweep's game in it, and a player's store lists it by its names
// and narrows to it; renamed, then deleted, its game moves to other. Then the
// game is handed to a publisher account the sweep makes, and back to none.
func TestCategoriesAndAGamesPublisher(t *testing.T) {
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	id := "sweep." + stamp
	key := "sweep-" + stamp
	studio := serveStudio(t, id)

	admin := newClient(t)
	admin.origin = console
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, nil)
	admin.upload("PUT", "/v1/admin/games/"+id+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)

	// Made: every problem at once first, then the category, after every one
	// but other.
	var refused errorJSON
	admin.call("POST", "/v1/admin/categories", map[string]any{"id": "Not A Key", "name_ar": ""}, 422, &refused)
	if refused.Fields["id"] != "invalid" || refused.Fields["name_ar"] != "required" || refused.Fields["name_en"] != "required" {
		t.Fatalf("a bad category: %+v", refused)
	}
	var made consoleCategoryJSON
	admin.call("POST", "/v1/admin/categories", map[string]any{"id": key, "name_ar": "سويب", "name_en": "Sweep"}, 201, &made)
	gone := false
	t.Cleanup(func() {
		if !gone {
			admin.call("DELETE", "/v1/admin/categories/"+key, nil, 200, nil)
		}
	})
	admin.call("POST", "/v1/admin/categories", map[string]any{"id": key, "name_ar": "سويب", "name_en": "Sweep"}, 409, &refused)
	if refused.Error != "category_exists" {
		t.Fatalf("making it twice: %+v", refused)
	}
	var list struct {
		Categories []string              `json:"categories"`
		Items      []consoleCategoryJSON `json:"items"`
	}
	admin.call("GET", "/v1/admin/categories", nil, 200, &list)
	at := -1
	for i, c := range list.Items {
		if c.ID == key {
			at = i
		}
	}
	if at < 0 || list.Items[len(list.Items)-1].ID != "other" || list.Categories[at] != key {
		t.Fatalf("the console's list: %+v", list)
	}
	t.Logf("ok   the console made category %s at %d, before other", key, made.Position)

	// A game filed in it, and a player's store names it and narrows to it.
	admin.call("PATCH", "/v1/admin/games/"+id, map[string]any{"tagline": "لعبة الفئات", "category": key}, 200, nil)
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, nil)
	t.Cleanup(func() {
		admin.call("POST", "/v1/admin/games/"+id+"/suspend", map[string]any{"reason": "the categories sweep is over"}, 200, nil)
	})
	p, _ := player(t, "فئات")
	var store struct {
		Items []storeCategoryJSON `json:"items"`
	}
	p.call("GET", "/v1/categories", nil, 200, &store)
	var named *storeCategoryJSON
	for i := range store.Items {
		if store.Items[i].ID == key {
			named = &store.Items[i]
		}
	}
	if named == nil || named.NameAR != "سويب" || named.NameEN != "Sweep" {
		t.Fatalf("the player's categories: %+v", store.Items)
	}
	var narrowed struct {
		Games []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
		} `json:"games"`
	}
	p.call("GET", "/v1/games?category="+key, nil, 200, &narrowed)
	if len(narrowed.Games) != 1 || narrowed.Games[0].ID != id || narrowed.Games[0].Category != key {
		t.Fatalf("the store narrowed to %s: %+v", key, narrowed.Games)
	}
	p.call("GET", "/v1/games?category=no-such-key", nil, 422, &refused)
	if refused.Fields["category"] != "unknown" {
		t.Fatalf("an unknown category: %+v", refused)
	}
	t.Logf("ok   a player's store names %s and narrows to it: %s alone", key, id)

	// Renamed, then deleted: its game moves to other; other stays.
	var edited consoleCategoryJSON
	admin.call("PATCH", "/v1/admin/categories/"+key, map[string]any{"name_en": "Sweeping"}, 200, &edited)
	if edited.NameEN != "Sweeping" || edited.NameAR != "سويب" || edited.Games != 1 {
		t.Fatalf("renamed: %+v", edited)
	}
	admin.call("DELETE", "/v1/admin/categories/other", nil, 409, &refused)
	if refused.Error != "category_protected" {
		t.Fatalf("deleting other: %+v", refused)
	}
	var deleted struct {
		Moved int `json:"moved"`
	}
	admin.call("DELETE", "/v1/admin/categories/"+key, nil, 200, &deleted)
	gone = true
	var game struct {
		Category  string `json:"category"`
		Publisher string `json:"publisher"`
		Owner     *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"owner"`
	}
	admin.call("GET", "/v1/admin/games/"+id, nil, 200, &game)
	if deleted.Moved != 1 || game.Category != "other" {
		t.Fatalf("deleted, %d moved; the game is in %q", deleted.Moved, game.Category)
	}
	t.Logf("ok   renamed, then deleted: 1 game moved to other, and other cannot go")

	// The game handed to a publisher account the sweep makes, then to none.
	var account struct {
		Publisher struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"publisher"`
	}
	admin.call("POST", "/v1/admin/publishers", map[string]any{
		"name": "استوديو السويب " + stamp, "email": key + "@sweep.example",
	}, 201, &account)
	pub := account.Publisher.ID
	t.Cleanup(func() { admin.call("POST", "/v1/admin/publishers/"+pub+"/disable", nil, 200, nil) })

	admin.call("PUT", "/v1/admin/games/"+id+"/owner", map[string]any{"publisher": "pub_nosuchaccount00"}, 422, &refused)
	if refused.Fields["publisher"] != "unknown" {
		t.Fatalf("an unknown publisher: %+v", refused)
	}
	admin.call("PUT", "/v1/admin/games/no.such.game/owner", map[string]any{"publisher": pub}, 404, nil)
	admin.call("PUT", "/v1/admin/games/"+id+"/owner", map[string]any{"publisher": pub}, 200, &game)
	if game.Owner == nil || game.Owner.ID != pub || game.Publisher != account.Publisher.Name {
		t.Fatalf("handed over: owner %+v named %q", game.Owner, game.Publisher)
	}
	admin.call("PUT", "/v1/admin/games/"+id+"/owner", map[string]any{"publisher": ""}, 200, &game)
	if game.Owner != nil || game.Publisher != account.Publisher.Name {
		t.Fatalf("handed to none: owner %+v named %q", game.Owner, game.Publisher)
	}
	t.Logf("ok   %s handed to %s, then to none (the name kept)", id, pub)
}
