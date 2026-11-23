package games

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// A build's pictures live under its own prefix, games/<id>/builds/<build>/,
// so nothing a publisher uploads while testing touches what players see. A
// build starts with the live game's pictures, by key; taking one of those off
// the build leaves the live game's file where it is.

// SetIcon replaces the build's tile picture.
func (s *Studio) SetIcon(ctx context.Context, who Account, buildID string, up Picture) (*model.GameBuild, error) {
	b, g, err := s.pictureBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	if err := checkPicture("icon", up); err != nil {
		return nil, err
	}
	key := buildPrefix(b.GameID, b.ID) + "icon." + extension(up.ContentType)
	if err := s.reg.Files.Put(ctx, key, up.ContentType, up.Data); err != nil {
		return nil, errors.Internal(err)
	}
	// A jpeg replacing a png leaves the png behind under the same prefix.
	if old := b.IconKey; old != "" && old != key && old != g.IconKey {
		s.deleteFiles(ctx, []string{old})
	}
	return s.pictured(ctx, who, b, g, map[string]any{"icon_key": key}, "icon")
}

// AddScreenshot puts one more picture on the build's page.
func (s *Studio) AddScreenshot(ctx context.Context, who Account, buildID string, up Picture) (*model.GameBuild, error) {
	b, g, err := s.pictureBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	if len(b.Screenshots) >= MaxScreenshots {
		return nil, errors.Invalid(map[string]string{"file": fmt.Sprintf("at most %d screenshots", MaxScreenshots)})
	}
	if err := checkPicture("screenshot", up); err != nil {
		return nil, err
	}
	name := make([]byte, 6)
	if _, err := rand.Read(name); err != nil {
		panic("games: no randomness available: " + err.Error())
	}
	key := buildPrefix(b.GameID, b.ID) + "screens/" + hex.EncodeToString(name) + "." + extension(up.ContentType)
	if err := s.reg.Files.Put(ctx, key, up.ContentType, up.Data); err != nil {
		return nil, errors.Internal(err)
	}
	shots := append(model.Strings{}, b.Screenshots...)
	return s.pictured(ctx, who, b, g, map[string]any{"screenshots": append(shots, key)}, "screenshots")
}

// RemoveScreenshot takes one off the build's page. Its file goes only when
// it is the build's own.
func (s *Studio) RemoveScreenshot(ctx context.Context, who Account, buildID, shot string) (*model.GameBuild, error) {
	b, g, err := s.ownedBuild(ctx, who.ID, buildID)
	if err != nil {
		return nil, err
	}
	if err := editable(b); err != nil {
		return nil, err
	}
	kept := model.Strings{}
	var gone string
	for _, k := range b.Screenshots {
		if ScreenshotID(k) == shot {
			gone = k
			continue
		}
		kept = append(kept, k)
	}
	if gone == "" {
		return b, nil
	}
	out, err := s.pictured(ctx, who, b, g, map[string]any{"screenshots": kept}, "screenshots")
	if err != nil {
		return nil, err
	}
	s.deleteFiles(ctx, ownFiles(&model.GameBuild{ID: b.ID, GameID: b.GameID, Screenshots: model.Strings{gone}}, g))
	return out, nil
}

// pictureBuild is a build a picture may be put on, with its game.
func (s *Studio) pictureBuild(ctx context.Context, publisherID, buildID string) (*model.GameBuild, *model.Game, error) {
	if s.reg.Files == nil {
		return nil, nil, errors.Internal(fmt.Errorf("no file store"))
	}
	b, g, err := s.ownedBuild(ctx, publisherID, buildID)
	if err != nil {
		return nil, nil, err
	}
	if err := editable(b); err != nil {
		return nil, nil, err
	}
	return b, g, nil
}

// pictured writes a picture change: an approved build goes back to testing,
// since the reviewer saw other pictures.
func (s *Studio) pictured(ctx context.Context, who Account, b *model.GameBuild, g *model.Game, set map[string]any, field string) (*model.GameBuild, error) {
	if b.Status == enum.BuildApproved {
		set["status"] = enum.BuildTesting
	}
	if err := s.updateBuild(ctx, b.ID, set); err != nil {
		return nil, err
	}
	s.reg.record(ctx, who.ID, model.ActionBuildEdited, g.ID, map[string]any{"build": b.ID, "fields": []string{field}})
	return BuildByID(ctx, s.db, b.ID)
}
