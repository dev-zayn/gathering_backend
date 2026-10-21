package identity

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingLimiter is a fixed-window counter, like the Redis one. Nothing here
// waits, so a key that runs out stays out.
type countingLimiter struct {
	mu   sync.Mutex
	seen map[string]int
}

func (l *countingLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]int{}
	}
	l.seen[key]++
	return l.seen[key] <= limit, 30, nil
}

// countingSender is the vendor, and the count is the bill.
type countingSender struct {
	mu    sync.Mutex
	sent  []string
	codes []string
}

func (s *countingSender) Send(_ context.Context, phone, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, phone)
	s.codes = append(s.codes, code)
	return nil
}

// Notify counts like Send: a notice is a message too.
func (s *countingSender) Notify(_ context.Context, phone, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, phone)
	return nil
}

type nopStore struct{}

func (nopStore) Put(context.Context, string, string, time.Duration) error     { return nil }
func (nopStore) Replace(context.Context, string, string, time.Duration) error { return nil }
func (nopStore) Get(context.Context, string) (Pending, bool, error)           { return Pending{}, false, nil }
func (nopStore) Attempt(context.Context, string) (int, error)                 { return 0, nil }
func (nopStore) Clear(context.Context, string) error                          { return nil }

type stubMinter struct{ Minter }

func (stubMinter) MintOTP(string, time.Duration) (string, error) { return "otp-token", nil }

func newSignIn(ceiling int) (*SignIn, *countingSender, *countingLimiter) {
	sender := &countingSender{}
	limiter := &countingLimiter{}
	return &SignIn{
		Codes:   nopStore{},
		Sender:  sender,
		Minter:  stubMinter{},
		Limiter: limiter,
		Policy: SignInPolicy{
			CodeTTL: 5 * time.Minute, TokenTTL: 15 * time.Minute,
			Cooldown: time.Minute, Hourly: 5, Resends: 2, Ceiling: ceiling,
		},
	}, sender, limiter
}

// A number nobody has asked about yet, so the per-number buckets are empty and
// only the deployment-wide one can refuse it.
func iraqiNumber(i int) string { return fmt.Sprintf("07%09d", i) }

// The attack the per-number limits cannot see: a caller with a list of numbers
// pays nothing to any one bucket, and every request costs a message.
func TestRotatingNumbersCannotOutrunTheCeiling(t *testing.T) {
	const ceiling = 10
	s, sender, _ := newSignIn(ceiling)

	var refused int
	for i := range 50 {
		if _, err := s.Start(context.Background(), iraqiNumber(i)); err != nil {
			if !strings.Contains(err.Error(), "too_many") {
				t.Fatalf("number %d: unexpected error %v", i, err)
			}
			refused++
		}
	}

	if len(sender.sent) != ceiling {
		t.Errorf("vendor was asked %d times, ceiling is %d", len(sender.sent), ceiling)
	}
	if refused != 50-ceiling {
		t.Errorf("refused %d of 50, want %d", refused, 50-ceiling)
	}
}

// Hammering one number is already refused per-number, and must not spend the
// shared allowance: one caller would lock everybody else out.
func TestOneNumberCannotSpendEverybodyElsesAllowance(t *testing.T) {
	s, sender, limiter := newSignIn(10)

	for range 20 {
		_, _ = s.Start(context.Background(), iraqiNumber(1))
	}

	// One message: the cooldown refuses the other nineteen.
	if len(sender.sent) != 1 {
		t.Errorf("vendor was asked %d times for one number, want 1", len(sender.sent))
	}
	if got := limiter.seen["otp:ceiling"]; got != 1 {
		t.Errorf("the shared bucket was spent %d times, want 1 — it counts messages, not attempts", got)
	}
}

// Every door reaches the vendor through the same Start, which is why the
// limit is here rather than on a route.
func TestTheCeilingCoversEveryCallerOfStart(t *testing.T) {
	s, sender, _ := newSignIn(3)

	for i := range 10 {
		_, _ = s.Start(context.Background(), iraqiNumber(100+i))
	}
	if len(sender.sent) != 3 {
		t.Errorf("vendor was asked %d times, want 3", len(sender.sent))
	}
}

// Zero is off, so a test that is not about the ceiling does not have to know it
// exists.
func TestZeroCeilingIsNoCeiling(t *testing.T) {
	s, sender, limiter := newSignIn(0)

	for i := range 30 {
		_, _ = s.Start(context.Background(), iraqiNumber(200+i))
	}
	if len(sender.sent) != 30 {
		t.Errorf("vendor was asked %d times, want 30", len(sender.sent))
	}
	if _, ok := limiter.seen["otp:ceiling"]; ok {
		t.Error("the shared bucket was touched with the ceiling switched off")
	}
}

// A laptop may pin the code, so a sweep can sign in without reading the log.
// Unpinned, every code is six fresh digits.
func TestAPinnedCodeIsWhatIsSent(t *testing.T) {
	s, sender, _ := newSignIn(0)

	if _, err := s.Start(context.Background(), iraqiNumber(300)); err != nil {
		t.Fatal(err)
	}
	if got := sender.codes[0]; len(got) != 6 || strings.Trim(got, "0123456789") != "" {
		t.Fatalf("a random code came out as %q", got)
	}

	s.Fixed = "123456"
	if _, err := s.Start(context.Background(), iraqiNumber(301)); err != nil {
		t.Fatal(err)
	}
	if got := sender.codes[1]; got != "123456" {
		t.Fatalf("the pinned code came out as %q", got)
	}
}
