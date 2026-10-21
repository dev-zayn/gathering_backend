package identity

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Whitelist is what makes a token revocable: its id must be listed here, so
// signing out takes effect at once. It fails closed.
type Whitelist interface {
	Allow(ctx context.Context, jti, sessionID string, ttl time.Duration) error
	Valid(ctx context.Context, jti string) (bool, error)
	Revoke(ctx context.Context, jti string) error

	// Touch is Valid for a token that ends when it sits unused: listed or not,
	// and if listed, good for another idle from now.
	Touch(ctx context.Context, jti string, idle time.Duration) (bool, error)

	// RevokeSession clears every token issued for one session, which is what
	// signing out and a replayed refresh token both need.
	RevokeSession(ctx context.Context, sessionID string) error

	// AllowGame lists a token by its own id and under the game and the player,
	// so removing the game reaches it before it expires.
	AllowGame(ctx context.Context, jti, gameID, playerID string, ttl time.Duration) error

	// RevokeGame clears one player's tokens for a game, or — with no player —
	// every player's, which is the kill switch.
	RevokeGame(ctx context.Context, gameID, playerID string) error
}

// redisWhitelist lists the session tokens that are still good. An entry expires with
// the token it stands for, so the set never needs sweeping.
//
// Redis may be a cluster, where one command or transaction can only touch keys
// in one slot. A token is found by its own id and its session by another, so
// they cannot share one: the writes below are pipelined, one key per command,
// and ordered so that one cut off halfway leaves nothing let in.
type redisWhitelist struct{ c *goredis.Client }

func NewWhitelist(c *goredis.Client) Whitelist { return &redisWhitelist{c: c} }

func tokenKey(jti string) string  { return "tok:" + jti }
func sessionKey(id string) string { return "sess:" + id }

// Allow lists the token and remembers which session it came from, so the session
// can be cleared later without knowing every token it ever had.
func (r *redisWhitelist) Allow(ctx context.Context, jti, sessionID string, ttl time.Duration) error {
	pipe := r.c.Pipeline()
	// The set first: a token listed under no session could not be signed out.
	pipe.SAdd(ctx, sessionKey(sessionID), jti)
	pipe.Expire(ctx, sessionKey(sessionID), ttl)
	pipe.Set(ctx, tokenKey(jti), sessionID, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisWhitelist) RevokeSession(ctx context.Context, sessionID string) error {
	jtis, err := r.c.SMembers(ctx, sessionKey(sessionID)).Result()
	if err != nil && err != goredis.Nil {
		return err
	}

	return r.drop(ctx, jtis, sessionKey(sessionID))
}

// drop deletes the tokens, then the set that lists them, one key per command.
// The set goes last, so a delete cut off halfway can be run again.
func (r *redisWhitelist) drop(ctx context.Context, jtis []string, set string) error {
	pipe := r.c.Pipeline()
	for _, j := range jtis {
		pipe.Del(ctx, tokenKey(j))
	}
	pipe.Del(ctx, set)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisWhitelist) Valid(ctx context.Context, jti string) (bool, error) {
	n, err := r.c.Exists(ctx, tokenKey(jti)).Result()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Touch moves the entry's expiry to idle from now, and the set it is listed
// under with it, so RevokeSession still finds a token kept alive past the idle
// it was first given. An entry that already expired is not brought back.
func (r *redisWhitelist) Touch(ctx context.Context, jti string, idle time.Duration) (bool, error) {
	sessionID, err := r.c.Get(ctx, tokenKey(jti)).Result()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	pipe := r.c.Pipeline()
	pipe.Expire(ctx, sessionKey(sessionID), idle)
	live := pipe.Expire(ctx, tokenKey(jti), idle)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return live.Val(), nil
}

func (r *redisWhitelist) Revoke(ctx context.Context, jti string) error {
	return r.c.Del(ctx, tokenKey(jti)).Err()
}

func gameKey(gameID string) string { return "game:" + gameID }
func gamePlayerKey(gameID, playerID string) string {
	return "game:" + gameID + ":" + playerID
}

// AllowGame lists the token under two sets as well as its own key. Both sets
// expire with the tokens in them, so neither grows past an hour of activity.
func (r *redisWhitelist) AllowGame(ctx context.Context, jti, gameID, playerID string, ttl time.Duration) error {
	pipe := r.c.Pipeline()
	for _, key := range []string{gameKey(gameID), gamePlayerKey(gameID, playerID)} {
		pipe.SAdd(ctx, key, jti)
		pipe.Expire(ctx, key, ttl)
	}
	pipe.Set(ctx, tokenKey(jti), gameID, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// RevokeGame is what leaving a game and suspending one both reach for. With no
// player it clears every player's tokens for that game.
func (r *redisWhitelist) RevokeGame(ctx context.Context, gameID, playerID string) error {
	key := gameKey(gameID)
	if playerID != "" {
		key = gamePlayerKey(gameID, playerID)
	}

	jtis, err := r.c.SMembers(ctx, key).Result()
	if err != nil && err != goredis.Nil {
		return err
	}

	return r.drop(ctx, jtis, key)
}
