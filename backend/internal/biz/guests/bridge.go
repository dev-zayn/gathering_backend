package guests

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
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

	// Rooms describes the room a token names. Nil is a room that is never
	// described.
	Rooms Rooms

	// Inbox is where a game's notifications go; Limiter holds each game to
	// NotifyHourly a player an hour (NotifyHourly when zero). Nil Limiter
	// limits nothing, which only a test wants.
	Inbox        Notifier
	Limiter      identity.Limiter
	NotifyHourly int
}

// errNoInbox is a bridge wired without an inbox asked to raise into one.
var errNoInbox = stderrors.New("guests: the bridge has no inbox")

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

	// Room is the room the game was opened into, while the player is still a
	// current member of it. Nil otherwise: a game opened outside a room, or a
	// player who has since left it.
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

// RoomSummary is the room as a game's server sees it: which room, where it
// stands and which round it is on, the player's place in it, and who else is
// there.
type RoomSummary struct {
	ID      string
	Code    string
	Status  string
	Round   int
	Role    string
	Seat    int
	Members []RoomMember

	// Tournament is the match the room plays (docs/TOURNAMENTS.md): ids and
	// numbers, no names. Nil for any other room.
	Tournament *rooms.MatchBrief
}

// RoomMember is one current member. The nickname is there with
// identity:read only, like the player's own.
type RoomMember struct {
	ID       string
	Nickname string
	Role     string
	Seat     int
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

	if c.RoomID != "" && b.Rooms != nil {
		brief, err := b.Rooms.Brief(ctx, c.RoomID, c.PlayerID)
		if err != nil {
			return nil, err
		}
		if brief != nil {
			out.Room = summary(brief, c.has(ScopeIdentityRead))
		}
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

// ResultLine is one line of a room's results as a game's server reads it:
// the name and the picture with identity:read only, like everything else on
// the bridge.
type ResultLine struct {
	Player   string
	Nickname string

	// Avatar is a bundled avatar's id; AvatarURL a signed link to an
	// uploaded picture. One of them, or neither.
	Avatar    string
	AvatarURL string

	Score       float64
	Rank        int
	SubmittedBy string
}

// Written is the answer to a game's server writing results: the round it
// wrote, and that round's lines as they now stand.
type Written struct {
	Round int
	Lines []ResultLine
}

// Results is the game's server writing a round's results. The room is the
// body's, not the token's: any of this game's tokens for a player who was in
// the room will do. round is the body's too: nil is the room's own round, the
// one playing or the one just finished. The route already refuses a token
// without results:write; asking again here keeps the rule with the bridge
// rather than the route.
func (b *Bridge) Results(ctx context.Context, c Caller, roomID string, round *int, scores []rooms.ScoreIn) (*Written, error) {
	if !c.has(ScopeResultsWrite) {
		return nil, errors.ScopeRequired(ScopeResultsWrite)
	}
	if b.Rooms == nil {
		return nil, errors.NotFound("not_found")
	}
	written, err := b.Rooms.SubmitResults(ctx, rooms.Submission{
		GameID: c.GameID, PlayerID: c.PlayerID, RoomID: roomID, Round: round, Scores: scores,
	})
	if err != nil {
		return nil, err
	}

	names := c.has(ScopeIdentityRead)
	out := &Written{Round: written.Round, Lines: make([]ResultLine, 0, len(written.Results))}
	for _, r := range written.Results {
		line := ResultLine{Player: r.Player, Score: r.Score, Rank: r.Rank, SubmittedBy: r.SubmittedBy}
		if names {
			line.Nickname, line.Avatar = r.Nickname, r.Avatar
			if r.AvatarURL != nil {
				line.AvatarURL = *r.AvatarURL
			}
		}
		out.Lines = append(out.Lines, line)
	}
	return out, nil
}

// summary is the room as the bridge hands it over: the nicknames with
// identity:read only.
func summary(b *rooms.Brief, names bool) *RoomSummary {
	out := &RoomSummary{
		ID: b.ID, Code: b.Code, Status: b.Status, Round: b.Round, Role: b.Role, Seat: b.Seat,
		Tournament: b.Tournament,
	}
	out.Members = make([]RoomMember, 0, len(b.Members))
	for _, m := range b.Members {
		member := RoomMember{ID: m.ID, Role: m.Role, Seat: m.Seat}
		if names {
			member.Nickname = m.Nickname
		}
		out.Members = append(out.Members, member)
	}
	return out
}
