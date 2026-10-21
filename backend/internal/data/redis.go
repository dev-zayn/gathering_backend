package data

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis holds the things that are true only for a while: pending OTPs,
// rate-limit counters, the token whitelist, the OTP vendor's own access token.
type Redis = redis.Client

// RedisNil is returned by Get when the key is not there. It is a normal answer,
// not a failure — an expired OTP looks exactly like this.
const RedisNil = redis.Nil

// OpenRedis is named apart from Open because one package now opens both, and
// "open the database" is no longer an unambiguous thing to say.
func OpenRedis(ctx context.Context, url string) (*Redis, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opt.DialTimeout = 3 * time.Second
	opt.ReadTimeout = 2 * time.Second
	opt.WriteTimeout = 2 * time.Second

	c := redis.NewClient(opt)

	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pctx).Err(); err != nil {
		return nil, err
	}
	return c, nil
}
