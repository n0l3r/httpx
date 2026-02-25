package httpx

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"
)

// Cache is the interface for response caching.
type Cache interface {
	Get(key string) (*CachedResponse, bool)
	Set(key string, resp *CachedResponse, ttl time.Duration)
	Delete(key string)
}

// CachedResponse is the cached representation of an HTTP response.
type CachedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	CachedAt   time.Time
	TTL        time.Duration
}

// IsExpired reports whether the cached entry has expired.
func (cr *CachedResponse) IsExpired() bool {
	if cr.TTL <= 0 {
		return false
	}
	return time.Since(cr.CachedAt) > cr.TTL
}

// ToHTTPResponse reconstructs an *http.Response from the cached data.
func (cr *CachedResponse) ToHTTPResponse() *http.Response {
	return &http.Response{
		StatusCode: cr.StatusCode,
		Header:     cr.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(cr.Body)),
	}
}

// -------------------------------------------------------------------
// In-memory TTL cache
// -------------------------------------------------------------------

type memCacheEntry struct {
	resp     *CachedResponse
	expireAt time.Time
}

// MemoryCache is a simple in-memory cache with TTL eviction.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]*memCacheEntry
	// DefaultTTL is used when no TTL is provided in Set.
	DefaultTTL time.Duration
}

// NewMemoryCache creates a new MemoryCache.
func NewMemoryCache(defaultTTL time.Duration) *MemoryCache {
	c := &MemoryCache{
		entries:    make(map[string]*memCacheEntry),
		DefaultTTL: defaultTTL,
	}
	go c.evictLoop()
	return c
}

func (m *MemoryCache) Get(key string) (*CachedResponse, bool) {
	m.mu.RLock()
	e, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expireAt) {
		m.Delete(key)
		return nil, false
	}
	return e.resp, true
}

func (m *MemoryCache) Set(key string, resp *CachedResponse, ttl time.Duration) {
	if ttl <= 0 {
		ttl = m.DefaultTTL
	}
	resp.CachedAt = time.Now()
	resp.TTL = ttl
	m.mu.Lock()
	m.entries[key] = &memCacheEntry{resp: resp, expireAt: time.Now().Add(ttl)}
	m.mu.Unlock()
}

func (m *MemoryCache) Delete(key string) {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
}

func (m *MemoryCache) evictLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		m.mu.Lock()
		for k, e := range m.entries {
			if now.After(e.expireAt) {
				delete(m.entries, k)
			}
		}
		m.mu.Unlock()
	}
}

// -------------------------------------------------------------------
// NoopCache — disables caching (useful for testing or opt-out)
// -------------------------------------------------------------------

// NoopCache is a Cache implementation that never stores anything.
type NoopCache struct{}

func (NoopCache) Get(_ string) (*CachedResponse, bool)         { return nil, false }
func (NoopCache) Set(_ string, _ *CachedResponse, _ time.Duration) {}
func (NoopCache) Delete(_ string)                               {}

// -------------------------------------------------------------------
// TieredCache — L1 (fast, in-memory) + L2 (slower, e.g. Redis)
// -------------------------------------------------------------------

// TieredCache checks L1 first, falls through to L2 on miss, and back-fills L1 on hit.
type TieredCache struct {
	L1 Cache
	L2 Cache
}

// NewTieredCache creates a two-level cache.
// Typical usage: L1 = MemoryCache (short TTL), L2 = RedisCache (longer TTL).
func NewTieredCache(l1, l2 Cache) *TieredCache {
	return &TieredCache{L1: l1, L2: l2}
}

func (t *TieredCache) Get(key string) (*CachedResponse, bool) {
	if v, ok := t.L1.Get(key); ok {
		return v, true
	}
	if v, ok := t.L2.Get(key); ok {
		// Back-fill L1 with remaining TTL (use L2 TTL heuristic).
		t.L1.Set(key, v, v.TTL/2)
		return v, true
	}
	return nil, false
}

func (t *TieredCache) Set(key string, resp *CachedResponse, ttl time.Duration) {
	t.L1.Set(key, resp, ttl/2) // shorter TTL in L1
	t.L2.Set(key, resp, ttl)
}

func (t *TieredCache) Delete(key string) {
	t.L1.Delete(key)
	t.L2.Delete(key)
}

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

func cacheMiddleware(cache Cache) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			// Only cache GET requests.
			if req.Method != http.MethodGet {
				return next.RoundTrip(req)
			}

			key := req.URL.String()

			if cached, ok := cache.Get(key); ok {
				return cached.ToHTTPResponse(), nil
			}

			resp, err := next.RoundTrip(req)
			if err != nil {
				return nil, err
			}

			// Only cache 2xx responses.
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr == nil {
					cache.Set(key, &CachedResponse{
						StatusCode: resp.StatusCode,
						Header:     resp.Header.Clone(),
						Body:       body,
					}, 0)
					resp.Body = io.NopCloser(bytes.NewReader(body))
				}
			}

			return resp, nil
		})
	}
}
