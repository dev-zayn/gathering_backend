// Package tournaments is the rules of a tournament: making one, its entrants
// coming and going, the draw that starts it, its matches played in rooms and
// counted round by round, deciding them (played, a bye, a walkover, an
// award), a league's table, finishing and cancelling, the clock that starts
// what is due and ends what nobody started, and the views the app, the
// console and a game's server read. docs/TOURNAMENTS.md is the contract. The
// rooms ask this package about a match room (rooms.Matches); this package
// asks the rooms for a match's room (Rooms) and to close it once the match is
// decided, always after its own change has committed.
package tournaments

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/rooms"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Config is what the environment decides about tournaments.
type Config struct {
	// Alphabet is ROOM_CODE_ALPHABET: a tournament's code is drawn from the
	// same symbols as a room's and read the same way.
	Alphabet string

	// MaxEntrants is TOURNAMENT_MAX_ENTRANTS, the largest knockout (64 at
	// most); MaxMembers is RELAY_MAX_MEMBERS, the largest league table.
	MaxEntrants int
	MaxMembers  int

	// OpenFor is TOURNAMENT_OPEN_FOR: how long a tournament with no start
	// time may sit open before the clock calls it off.
	OpenFor time.Duration

	// LinkURL is LINK_PUBLIC_URL: a tournament's link is LinkURL/t/CODE.
	LinkURL string

	// AvatarTTL is how long a signed link to an uploaded avatar lives.
	AvatarTTL time.Duration
}

// Service is every rule about tournaments, over Postgres.
type Service struct {
	db  *gorm.DB
	tx  *data.Tx
	cfg Config

	// alphabet is cfg.Alphabet, upper case.
	alphabet string

	// Rooms makes and joins a match's room, and closes it once the match is
	// decided. Nil plays nothing: the spec command, and tests about the
	// rules alone.
	Rooms Rooms

	// Friends says who may come into a friends tournament and who may be
	// invited; Inbox carries the notifications. Nil Friends is nobody's
	// friend; nil Inbox tells nobody.
	Friends Friends
	Inbox   Notifier

	// Files signs the links to avatars and game icons. Nil is a deployment
	// with bundled avatars only and no icons.
	Files files.Files

	// Audit is the tournaments area of the audit log: what an admin did from
	// the console. Nil writes nothing.
	Audit audit.Log
}

// New is the rules over one database. The collaborators are fields, set by
// the caller.
func New(db *gorm.DB, cfg Config) *Service {
	if cfg.MaxEntrants <= 0 || cfg.MaxEntrants > maxEntrants {
		cfg.MaxEntrants = maxEntrants
	}
	if cfg.MaxMembers <= 0 || cfg.MaxMembers > maxEntrants {
		cfg.MaxMembers = maxEntrants
	}
	if cfg.OpenFor <= 0 {
		cfg.OpenFor = 7 * 24 * time.Hour
	}
	cfg.LinkURL = strings.TrimRight(cfg.LinkURL, "/")
	return &Service{db: db, tx: data.NewTx(db), cfg: cfg, alphabet: strings.ToUpper(cfg.Alphabet)}
}

// Rooms is the rooms as a tournament needs them. *rooms.Service is one.
type Rooms interface {
	// Seat puts the player in the match's room: its open one, or a new one
	// with the player as its host.
	Seat(ctx context.Context, playerID string, in rooms.MatchSeat) (*rooms.Room, error)

	// CloseMatchRoom ends the match's open room, if it has one, with the
	// reason tournament.
	CloseMatchRoom(ctx context.Context, matchID string) error
}

// Friends is who may meet whom. *friends.Service is one.
type Friends interface {
	Among(ctx context.Context, playerID string, others []string) (map[string]bool, error)
}

// Notifier raises a notification in a player's inbox. *inbox.Inbox is one.
type Notifier interface {
	Raise(ctx context.Context, n inbox.Notice) (*inbox.Raised, error)
}

// Actor is who asks for a change: a player (the organizer, an entrant), or
// an admin from the console, who may act on any tournament.
type Actor struct {
	Player string
	Admin  string
}

// AsPlayer and AsAdmin name the actor.
func AsPlayer(id string) Actor { return Actor{Player: id} }
func AsAdmin(id string) Actor  { return Actor{Admin: id} }

// The refusals, one value each, named as docs/TOURNAMENTS.md names them.
var (
	errNotFound           = errors.NotFound("not_found")
	errTournamentNotFound = errors.NotFound("tournament_not_found")
	errClosed             = errors.Conflict("tournament_closed")
	errStarted            = errors.Conflict("tournament_started")
	errFull               = errors.Conflict("tournament_full")
	errRemoved            = errors.Forbidden("removed")
	errFriendsOnly        = errors.Forbidden("friends_only")
	errOrganizerOnly      = errors.Forbidden("organizer_only")
	errNotEntrant         = errors.Forbidden("not_entrant")
	errTooFew             = errors.Conflict("too_few_entrants")
	errNotRunning         = errors.Conflict("tournament_not_running")
	errNotInMatch         = errors.Forbidden("not_in_match")
	errMatchWaiting       = errors.Conflict("match_waiting")
	errMatchDecided       = errors.Conflict("match_decided")
	errNotKnockout        = errors.Conflict("not_knockout")
	errNotLeague          = errors.Conflict("not_league")
	errNothingPlayed      = errors.Conflict("nothing_played")
)

// asError keeps a refusal as it is and turns anything else into the one
// internal error: a transaction hands back whatever its body returned.
func asError(err error) error {
	var e *errors.Error
	if stderrors.As(err, &e) {
		return e
	}
	return errors.Internal(err)
}

// closeRooms has the rooms of decided matches closed once the change that
// decided them has committed: a socket must never hear of a decision that
// rolled back. A room that cannot be closed is logged; it ends with the
// reason tournament on its next round, which the tournament no longer
// counts.
func (s *Service) closeRooms(ctx context.Context, matchIDs []string) {
	if s.Rooms == nil || len(matchIDs) == 0 {
		return
	}
	ids := append([]string(nil), matchIDs...)
	data.AfterCommit(ctx, func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(data.Outside(ctx)), 10*time.Second)
		defer cancel()
		for _, id := range ids {
			if err := s.Rooms.CloseMatchRoom(cctx, id); err != nil {
				applog.From(ctx).Warn("match room not closed", "match", id, "err", err)
			}
		}
	})
}

// record writes an audit line for a change already made. A log that is down
// must not undo the change or refuse the admin, so a failure is logged.
func (s *Service) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Write(ctx, model.Entry{Actor: actor, Action: action, Subject: subject, Detail: detail}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "subject", subject, "err", err)
	}
}
