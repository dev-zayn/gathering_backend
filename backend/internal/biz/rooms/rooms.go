package rooms

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/pkg/files"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Config is what the environment decides about rooms.
type Config struct {
	// Alphabet is ROOM_CODE_ALPHABET: the symbols a code is drawn from, upper
	// case, none that read alike.
	Alphabet string

	// MaxMembers is RELAY_MAX_MEMBERS. The manifest check already caps a
	// room at 64; a deployment may cap it lower, and a room is made under
	// the lower of the two.
	MaxMembers int

	// LobbyIdle is how long a lobby with nobody connected may sit before the
	// sweeper ends it; MaxPlay how long a game may run; Retention how long an
	// ended room is kept before it is deleted with its members.
	LobbyIdle time.Duration
	MaxPlay   time.Duration
	Retention time.Duration

	// TicketTTL is TOKEN_ROOM_TTL: how long a room ticket lives.
	TicketTTL time.Duration

	// LinkURL is LINK_PUBLIC_URL, where an invite link lives: LinkURL/r/CODE.
	LinkURL string

	// AvatarTTL is how long a signed link to an uploaded avatar lives.
	AvatarTTL time.Duration
}

// Service is every rule about rooms. Its stores are Postgres; the live side
// of a room (the frame counter, the history, the shared state) is the
// relay's, reached through Publisher.
type Service struct {
	db  *gorm.DB
	tx  *data.Tx
	cfg Config

	// alphabet is cfg.Alphabet, upper case, as bytes: what a code is drawn
	// from and checked against.
	alphabet string

	// Publisher carries every change to the room's sockets. Nil publishes
	// nothing: the spec command, and tests about the rules alone.
	Publisher Publisher

	// Presence says which members have a socket open: Member.connected, and
	// whether the sweeper may call a lobby idle. Nil is nobody connected.
	Presence Presence

	// Minter signs room tickets.
	Minter identity.Minter

	// Files signs the links to uploaded avatars and game icons. Nil is a
	// deployment with bundled avatars only and no icons.
	Files files.Files

	// Friends says who may come into a friends-only room and who may be
	// invited; Inbox carries the invites. Nil Friends is nobody's friend;
	// nil Inbox invites without telling anybody.
	Friends Friends
	Inbox   Notifier

	// Audit is the rooms area of the audit log: what an admin did to a room
	// from the console. Nil writes nothing.
	Audit audit.Log

	// Matches is the tournaments: who may sit in a match room, and what a
	// round of one decides (docs/TOURNAMENTS.md). Nil is a platform where
	// every room is an ordinary one, and a match room lets nobody in.
	Matches Matches
}

// New is the rules over one database. The collaborators are fields, set by
// the caller: the relay is built after this, from this.
func New(db *gorm.DB, cfg Config) *Service {
	if cfg.Retention == 0 {
		cfg.Retention = 24 * time.Hour
	}
	if cfg.MaxMembers <= 0 {
		cfg.MaxMembers = 64
	}
	cfg.LinkURL = strings.TrimRight(cfg.LinkURL, "/")
	return &Service{db: db, tx: data.NewTx(db), cfg: cfg, alphabet: strings.ToUpper(cfg.Alphabet)}
}

// Publisher is the relay as the rules see it: where a room's frames go.
type Publisher interface {
	// Publish appends frames to the room's history, in order, each with the
	// next seq, and delivers them to every replica's sockets.
	Publish(ctx context.Context, roomID string, frames ...any) error

	// Start resets the room's shared state to version 0 with no data and
	// publishes the started frame, in one step: every round begins afresh,
	// and no write based on the last round's state lands in between.
	Start(ctx context.Context, roomID string, started Started) error

	// End publishes the ended frame, then drops the room's live keys: the
	// counter, the history and the state. Every hub closes the room's
	// sockets with 4004 once the frame is delivered.
	End(ctx context.Context, roomID string, ended Ended) error

	// State is the room's live shared state: version 0 and no data until the
	// first write.
	State(ctx context.Context, roomID string) (StateView, error)
}

// Presence is who has a socket open right now.
type Presence interface {
	Of(ctx context.Context, ids []string) (map[string]players.State, error)
}

// publishTimeout bounds a publish that outlives the request that caused it.
const publishTimeout = 5 * time.Second

// publish sends frames once the change behind them is committed. A failure
// is logged, not returned: the change happened, and a socket that missed the
// frames catches up from hello when it reconnects. The request may already
// be gone, so the publish carries on without it.
func (s *Service) publish(ctx context.Context, roomID string, frames ...any) {
	if s.Publisher == nil || len(frames) == 0 {
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	if err := s.Publisher.Publish(pctx, roomID, frames...); err != nil {
		applog.From(ctx).Warn("room frames not published", "room", roomID, "err", err)
	}
}

// start has the relay reset the room's state and publish the start of a
// round. A failure is logged like any publish: the round has begun.
func (s *Service) start(ctx context.Context, roomID string, started Started) {
	if s.Publisher == nil {
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	if err := s.Publisher.Start(pctx, roomID, started); err != nil {
		applog.From(ctx).Warn("round start not published", "room", roomID, "err", err)
	}
}

// end publishes a room's end and lets the relay close its sockets.
func (s *Service) end(ctx context.Context, roomID string, ended Ended) {
	if s.Publisher == nil {
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	if err := s.Publisher.End(pctx, roomID, ended); err != nil {
		applog.From(ctx).Warn("room end not published", "room", roomID, "err", err)
	}
}

// liveState is the state the relay holds, or nothing when there is no relay
// or it cannot be read. Ending a room copies it; a failure to read it must
// not stop the end.
func (s *Service) liveState(ctx context.Context, roomID string) (StateView, bool) {
	if s.Publisher == nil {
		return StateView{}, false
	}
	st, err := s.Publisher.State(ctx, roomID)
	if err != nil {
		applog.From(ctx).Warn("room state not read", "room", roomID, "err", err)
		return StateView{}, false
	}
	return st, true
}

// rawOrNil keeps "never written" apart from a written null.
func rawOrNil(b json.RawMessage) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
