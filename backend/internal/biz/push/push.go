package push

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/fcm"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Sender sends one message to one phone. *fcm.Client is one; its refusals
// are *fcm.Error.
type Sender interface {
	Send(ctx context.Context, m fcm.Message) error
}

// Devices is the phones a push may reach, and forgetting a dead one.
// *identity.Devices is one.
type Devices interface {
	// Live is the players' phones on sessions that have not ended, of
	// players not switched off.
	Live(ctx context.Context, playerIDs []string, now time.Time) ([]model.Device, error)
	// Drop forgets phones by id.
	Drop(ctx context.Context, ids ...string) error
}

// The pool's shape.
const (
	// Workers is how many sends run at once in one process; an
	// announcement to everybody goes through the same eight.
	Workers = 8
	// queued is how many hand-overs may wait; past it a push is dropped,
	// never waited for.
	queued = 4096
	// chunk is how many notifications are read at a time. Between chunks of
	// a big hand-over, one that arrived meanwhile goes first, so a friend's
	// request is not stuck behind an announcement to everybody.
	chunk = 100
	// backoff is the wait before the one retry of a 429 or a 5xx.
	backoff = time.Second
)

// Options are the pool's settings a test changes; zero is the default.
type Options struct {
	Workers int
	Backoff time.Duration
}

// Service is the pool: one planner reading notifications and their phones,
// and Workers senders. Without a Sender it sends nothing and writes a
// "would push" line per notification instead.
type Service struct {
	db      *gorm.DB
	sender  Sender
	devices Devices
	log     *applog.Logger
	backoff time.Duration

	// ctx is every read's and send's. Cancelled by a Close that runs out of
	// time, which abandons whatever is left.
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	queue  chan []string

	sends chan delivery
	done  sync.WaitGroup
}

// delivery is one message to one phone. The token rides in the message and
// nowhere else: what is logged is the device's id.
type delivery struct {
	notification string
	device       string
	message      fcm.Message
}

// New starts the pool. sender nil is a deployment without a Firebase key.
func New(db *gorm.DB, sender Sender, devices Devices, log *applog.Logger, o Options) *Service {
	workers := o.Workers
	if workers <= 0 {
		workers = Workers
	}
	if log == nil {
		log = applog.From(context.Background())
	}
	s := &Service{
		db: db, sender: sender, devices: devices, log: log, backoff: o.Backoff,
		queue: make(chan []string, queued),
		sends: make(chan delivery),
	}
	if s.backoff <= 0 {
		s.backoff = backoff
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.done.Add(1 + workers)
	go s.plan()
	for range workers {
		go s.work()
	}
	return s
}

// Push takes notifications whose write has committed. It never waits: past
// the queue's room, or after Close, the push is dropped with a log line, and
// the inbox still has the row.
func (s *Service) Push(ids ...string) {
	if len(ids) == 0 {
		return
	}
	ids = append([]string(nil), ids...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.log.Warn("push after shutdown, dropped", "notification", ids[0], "notifications", len(ids))
		return
	}
	select {
	case s.queue <- ids:
	default:
		s.log.Error("push queue full, dropped", "notification", ids[0], "notifications", len(ids))
	}
}

// Close takes no more and finishes what is queued, until ctx ends; then the
// rest is abandoned and every send in flight cancelled. It returns once the
// pool's goroutines have.
func (s *Service) Close(ctx context.Context) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.queue)
	s.mu.Unlock()

	finished := make(chan struct{})
	go func() {
		s.done.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
		s.cancel()
		<-finished
	}
	s.cancel()
}

// plan reads what was handed over, a chunk at a time, and gives the workers
// one message per live phone. A hand-over waiting in the queue goes before
// the rest of a big one.
func (s *Service) plan() {
	defer s.done.Done()
	defer close(s.sends)

	queue := s.queue
	var later [][]string
	for s.ctx.Err() == nil {
		var ids []string
		select {
		case got, open := <-queue:
			if !open {
				queue = nil // closed: what is left is later's
				continue
			}
			ids = got
		default:
			switch {
			case len(later) > 0:
				ids, later = later[0], later[1:]
			case queue == nil:
				return
			default:
				select {
				case got, open := <-queue:
					if !open {
						queue = nil
						continue
					}
					ids = got
				case <-s.ctx.Done():
					return
				}
			}
		}
		if len(ids) > chunk {
			later = append(later, ids[chunk:])
			ids = ids[:chunk]
		}
		s.deliver(ids)
	}
}

// deliver reads the notifications, their recipients' live phones and unread
// counts, and hands one message per phone to the workers. A notification
// deleted since, or for a player switched off, has nothing to push.
func (s *Service) deliver(ids []string) {
	notices, err := s.notices(ids)
	if err != nil {
		if s.ctx.Err() == nil {
			s.log.Error("push: notifications not read", "notification", ids[0], "notifications", len(ids), "err", err)
		}
		return
	}
	if len(notices) == 0 {
		return
	}
	players := make([]string, 0, len(notices))
	seen := make(map[string]bool, len(notices))
	for _, n := range notices {
		if !seen[n.PlayerID] {
			seen[n.PlayerID] = true
			players = append(players, n.PlayerID)
		}
	}
	phones, err := s.devices.Live(s.ctx, players, time.Now())
	if err != nil {
		if s.ctx.Err() == nil {
			s.log.Error("push: phones not read", "notification", ids[0], "notifications", len(ids), "err", err)
		}
		return
	}
	byPlayer := make(map[string][]model.Device, len(players))
	for _, d := range phones {
		byPlayer[d.PlayerID] = append(byPlayer[d.PlayerID], d)
	}

	for _, n := range notices {
		mine := byPlayer[n.PlayerID]
		if s.sender == nil {
			s.log.Info("would push", "notification", n.ID, "devices", len(mine))
			continue
		}
		for _, d := range mine {
			select {
			case s.sends <- delivery{notification: n.ID, device: d.ID, message: Message(n, d.FCMToken)}:
			case <-s.ctx.Done():
				return
			}
		}
	}
}

// notices is the notifications named, each with its recipient's unread
// count, leaving out those of players switched off.
func (s *Service) notices(ids []string) ([]Notice, error) {
	var out []Notice
	err := s.db.WithContext(s.ctx).Raw(`
		SELECT n.id, n.player_id, n.kind, n.game_id, n.title, n.body, n.deep_link, n.data, n.key,
		       (SELECT count(*) FROM notifications u WHERE u.player_id = n.player_id AND u.read_at IS NULL) AS unread
		FROM notifications n
		JOIN players p ON p.id = n.player_id
		WHERE n.id IN ? AND NOT p.disabled
		ORDER BY n.created_at, n.id`, ids).Scan(&out).Error
	return out, err
}

// work sends what the planner hands it until the planner is done.
func (s *Service) work() {
	defer s.done.Done()
	for d := range s.sends {
		s.send(d)
	}
}

// send sends one message. A 429 or a 5xx is tried once more after a short
// wait; a token Firebase says is gone takes its phone with it; anything else
// is logged by the device's id, never its token, and dropped.
func (s *Service) send(d delivery) {
	err := s.sender.Send(s.ctx, d.message)
	var refused *fcm.Error
	if errors.As(err, &refused) && refused.Temporary() {
		wait := time.NewTimer(s.backoff)
		select {
		case <-wait.C:
		case <-s.ctx.Done():
			wait.Stop()
			return
		}
		err = s.sender.Send(s.ctx, d.message)
	}

	switch {
	case err == nil:
		// Firebase took it: what reaches the phone is Firebase's business
		// from here, and this line is the platform's word that it was sent.
		s.log.Info("push sent", "notification", d.notification, "device", d.device)
	case s.ctx.Err() != nil:
		// Shutting down: this one is abandoned with the rest.
	case errors.As(err, &refused) && refused.Gone():
		if err := s.devices.Drop(s.ctx, d.device); err != nil {
			s.log.Error("push: a dead phone not forgotten", "device", d.device, "err", err)
			return
		}
		s.log.Info("push: the phone is gone, forgotten", "device", d.device, "notification", d.notification, "code", refused.Code)
	default:
		s.log.Warn("push failed", "device", d.device, "notification", d.notification, "err", err)
	}
}
