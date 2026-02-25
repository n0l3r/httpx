package httpx

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/time/rate"
)

// RateLimiterProvider decides whether a request should proceed.
type RateLimiterProvider interface {
	// Wait blocks until the rate limiter permits a request for the given host,
	// or until the context is done.
	Wait(ctx context.Context, host string) error
}

// -------------------------------------------------------------------
// Token-bucket rate limiter (global)
// -------------------------------------------------------------------

// GlobalRateLimiter applies a single token-bucket limit across all hosts.
type GlobalRateLimiter struct {
	limiter *rate.Limiter
}

// NewGlobalRateLimiter creates a rate limiter that allows r requests/sec
// with a burst of b.
func NewGlobalRateLimiter(r rate.Limit, b int) *GlobalRateLimiter {
	return &GlobalRateLimiter{limiter: rate.NewLimiter(r, b)}
}

func (g *GlobalRateLimiter) Wait(ctx context.Context, _ string) error {
	if err := g.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrRateLimitExceeded, err)
	}
	return nil
}

// -------------------------------------------------------------------
// Per-host rate limiter
// -------------------------------------------------------------------

// PerHostRateLimiter applies independent token-bucket limits per host.
type PerHostRateLimiter struct {
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

// NewPerHostRateLimiter creates a per-host rate limiter.
// If a host is not in limiters map it falls back to using global r/b settings.
func NewPerHostRateLimiter(r rate.Limit, b int, perHost map[string]*rate.Limiter) *PerHostRateLimiter {
	if perHost == nil {
		perHost = make(map[string]*rate.Limiter)
	}
	return &PerHostRateLimiter{limiters: perHost, r: r, b: b}
}

func (p *PerHostRateLimiter) Wait(ctx context.Context, host string) error {
	l, ok := p.limiters[host]
	if !ok {
		l = rate.NewLimiter(p.r, p.b)
		p.limiters[host] = l
	}
	if err := l.Wait(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrRateLimitExceeded, err)
	}
	return nil
}

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

func rateLimiterMiddleware(rl RateLimiterProvider) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if err := rl.Wait(req.Context(), req.URL.Host); err != nil {
				return nil, err
			}
			return next.RoundTrip(req)
		})
	}
}
