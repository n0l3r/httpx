// Package redis provides a Redis-backed rate limiter for httpx.
//
// It uses a sliding window algorithm implemented with Redis sorted sets:
//   - ZADD: add current timestamp as member
//   - ZREMRANGEBYSCORE: remove entries outside the window
//   - ZCARD: count remaining entries
//
// This gives an accurate sliding window without Lua scripting.
//
// Usage:
//
//	rdb := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
//	rl := redisrl.New(rdb, redisrl.Config{
//	    Rate:   100,            // requests
//	    Window: time.Second,    // per second
//	    Burst:  10,
//	})
//	c, _ := httpx.New(httpx.WithRateLimiter(rl))
package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/n0l3r/httpx"
)

// Config controls the Redis rate limiter behaviour.
type Config struct {
	// KeyPrefix is prepended to the Redis key. Default: "httpx:rl:".
	KeyPrefix string
	// Rate is the number of requests allowed per Window.
	Rate int
	// Window is the sliding window duration. Default: 1s.
	Window time.Duration
	// Burst allows this many extra requests above Rate in a window.
	Burst int
	// PerHost, when true, creates a separate key per host.
	// When false, a single global key is used.
	PerHost bool
	// Timeout for Redis commands. Default: 200ms.
	Timeout time.Duration
}

// RateLimiter is a Redis-backed sliding-window rate limiter.
type RateLimiter struct {
	rdb    *goredis.Client
	config Config
}

// New creates a new Redis rate limiter.
func New(rdb *goredis.Client, cfg Config) *RateLimiter {
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "httpx:rl:"
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 200 * time.Millisecond
	}
	return &RateLimiter{rdb: rdb, config: cfg}
}

// Wait blocks until the rate limiter permits a request, or until the context is done.
func (r *RateLimiter) Wait(ctx context.Context, host string) error {
	key := r.buildKey(host)
	limit := r.config.Rate + r.config.Burst

	for {
		allowed, waitFor, err := r.check(ctx, key, limit)
		if err != nil {
			// On Redis error, allow the request (fail open).
			return nil
		}
		if allowed {
			return nil
		}
		// Wait until the window clears, respecting context cancellation.
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", httpx.ErrRateLimitExceeded, ctx.Err())
		case <-time.After(waitFor):
		}
	}
}

func (r *RateLimiter) check(ctx context.Context, key string, limit int) (bool, time.Duration, error) {
	timeCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()

	now := time.Now()
	windowStart := now.Add(-r.config.Window)

	pipe := r.rdb.Pipeline()
	// Remove timestamps outside the window.
	pipe.ZRemRangeByScore(timeCtx, key, "0", strconv.FormatInt(windowStart.UnixNano(), 10))
	// Add current request timestamp (use nanoseconds as both score and member for uniqueness).
	member := strconv.FormatInt(now.UnixNano(), 10)
	pipe.ZAdd(timeCtx, key, goredis.Z{Score: float64(now.UnixNano()), Member: member})
	// Count entries in window.
	countCmd := pipe.ZCard(timeCtx, key)
	// Expire the key after the window duration.
	pipe.Expire(timeCtx, key, r.config.Window*2)

	if _, err := pipe.Exec(timeCtx); err != nil {
		return true, 0, err // fail open on Redis error
	}

	count := countCmd.Val()
	if count <= int64(limit) {
		return true, 0, nil
	}

	// Remove the request we just added (rejected).
	_ = r.rdb.ZRem(timeCtx, key, member)

	// Estimate wait: time until the oldest entry exits the window.
	oldest, err := r.rdb.ZRangeWithScores(timeCtx, key, 0, 0).Result()
	if err != nil || len(oldest) == 0 {
		return false, r.config.Window, nil
	}
	oldestNs := int64(oldest[0].Score)
	expiry := time.Unix(0, oldestNs).Add(r.config.Window)
	waitFor := time.Until(expiry)
	if waitFor < 0 {
		waitFor = 0
	}
	return false, waitFor, nil
}

func (r *RateLimiter) buildKey(host string) string {
	if r.config.PerHost {
		return r.config.KeyPrefix + host
	}
	return r.config.KeyPrefix + "global"
}
