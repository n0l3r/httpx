// Package tiered provides a two-level (L1 + L2) cache for httpx.
//
// Typical usage pairs a fast in-memory cache (L1) with a Redis cache (L2):
//
//	l1 := httpx.NewMemoryCache(30 * time.Second)
//	l2 := rediscache.New(rdb, rediscache.DefaultConfig)
//
//	c, _ := httpx.New(
//	    httpx.WithCache(tiered.New(l1, l2)),
//	)
//
// On cache miss in L1, the TieredCache checks L2 and back-fills L1.
// Writes go to both levels simultaneously.
package tiered

import (
	"time"

	"github.com/n0l3r/httpx"
)

// Cache is a two-level cache that combines a fast L1 and a slower L2.
type Cache struct {
	l1 httpx.Cache
	l2 httpx.Cache
	// L1TTLFraction controls what fraction of the TTL is used for L1.
	// Default: 0.5 (L1 gets half the TTL of L2).
	L1TTLFraction float64
}

// New creates a TieredCache with L1 (e.g. MemoryCache) and L2 (e.g. RedisCache).
func New(l1, l2 httpx.Cache) *Cache {
	return &Cache{l1: l1, l2: l2, L1TTLFraction: 0.5}
}

// Get checks L1 first, then L2 on miss (and back-fills L1).
func (t *Cache) Get(key string) (*httpx.CachedResponse, bool) {
	if v, ok := t.l1.Get(key); ok {
		return v, true
	}
	v, ok := t.l2.Get(key)
	if !ok {
		return nil, false
	}
	// Back-fill L1 with a shorter TTL.
	fraction := t.L1TTLFraction
	if fraction <= 0 {
		fraction = 0.5
	}
	l1TTL := time.Duration(float64(v.TTL) * fraction)
	if l1TTL > 0 {
		t.l1.Set(key, v, l1TTL)
	}
	return v, true
}

// Set writes to both L1 (short TTL) and L2 (full TTL).
func (t *Cache) Set(key string, resp *httpx.CachedResponse, ttl time.Duration) {
	fraction := t.L1TTLFraction
	if fraction <= 0 {
		fraction = 0.5
	}
	l1TTL := time.Duration(float64(ttl) * fraction)
	t.l1.Set(key, resp, l1TTL)
	t.l2.Set(key, resp, ttl)
}

// Delete removes the entry from both levels.
func (t *Cache) Delete(key string) {
	t.l1.Delete(key)
	t.l2.Delete(key)
}
