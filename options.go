package httpx

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Option is a functional option for configuring a Client.
type Option func(*clientConfig)

// clientConfig holds all configuration for a Client.
type clientConfig struct {
	// Transport / connection settings
	timeout         time.Duration
	transport       http.RoundTripper
	tlsConfig       *tls.Config
	proxyURL        *url.URL
	maxIdleConns    int
	maxConnsPerHost int
	idleConnTimeout time.Duration

	// Base URL & headers
	baseURL        string
	defaultHeaders map[string]string

	// Retry
	retryPolicy *RetryPolicy

	// Logging / metrics hooks
	logHook     LogHook
	metricsHook MetricsHook

	// Middleware
	middlewares []Middleware

	// Circuit breaker
	circuitBreaker         CircuitBreakerProvider
	executingCircuitBreaker ExecutingCircuitBreaker

	// Rate limiter
	rateLimiter RateLimiterProvider

	// Cache
	cache Cache

	// Body size limit (0 = unlimited)
	maxBodyBytes int64

	// Debug / observability
	debugMode      bool
	debugOutput    interface{ Write([]byte) (int, error) }
	logger         *slog.Logger
	correlationKey string // header name for correlation/request-id

	// Singleflight
	singleflightEnabled bool

	// HTTP/2
	forceHTTP2 bool

	// Failover hosts (tried in order on network errors)
	failoverHosts []string

	// Context hook — called before each request
	beforeRequest func(ctx context.Context, req *http.Request)
	afterResponse func(ctx context.Context, req *http.Request, resp *http.Response, err error)
}

func defaultConfig() *clientConfig {
	return &clientConfig{
		timeout:         30 * time.Second,
		maxIdleConns:    100,
		maxConnsPerHost: 10,
		idleConnTimeout: 90 * time.Second,
		defaultHeaders:  make(map[string]string),
		correlationKey:  "X-Request-ID",
	}
}

// -------------------------------------------------------------------
// Transport / connection options
// -------------------------------------------------------------------

// WithTimeout sets the overall request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *clientConfig) { c.timeout = d }
}

// WithTransport sets a custom http.RoundTripper.
func WithTransport(t http.RoundTripper) Option {
	return func(c *clientConfig) { c.transport = t }
}

// WithTLSConfig sets a custom TLS configuration.
func WithTLSConfig(tlsCfg *tls.Config) Option {
	return func(c *clientConfig) { c.tlsConfig = tlsCfg }
}

// WithProxy sets an HTTP proxy URL.
func WithProxy(proxyURL string) Option {
	return func(c *clientConfig) {
		u, err := url.Parse(proxyURL)
		if err == nil {
			c.proxyURL = u
		}
	}
}

// WithConnectionPool tunes connection pool parameters.
func WithConnectionPool(maxIdle, maxPerHost int, idleTimeout time.Duration) Option {
	return func(c *clientConfig) {
		c.maxIdleConns = maxIdle
		c.maxConnsPerHost = maxPerHost
		c.idleConnTimeout = idleTimeout
	}
}

// WithHTTP2 forces HTTP/2 usage.
func WithHTTP2() Option {
	return func(c *clientConfig) { c.forceHTTP2 = true }
}

// -------------------------------------------------------------------
// Base URL & default headers
// -------------------------------------------------------------------

// WithBaseURL sets a base URL prepended to all relative request paths.
func WithBaseURL(base string) Option {
	return func(c *clientConfig) { c.baseURL = base }
}

// WithDefaultHeader adds a default header sent on every request.
func WithDefaultHeader(key, value string) Option {
	return func(c *clientConfig) { c.defaultHeaders[key] = value }
}

// WithDefaultHeaders adds multiple default headers at once.
func WithDefaultHeaders(headers map[string]string) Option {
	return func(c *clientConfig) {
		for k, v := range headers {
			c.defaultHeaders[k] = v
		}
	}
}

// WithCorrelationIDHeader sets the header name used for request/correlation IDs.
// Default: "X-Request-ID".
func WithCorrelationIDHeader(header string) Option {
	return func(c *clientConfig) { c.correlationKey = header }
}

// -------------------------------------------------------------------
// Retry
// -------------------------------------------------------------------

// WithRetryPolicy sets the retry policy.
func WithRetryPolicy(p *RetryPolicy) Option {
	return func(c *clientConfig) { c.retryPolicy = p }
}

// -------------------------------------------------------------------
// Hooks
// -------------------------------------------------------------------

// WithLogHook registers a structured logging hook.
func WithLogHook(h LogHook) Option {
	return func(c *clientConfig) { c.logHook = h }
}

// WithMetricsHook registers a metrics hook.
func WithMetricsHook(h MetricsHook) Option {
	return func(c *clientConfig) { c.metricsHook = h }
}

// WithSlogLogger attaches a *slog.Logger and registers a default slog-based log hook.
func WithSlogLogger(l *slog.Logger) Option {
	return func(c *clientConfig) {
		c.logger = l
		c.logHook = newSlogHook(l)
	}
}

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

// WithMiddleware appends middleware to the request pipeline.
// Middleware is executed in the order it is registered (outermost first).
func WithMiddleware(m ...Middleware) Option {
	return func(c *clientConfig) { c.middlewares = append(c.middlewares, m...) }
}

// WithBeforeRequest sets a hook called just before each HTTP request is sent.
func WithBeforeRequest(fn func(ctx context.Context, req *http.Request)) Option {
	return func(c *clientConfig) { c.beforeRequest = fn }
}

// WithAfterResponse sets a hook called after each HTTP response is received.
func WithAfterResponse(fn func(ctx context.Context, req *http.Request, resp *http.Response, err error)) Option {
	return func(c *clientConfig) { c.afterResponse = fn }
}

// -------------------------------------------------------------------
// Reliability
// -------------------------------------------------------------------

// WithCircuitBreaker attaches a circuit breaker provider.
func WithCircuitBreaker(cb CircuitBreakerProvider) Option {
	return func(c *clientConfig) { c.circuitBreaker = cb }
}

// WithExecutingCircuitBreaker attaches an execute-style circuit breaker
// (e.g. sony/gobreaker). Use this instead of WithCircuitBreaker when your
// circuit breaker library uses the Execute/Call pattern.
func WithExecutingCircuitBreaker(cb ExecutingCircuitBreaker) Option {
	return func(c *clientConfig) { c.executingCircuitBreaker = cb }
}

// WithRateLimiter attaches a rate limiter provider.
func WithRateLimiter(rl RateLimiterProvider) Option {
	return func(c *clientConfig) { c.rateLimiter = rl }
}

// WithSingleflight enables singleflight request deduplication for GET requests.
func WithSingleflight() Option {
	return func(c *clientConfig) { c.singleflightEnabled = true }
}

// -------------------------------------------------------------------
// Cache
// -------------------------------------------------------------------

// WithCache attaches a response cache.
func WithCache(cache Cache) Option {
	return func(c *clientConfig) { c.cache = cache }
}

// -------------------------------------------------------------------
// Response body
// -------------------------------------------------------------------

// WithMaxBodyBytes limits the maximum number of bytes read from a response body.
// 0 means unlimited.
func WithMaxBodyBytes(n int64) Option {
	return func(c *clientConfig) { c.maxBodyBytes = n }
}

// -------------------------------------------------------------------
// Debug
// -------------------------------------------------------------------

// WithDebugMode enables full request/response logging.
func WithDebugMode(w interface{ Write([]byte) (int, error) }) Option {
	return func(c *clientConfig) {
		c.debugMode = true
		c.debugOutput = w
	}
}

// -------------------------------------------------------------------
// Failover
// -------------------------------------------------------------------

// WithFailoverHosts sets a list of alternative base URLs tried on network failures.
func WithFailoverHosts(hosts ...string) Option {
	return func(c *clientConfig) { c.failoverHosts = hosts }
}
