package guests

import (
	"context"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// The scopes the bridge itself reads. Everything else in the vocabulary is a
// permission over some other part of the API, not over this answer.
const (
	ScopeIdentityRead = "identity:read"
	ScopeFriendsRead  = "friends:read"
	ScopeNotifySend   = "notify:send"
	ScopeResultsWrite = "results:write"
)

// Bridge is the whole of what a guest may ask the host for, filtered by the
// scopes granted at launch so no game is trusted to leave anything out.
type Bridge struct {
	Players *identity.PlayerStore
	Games   *games.Catalogue

	// Files signs the link to an uploaded avatar. Nil is a deployment with
	// bundled avatars only, where the id is the picture.
	Files     files.Files
	AvatarTTL time.Duration
}

// Caller is the game token, read once: who is playing, which game, in which
// room, with what.
type Caller struct {
	PlayerID string
	GameID   string
	RoomID   string
	Scopes   []string
	Live     bool
}

func (c Caller) has(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Context is the answer to "who am I talking to". An ungranted field is
// absent rather than empty, so a game can tell "not given" from "not set".
type Context struct {
	Player Player

	// Room is the room the game was opened into. Nil until the rooms arrive:
	// the token may carry an id, but there is nothing to say about it yet.
	Room *RoomSummary

	Scopes []string
	Live   bool
}

// Player is the card, as far as the scopes allow: the id always, the rest
// with identity:read.
type Player struct {
	ID       string
	Nickname string
	Handle   string

	// Avatar is a bundled avatar's id; AvatarURL is a signed link when the
	// picture was uploaded. One of them is set, or neither.
	Avatar    string
	AvatarURL string
}

// RoomSummary is the room as a game's server sees it. Arrives with the rooms.
type RoomSummary struct {
	ID     string
	Code   string
	Status string
	Role   string
	Seat   int
}

// Who answers on every launch: a cached nickname goes stale the day the
// player changes it, and a webhook is delivery we would have to own.
func (b *Bridge) Who(ctx context.Context, c Caller) (*Context, error) {
	out := &Context{
		Player: Player{ID: c.PlayerID},
		Scopes: c.Scopes,
		Live:   c.Live,
	}

	// The token is signed and whitelisted, but the shelf is the lifetime: a
	// game the player removed in the last hour has nothing to be told.
	grant, err := b.Games.Grant(ctx, c.PlayerID, c.GameID)
	if err != nil {
		return nil, err
	}
	if !grant.InLibrary {
		return nil, errors.Unauthorized("invalid_token")
	}

	if !c.has(ScopeIdentityRead) {
		return out, nil
	}

	player, err := b.Players.ByID(ctx, c.PlayerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if player == nil || !player.CanSignIn() {
		// The person behind the token was removed or switched off. Nothing to
		// describe, and nothing to serve.
		return nil, errors.Unauthorized("invalid_token")
	}
	out.Player.Nickname = player.Nickname
	out.Player.Handle = player.Handle

	// An uploaded picture is a key with a slash, signed on the way out; a
	// bundled one is an id the app ships, handed over as it is.
	switch {
	case player.AvatarKey == "":
	case strings.Contains(player.AvatarKey, "/"):
		if b.Files == nil {
			break
		}
		u, err := b.Files.URL(ctx, player.AvatarKey, b.AvatarTTL)
		if err != nil {
			applog.From(ctx).Warn("avatar not linked", "player", player.ID, "err", err)
			break
		}
		out.Player.AvatarURL = u
	default:
		out.Player.Avatar = player.AvatarKey
	}
	return out, nil
}
