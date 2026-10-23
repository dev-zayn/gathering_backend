package guests

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/data"
	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// refusal is the API's error inside err, or nil.
func refusal(err error) *apperr.Error {
	var e *apperr.Error
	if as(err, &e) {
		return e
	}
	return nil
}

// countingLimiter is a fixed window that never ends: every call counts, and
// the window has retryAfter seconds left.
type countingLimiter struct {
	mu         sync.Mutex
	counts     map[string]int
	keys       []string
	retryAfter int
}

func (l *countingLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil {
		l.counts = map[string]int{}
	}
	l.counts[key]++
	l.keys = append(l.keys, key)
	if l.counts[key] > limit {
		return false, l.retryAfter, nil
	}
	return true, 0, nil
}

// notifyBridge is the bridge over the test's database with an inbox and a
// limiter that counts.
func notifyBridge(t *testing.T) (*Bridge, *inbox.Inbox, *countingLimiter, string) {
	t.Helper()
	b, _, player, db := testBridge(t)
	in := inbox.New(db)
	limiter := &countingLimiter{retryAfter: 1800}
	b.Inbox, b.Limiter = in, limiter
	return b, in, limiter, player.ID
}

func TestAGameRaisesANotification(t *testing.T) {
	b, in, limiter, playerID := notifyBridge(t)
	ctx := context.Background()
	c := asking(playerID, ScopeNotifySend)

	raised, err := b.Notify(ctx, c, Notice{Title: "  دورك يا بطل  ", Body: "مريم لعبت، دورك", DeepLink: "/match/42"})
	if err != nil || !raised.Created || !ids.Is(ids.Notification, raised.ID) {
		t.Fatalf("raise: %+v %v", raised, err)
	}
	feed, err := in.Feed(ctx, playerID, "", 20, false)
	if err != nil || len(feed.Items) != 1 {
		t.Fatalf("the inbox: %+v %v", feed, err)
	}
	n := feed.Items[0]
	if n.ID != raised.ID || n.Kind != "game" || n.Title != "دورك يا بطل" || n.Body != "مريم لعبت، دورك" ||
		n.DeepLink != "/match/42" || n.Actor != nil || n.Game == nil || n.Game.ID != xo || len(n.Data) != 0 || n.Read {
		t.Fatalf("the notification: %+v", n)
	}
	if len(limiter.keys) != 1 || limiter.keys[0] != "notify:"+xo+":"+playerID {
		t.Fatalf("counted under %v", limiter.keys)
	}
}

// A key already used for this player answers that notification, updated in
// place: back at the top, unread.
func TestAGamesKeyIsIdempotent(t *testing.T) {
	b, in, _, playerID := notifyBridge(t)
	ctx := context.Background()
	c := asking(playerID, ScopeNotifySend)

	first, err := b.Notify(ctx, c, Notice{Title: "دورك", Key: "turn:42"})
	if err != nil || !first.Created {
		t.Fatalf("first: %+v %v", first, err)
	}
	if err := in.Read(ctx, playerID, []string{first.ID}, false); err != nil {
		t.Fatal(err)
	}
	again, err := b.Notify(ctx, c, Notice{Title: "دورك مرة ثانية", Key: "turn:42"})
	if err != nil || again.Created || again.ID != first.ID {
		t.Fatalf("again: %+v %v", again, err)
	}
	other, err := b.Notify(ctx, c, Notice{Title: "مباراة ثانية", Key: "turn:43"})
	if err != nil || !other.Created || other.ID == first.ID {
		t.Fatalf("another key: %+v %v", other, err)
	}
	feed, _ := in.Feed(ctx, playerID, "", 20, false)
	if len(feed.Items) != 2 || feed.Unread != 2 {
		t.Fatalf("the inbox: %+v", feed)
	}
	for _, n := range feed.Items {
		if n.ID == first.ID && (n.Read || n.Title != "دورك مرة ثانية") {
			t.Fatalf("the reused key's line: %+v", n)
		}
	}
}

func TestANotificationIsRefused(t *testing.T) {
	b, in, limiter, playerID := notifyBridge(t)
	ctx := context.Background()

	_, err := b.Notify(ctx, asking(playerID, ScopeIdentityRead), Notice{Title: "دورك"})
	if e := refusal(err); e == nil || e.Status != 403 || e.Code != "scope_required" || e.Scope != ScopeNotifySend {
		t.Fatalf("without notify:send: %v", err)
	}

	// Every problem at once, before anything is counted.
	_, err = b.Notify(ctx, asking(playerID, ScopeNotifySend), Notice{
		Title: "   ", Body: strings.Repeat("ب", 161), DeepLink: "https://elsewhere.example/", Key: strings.Repeat("k", 65),
	})
	e := refusal(err)
	if e == nil || e.Code != "invalid_fields" || e.Fields["title"] != "1_to_60" || e.Fields["body"] != "up_to_160" ||
		e.Fields["deep_link"] != "must_be_a_path" || e.Fields["idempotency_key"] != "up_to_64" {
		t.Fatalf("the fields: %+v", e)
	}
	_, err = b.Notify(ctx, asking(playerID, ScopeNotifySend), Notice{Title: strings.Repeat("ع", 61), DeepLink: "//elsewhere.example"})
	if e := refusal(err); e == nil || e.Fields["title"] != "1_to_60" || e.Fields["deep_link"] != "must_be_a_path" {
		t.Fatalf("61 characters and a host: %+v", e)
	}
	if len(limiter.keys) != 0 {
		t.Fatalf("a refused notice was counted: %v", limiter.keys)
	}
	// Sixty Arabic letters are sixty characters, not a hundred and twenty bytes.
	if _, err := b.Notify(ctx, asking(playerID, ScopeNotifySend), Notice{Title: strings.Repeat("ع", 60), Body: strings.Repeat("ب", 160)}); err != nil {
		t.Fatalf("at the limits: %v", err)
	}

	// A game off the shelf has nobody to tell.
	if err := b.Games.Remove(ctx, playerID, xo); err != nil {
		t.Fatal(err)
	}
	_, err = b.Notify(ctx, asking(playerID, ScopeNotifySend), Notice{Title: "دورك"})
	if e := refusal(err); e == nil || e.Status != 401 || e.Code != "invalid_token" {
		t.Fatalf("off the shelf: %v", err)
	}
	if feed, _ := in.Feed(ctx, playerID, "", 20, false); len(feed.Items) != 1 {
		t.Fatalf("the inbox holds %d", len(feed.Items))
	}
}

// Ten a player an hour for each game, every call counting, a reused key
// included; the eleventh is told when to come back.
func TestTenAnHour(t *testing.T) {
	b, in, _, playerID := notifyBridge(t)
	ctx := context.Background()
	c := asking(playerID, ScopeNotifySend)
	for i := range 10 {
		if _, err := b.Notify(ctx, c, Notice{Title: "دورك", Key: "same"}); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	_, err := b.Notify(ctx, c, Notice{Title: "دورك", Key: "another"})
	if e := refusal(err); e == nil || e.Status != 429 || e.Code != "too_many_requests" || e.RetryAfter != 1800 {
		t.Fatalf("the eleventh: %+v", e)
	}
	if feed, _ := in.Feed(ctx, playerID, "", 20, false); len(feed.Items) != 1 {
		t.Fatalf("the inbox holds %d", len(feed.Items))
	}

	// The allowance is the bridge's to set.
	b.NotifyHourly = 12
	if _, err := b.Notify(ctx, c, Notice{Title: "دورك"}); err != nil {
		t.Fatalf("with twelve: %v", err)
	}
}

// The real limiter, in Redis: a fixed window an hour long, which says how
// long is left. Skips without TEST_REDIS_URL.
func TestTenAnHourInRedis(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	rdb, err := data.OpenRedis(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	b, _, _, playerID := notifyBridge(t)
	b.Limiter = identity.NewLimiter(rdb)
	ctx := context.Background()
	c := asking(playerID, ScopeNotifySend)
	t.Cleanup(func() { rdb.Del(context.Background(), "notify:"+xo+":"+playerID) })
	for i := range 10 {
		if _, err := b.Notify(ctx, c, Notice{Title: "دورك"}); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	_, err = b.Notify(ctx, c, Notice{Title: "دورك"})
	if e := refusal(err); e == nil || e.Status != 429 || e.RetryAfter < 3500 || e.RetryAfter > 3600 {
		t.Fatalf("the eleventh: %+v", e)
	}
}
