package games

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// The tile picture is a file of ours, in our own bucket: a studio's CDN going
// away must not blank every store. Nothing in the bucket is public, so what
// goes out is a link that expires.

// Picture is one upload: the bytes and what they claim to be.
type Picture struct {
	ContentType string
	Data        []byte
}

const (
	MaxPicture = 2 << 20
	// PictureTTL is how long a link to a tile lives. Long, since a store shows
	// dozens at once and a player's shelf is not a secret.
	PictureTTL = time.Hour
	// MaxScreenshots is how many pictures a page in the store may carry.
	MaxScreenshots = 6
)

var pictureTypes = []string{"image/png", "image/jpeg"}

// checkPicture refuses what is not a picture, or too big, before the bucket
// is touched.
func checkPicture(what string, up Picture) error {
	if int64(len(up.Data)) > MaxPicture || !in(pictureTypes, up.ContentType) {
		e := errors.New(422, "invalid_file")
		e.Fields = map[string]string{
			"file": fmt.Sprintf("%s must be %s, up to %d MB", what, strings.Join(pictureTypes, " or "), MaxPicture>>20),
		}
		return e
	}
	return nil
}

// SetIcon replaces the tile picture. One file per game, at a fixed key, so
// there is nothing to garbage-collect.
func (r *Registry) SetIcon(ctx context.Context, actor, id string, up Picture) (*model.Game, error) {
	if r.Files == nil {
		return nil, errors.Internal(fmt.Errorf("no file store"))
	}
	if _, err := r.Get(ctx, id); err != nil {
		return nil, err
	}
	if err := checkPicture("icon", up); err != nil {
		return nil, err
	}

	key := iconKey(id, up.ContentType)
	if err := r.Files.Put(ctx, key, up.ContentType, up.Data); err != nil {
		return nil, errors.Internal(err)
	}
	if err := setIconKey(ctx, r.db, id, key); err != nil {
		return nil, errors.Internal(err)
	}
	r.record(ctx, actor, model.ActionListed, id, map[string]any{"fields": []string{"icon"}})
	return r.Get(ctx, id)
}

func iconKey(id, contentType string) string {
	return "games/" + id + "/icon." + extension(contentType)
}

func extension(contentType string) string {
	if contentType == "image/jpeg" {
		return "jpg"
	}
	return "png"
}

// IconURL is the link a tile is drawn from, or nothing when there is no file.
// A signing failure is logged and shown as no icon: a list of games must not
// fail because one picture could not be linked.
func IconURL(ctx context.Context, store files.Files, g *model.Game) string {
	if g.IconKey == "" || store == nil {
		return ""
	}
	u, err := store.URL(ctx, g.IconKey, PictureTTL)
	if err != nil {
		applog.From(ctx).Warn("icon not linked", "game", g.ID, "err", err)
		return ""
	}
	return u
}

func in(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// A screenshot is a file like the icon, under the game's own prefix, named by
// a short random id so two uploads never collide and a remove names one.

// AddScreenshot stores one more picture for the game's page in the store.
func (r *Registry) AddScreenshot(ctx context.Context, actor, id string, up Picture) (*model.Game, error) {
	if r.Files == nil {
		return nil, errors.Internal(fmt.Errorf("no file store"))
	}
	g, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(g.Screenshots) >= MaxScreenshots {
		return nil, errors.Invalid(map[string]string{"file": fmt.Sprintf("at most %d screenshots", MaxScreenshots)})
	}
	if err := checkPicture("screenshot", up); err != nil {
		return nil, err
	}

	key := screenshotKey(id, up.ContentType)
	if err := r.Files.Put(ctx, key, up.ContentType, up.Data); err != nil {
		return nil, errors.Internal(err)
	}
	if err := setScreenshots(ctx, r.db, id, append(g.Screenshots, key)); err != nil {
		return nil, errors.Internal(err)
	}
	r.record(ctx, actor, model.ActionListed, id, map[string]any{"fields": []string{"screenshots"}})
	return r.Get(ctx, id)
}

// RemoveScreenshot takes one off the page and out of the bucket. Naming one
// that is not there is not an error: it is gone either way.
func (r *Registry) RemoveScreenshot(ctx context.Context, actor, id, shot string) (*model.Game, error) {
	g, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	kept := model.Strings{}
	var gone string
	for _, k := range g.Screenshots {
		if ScreenshotID(k) == shot {
			gone = k
			continue
		}
		kept = append(kept, k)
	}
	if gone == "" {
		return g, nil
	}
	if err := setScreenshots(ctx, r.db, id, kept); err != nil {
		return nil, errors.Internal(err)
	}
	if r.Files != nil {
		if err := r.Files.Delete(ctx, gone); err != nil {
			applog.From(ctx).Warn("orphan file left behind", "key", gone, "err", err)
		}
	}
	r.record(ctx, actor, model.ActionListed, id, map[string]any{"fields": []string{"screenshots"}})
	return r.Get(ctx, id)
}

func screenshotKey(id, contentType string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("games: no randomness available: " + err.Error())
	}
	return "games/" + id + "/screens/" + hex.EncodeToString(b) + "." + extension(contentType)
}

// ScreenshotID is the part of a key a remove names: the file's own name,
// without the prefix or the extension.
func ScreenshotID(key string) string {
	name := key[strings.LastIndex(key, "/")+1:]
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	return name
}

// Screenshot is one picture as it goes out: what to name in a remove, and a
// link that expires.
type Screenshot struct {
	ID  string
	URL string
}

// ScreenshotURLs signs every picture of the page. One that cannot be linked is
// left out, with a warning, rather than failing the page.
func ScreenshotURLs(ctx context.Context, store files.Files, g *model.Game) []Screenshot {
	out := make([]Screenshot, 0, len(g.Screenshots))
	if store == nil {
		return out
	}
	for _, key := range g.Screenshots {
		u, err := store.URL(ctx, key, PictureTTL)
		if err != nil {
			applog.From(ctx).Warn("screenshot not linked", "game", g.ID, "key", key, "err", err)
			continue
		}
		out = append(out, Screenshot{ID: ScreenshotID(key), URL: u})
	}
	return out
}
