package httpx

import (
	"context"
	"net/http"
	"time"
)

// Middleware is a function that wraps an http.RoundTripper.
// Middlewares are applied outermost-first, meaning the first middleware in the
// slice is the first to process the request and the last to process the response.
type Middleware func(next http.RoundTripper) http.RoundTripper

// RoundTripperFunc is an adapter to use a plain function as an http.RoundTripper.
type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// buildMiddlewareChain wraps transport with all configured middlewares and
// built-in behaviours (retry, circuit breaker, rate limiter, cache, logging, metrics).
func buildMiddlewareChain(transport http.RoundTripper, cfg *clientConfig) http.RoundTripper {
	// Start with the raw transport.
	chain := transport

	// Apply user-defined middlewares in reverse order so that the first one
	// registered is the outermost (first to run).
	for i := len(cfg.middlewares) - 1; i >= 0; i-- {
		chain = cfg.middlewares[i](chain)
	}

	// Built-in: metrics (outer)
	if cfg.metricsHook != nil {
		chain = metricsMiddleware(cfg.metricsHook)(chain)
	}

	// Built-in: logging (outer-ish, wraps everything below)
	if cfg.logHook != nil {
		chain = loggingMiddleware(cfg.logHook)(chain)
	}

	// Built-in: rate limiter
	if cfg.rateLimiter != nil {
		chain = rateLimiterMiddleware(cfg.rateLimiter)(chain)
	}

	// Built-in: circuit breaker (allow/record style)
	if cfg.circuitBreaker != nil {
		chain = circuitBreakerMiddleware(cfg.circuitBreaker)(chain)
	}

	// Built-in: circuit breaker (execute style, e.g. gobreaker)
	if cfg.executingCircuitBreaker != nil {
		chain = executingCircuitBreakerMiddleware(cfg.executingCircuitBreaker)(chain)
	}

	// Built-in: cache (outermost, so we skip all inner work on hit)
	if cfg.cache != nil {
		chain = cacheMiddleware(cfg.cache)(chain)
	}

	// Built-in: retry (wraps the inner chain so retries go through CB + RL)
	if cfg.retryPolicy != nil {
		chain = retryMiddleware(cfg.retryPolicy)(chain)
	}

	return chain
}

// -------------------------------------------------------------------
// Built-in middleware constructors
// -------------------------------------------------------------------

// HeaderInjector returns a Middleware that injects static headers into every request.
func HeaderInjector(headers map[string]string) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			// Clone to avoid mutating the original request.
			r := req.Clone(req.Context())
			for k, v := range headers {
				if r.Header.Get(k) == "" {
					r.Header.Set(k, v)
				}
			}
			return next.RoundTrip(r)
		})
	}
}

// CorrelationIDInjector returns a Middleware that injects a correlation/request-ID header
// if one is not already present.
func CorrelationIDInjector(headerName string, generator func() string) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			r := req.Clone(req.Context())
			if r.Header.Get(headerName) == "" {
				r.Header.Set(headerName, generator())
			}
			return next.RoundTrip(r)
		})
	}
}

// TimeoutMiddleware returns a Middleware that enforces a per-request timeout via context.
func TimeoutMiddleware(d time.Duration) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			ctx, cancel := context.WithTimeout(req.Context(), d)
			defer cancel()
			return next.RoundTrip(req.Clone(ctx))
		})
	}
}
