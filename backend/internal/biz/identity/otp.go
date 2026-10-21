package identity

// The outside things sign-in depends on: an interface here, a real
// implementation in internal/pkg.

import (
	"context"

	"math"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// OTPSender hands a code to the vendor. A nil error means the message is on its
// way, not that it arrived.
type OTPSender interface {
	Send(ctx context.Context, phone, code string) error
}

// Pending is a code waiting to be typed.
type Pending struct {
	Hash     string
	Attempts int
}

// OTPStore holds pending codes for as long as they are worth holding. A missing
// code and an expired one are the same answer: found is false.
type OTPStore interface {
	// Put starts a fresh attempt: new code, wrong-guess counter back to zero.
	Put(ctx context.Context, phone, hash string, ttl time.Duration) error
	// Replace is a resend. Same attempt, so the wrong-guess counter survives.
	Replace(ctx context.Context, phone, hash string, ttl time.Duration) error
	Get(ctx context.Context, phone string) (Pending, bool, error)
	// Attempt counts a guess before it is compared and returns how many there
	// have now been, this one included.
	Attempt(ctx context.Context, phone string) (int, error)
	Clear(ctx context.Context, phone string) error
}

type redisOTP struct{ c *goredis.Client }

func NewOTPStore(c *goredis.Client) OTPStore { return &redisOTP{c: c} }

// The braces are a Redis hash tag: a cluster places both keys by the phone
// alone, so they share a slot and Put and Clear may touch them together.
func codeKey(phone string) string  { return "otp:{" + phone + "}:code" }
func triesKey(phone string) string { return "otp:{" + phone + "}:tries" }

// Put replaces any code already pending for that number. Asking for a new code
// invalidates the old one, so two codes are never live at once.
func (r *redisOTP) Put(ctx context.Context, phone, hash string, ttl time.Duration) error {
	pipe := r.c.TxPipeline()
	pipe.Set(ctx, codeKey(phone), hash, ttl)
	pipe.Del(ctx, triesKey(phone))
	_, err := pipe.Exec(ctx)
	return err
}

// Replace swaps the code a player is waiting for without touching the counter.
// Asking for the code again is not a way to buy five more guesses.
func (r *redisOTP) Replace(ctx context.Context, phone, hash string, ttl time.Duration) error {
	return r.c.Set(ctx, codeKey(phone), hash, ttl).Err()
}

func (r *redisOTP) Get(ctx context.Context, phone string) (Pending, bool, error) {
	hash, err := r.c.Get(ctx, codeKey(phone)).Result()
	if err == goredis.Nil {
		return Pending{}, false, nil
	}
	if err != nil {
		return Pending{}, false, err
	}

	tries, err := r.c.Get(ctx, triesKey(phone)).Int()
	if err != nil && err != goredis.Nil {
		return Pending{}, false, err
	}
	return Pending{Hash: hash, Attempts: tries}, true, nil
}

// Attempt counts a guess. INCR is atomic, so two guesses at once never read the
// same count. The counter expires with the code, so a number is never locked out
// beyond the code's own life.
func (r *redisOTP) Attempt(ctx context.Context, phone string) (int, error) {
	n, err := r.c.Incr(ctx, triesKey(phone)).Result()
	if err != nil {
		return 0, err
	}
	if n == 1 {
		ttl, err := r.c.TTL(ctx, codeKey(phone)).Result()
		if err == nil && ttl > 0 {
			r.c.Expire(ctx, triesKey(phone), ttl)
		}
	}
	return int(n), nil
}

func (r *redisOTP) Clear(ctx context.Context, phone string) error {
	return r.c.Del(ctx, codeKey(phone), triesKey(phone)).Err()
}

// Limiter answers whether an action may happen again yet. RetryAfter is in
// seconds and is what the client is told.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (ok bool, retryAfter int, err error)
}

// redisLimiter counts a fixed window. A blocked call counts but does not push the
// window out, so waiting quietly is enough.
type redisLimiter struct{ c *goredis.Client }

func NewLimiter(c *goredis.Client) Limiter { return &redisLimiter{c: c} }

func (r *redisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	n, err := r.c.Incr(ctx, key).Result()
	if err != nil {
		return false, 0, err
	}
	if n == 1 {
		if err := r.c.Expire(ctx, key, window).Err(); err != nil {
			return false, 0, err
		}
	}
	if int(n) <= limit {
		return true, 0, nil
	}

	ttl, err := r.c.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 {
		ttl = window
	}
	return false, int(math.Ceil(ttl.Seconds())), nil
}

// Notifier carries a plain message through the same vendor. Separate because
// nothing here is a secret: a forwarded "you were added" grants nobody anything.
type Notifier interface {
	Notify(ctx context.Context, phone, text string) error
}
