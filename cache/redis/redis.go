// Package redis provides a Redis-backed cache for httpx.
//
// Usage:
//
//	rdb := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
//	c, _ := httpx.New(
//	    httpx.WithCache(rediscache.New(rdb, rediscache.DefaultConfig)),
//	)
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/n0l3r/httpx"
)

// Config holds configuration for the Redis cache.
type Config struct {
	// KeyPrefix is prepended to every cache key. Default: "httpx:cache:".
	KeyPrefix string
	// DefaultTTL is used when Set is called with ttl == 0.
	DefaultTTL time.Duration
}

// DefaultConfig is a sensible default Redis cache config.
var DefaultConfig = Config{
	KeyPrefix:  "httpx:cache:",
	DefaultTTL: 5 * time.Minute,
}

// entry is the JSON-serialisable representation stored in Redis.
type entry struct {
	StatusCode int                 `json:"status"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
	TTLNs      int64               `json:"ttl_ns"`
}

// Cache is a Redis-backed implementation of httpx.Cache.
type Cache struct {
	rdb    *goredis.Client
	config Config
}

// New creates a new Redis cache.
func New(rdb *goredis.Client, cfg Config) *Cache {
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "httpx:cache:"
	}
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = 5 * time.Minute
	}
	return &Cache{rdb: rdb, config: cfg}
}

func (c *Cache) key(k string) string { return c.config.KeyPrefix + k }

// Get retrieves a cached response.
func (c *Cache) Get(key string) (*httpx.CachedResponse, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	b, err := c.rdb.Get(ctx, c.key(key)).Bytes()
	if err != nil {
		return nil, false
	}

	var e entry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, false
	}

	return &httpx.CachedResponse{
		StatusCode: e.StatusCode,
		Header:     http.Header(e.Headers),
		Body:       e.Body,
		CachedAt:   time.Now(),
		TTL:        time.Duration(e.TTLNs),
	}, true
}

// Set stores a response in Redis.
func (c *Cache) Set(key string, resp *httpx.CachedResponse, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.config.DefaultTTL
	}

	e := entry{
		StatusCode: resp.StatusCode,
		Headers:    map[string][]string(resp.Header),
		Body:       resp.Body,
		TTLNs:      int64(ttl),
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = c.rdb.Set(ctx, c.key(key), b, ttl).Err()
}

// Delete removes a cached entry.
func (c *Cache) Delete(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = c.rdb.Del(ctx, c.key(key)).Err()
}

// Flush removes all cache entries with the configured key prefix.
func (c *Cache) Flush() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var cursor uint64
	pattern := c.config.KeyPrefix + "*"
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("redis cache flush scan: %w", err)
		}
		if len(keys) > 0 {
			if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("redis cache flush del: %w", err)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return nil
}

