package friends

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// Service is every rule about friends. Its store is Postgres; who is online
// is Presence's, and the notifications a request and an accept raise are
// the inbox's.
type Service struct {
	db *gorm.DB
	tx *data.Tx

	Players *identity.PlayerStore
	Inbox   *inbox.Inbox

	// Presence says who is here and in which room. Nil is everybody offline.
	Presence Here

	// Files signs the avatars and the game icons on the way out; AvatarTTL
	// is how long an avatar's link lives.
	Files     files.Files
	AvatarTTL time.Duration
}

// New is the rules over one database, the stores they read and the inbox
// they raise into. The rest is set by the caller.
func New(db *gorm.DB, players *identity.PlayerStore, in *inbox.Inbox) *Service {
	return &Service{db: db, tx: data.NewTx(db), Players: players, Inbox: in}
}

// Here is who is here right now, and in which room. *players.Presence is
// one.
type Here interface {
	Of(ctx context.Context, ids []string) (map[string]players.State, error)
}

// Where one player stands with another, as the app reads it.
const (
	None      = "none"
	Requested = "requested" // I asked
	Incoming  = "incoming"  // they asked
	Friends   = "friends"
	Blocked   = "blocked" // I blocked them, whatever they did
)

// The refusals, one value each, named as docs/FRIENDS.md names them.
var (
	errNotFound        = errors.NotFound("not_found")
	errRequestNotFound = errors.NotFound("request_not_found")
	errNotFriends      = errors.NotFound("not_friends")
	errAlreadyFriends  = errors.Conflict("already_friends")
	errRequestPending  = errors.Conflict("request_pending")
	errBlocked         = errors.Forbidden("blocked")
	errIsYou           = errors.Invalid(map[string]string{"player": "is_you"})
)

// The notifications a request and an accept raise, and their keys: one per
// asker in the other's inbox, so asking again brings the same one back.
const (
	requestTitle  = "طلب صداقة"
	requestBody   = " يريد يصير صديقك"
	acceptedTitle = "صرتوا أصدقاء"
	acceptedBody  = " قبل طلب صداقتك"
)

func requestKey(asker string) string     { return "friend_request:" + asker }
func acceptedKey(accepter string) string { return "friend_accepted:" + accepter }

// asError keeps a refusal as it is and turns anything else into the one
// internal error: a transaction hands back whatever its body returned.
func asError(err error) error {
	var e *errors.Error
	if stderrors.As(err, &e) {
		return e
	}
	return errors.Internal(err)
}

// target is a player the caller names by id: there, and not switched off by
// an admin. Anything else is nobody.
func (s *Service) target(ctx context.Context, id string) (*model.Player, error) {
	if !ids.Is(ids.Player, id) {
		return nil, errNotFound
	}
	p, err := s.Players.ByID(ctx, id)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if p == nil || p.Disabled {
		return nil, errNotFound
	}
	return p, nil
}

// me is the caller's own record: their nickname goes into what they raise.
func (s *Service) me(ctx context.Context, id string) (*model.Player, error) {
	p, err := s.Players.ByID(ctx, id)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if p == nil {
		return nil, errors.Unauthorized("invalid_token")
	}
	return p, nil
}

// card draws a row's player, the picture signed on the way out.
func (s *Service) card(ctx context.Context, r cardRow) players.Card {
	return players.NewCard(ctx, s.Files, s.AvatarTTL, r.ID, r.Nickname, r.Handle, r.AvatarKey)
}

// byName orders two names the way a list reads them: case aside, then as
// written.
func byName(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// stamp is the one timestamp format this API answers in: UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
