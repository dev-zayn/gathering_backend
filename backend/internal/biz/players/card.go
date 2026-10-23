package players

import (
	"context"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Card is a player as others see them wherever a row draws one: a friend, a
// request, a line of a leaderboard, whoever caused a notification.
type Card struct {
	ID        string  `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname  string  `json:"nickname" example:"سارة"`
	Handle    string  `json:"handle" example:"sara_1203"`
	Avatar    string  `json:"avatar" doc:"A bundled avatar's id, or empty."`
	AvatarURL *string `json:"avatar_url" nullable:"true" doc:"A link that expires, when the player uploaded a picture."`
}

// NewCard draws one from the columns, the picture signed on the way out.
func NewCard(ctx context.Context, store files.Files, ttl time.Duration, id, nickname, handle, avatarKey string) Card {
	c := Card{ID: id, Nickname: nickname, Handle: handle}
	c.Avatar, c.AvatarURL = Picture(ctx, store, ttl, id, avatarKey)
	return c
}

// Picture is the one rule for a player's avatar: an uploaded picture is a
// key with a slash, signed on the way out (the link); a bundled one is an id
// the app ships (the avatar). One of the two, or neither. A link that cannot
// be signed is no picture, not a failed answer.
func Picture(ctx context.Context, store files.Files, ttl time.Duration, playerID, key string) (avatar string, link *string) {
	switch {
	case key == "":
	case strings.Contains(key, "/"):
		if store == nil {
			return "", nil
		}
		u, err := store.URL(ctx, key, ttl)
		if err != nil {
			applog.From(ctx).Warn("avatar not linked", "player", playerID, "err", err)
			return "", nil
		}
		return "", &u
	default:
		return key, nil
	}
	return "", nil
}
