package guests

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Notifier raises a notification in a player's inbox. *inbox.Inbox is one.
type Notifier interface {
	Raise(ctx context.Context, n inbox.Notice) (*inbox.Raised, error)
}

// NotifyHourly is how many notifications one game may raise for one player
// in an hour, when the bridge is not told otherwise.
const NotifyHourly = 10

// Notice is what a game's server asks to put in its player's inbox.
type Notice struct {
	Title    string
	Body     string
	DeepLink string

	// Key is the idempotency key: a notice with a key the game already used
	// for this player updates that one, back at the top and unread.
	Key string
}

// Notify is the game's server raising a notification for the player its
// token is for, as the game. Every problem with the fields comes back at
// once; then the game must still be on the player's shelf, and within its
// hourly allowance, which every call that gets that far spends, a reused
// key included. The route already refuses a token without notify:send;
// asking again here keeps the rule with the bridge rather than the route.
func (b *Bridge) Notify(ctx context.Context, c Caller, in Notice) (*inbox.Raised, error) {
	if !c.has(ScopeNotifySend) {
		return nil, errors.ScopeRequired(ScopeNotifySend)
	}

	title, body := strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	bad := map[string]string{}
	if n := utf8.RuneCountInString(title); n < 1 || n > model.MaxTitle {
		bad["title"] = "1_to_60"
	}
	if model.TooLong(body, model.MaxBody) {
		bad["body"] = "up_to_160"
	}
	if !DeepLink(in.DeepLink) {
		bad["deep_link"] = "must_be_a_path"
	}
	if model.TooLong(in.Key, model.MaxKey) {
		bad["idempotency_key"] = "up_to_64"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	// The token is signed and whitelisted, but the shelf is the lifetime: a
	// game the player removed has nothing to tell them.
	grant, err := b.Games.Grant(ctx, c.PlayerID, c.GameID)
	if err != nil {
		return nil, err
	}
	if !grant.InLibrary {
		return nil, errors.Unauthorized("invalid_token")
	}
	player, err := b.Players.ByID(ctx, c.PlayerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if player == nil || !player.CanSignIn() {
		return nil, errors.Unauthorized("invalid_token")
	}

	if b.Limiter != nil {
		hourly := b.NotifyHourly
		if hourly <= 0 {
			hourly = NotifyHourly
		}
		ok, retry, err := b.Limiter.Allow(ctx, "notify:"+c.GameID+":"+c.PlayerID, hourly, time.Hour)
		if err != nil {
			return nil, errors.Internal(err)
		}
		if !ok {
			return nil, errors.TooManyRequests(retry)
		}
	}
	if b.Inbox == nil {
		return nil, errors.Internal(errNoInbox)
	}
	return b.Inbox.Raise(ctx, inbox.Notice{
		PlayerID: c.PlayerID, Kind: enum.NoticeGame, GameID: c.GameID,
		Title: title, Body: body, DeepLink: in.DeepLink, Key: in.Key,
	})
}
