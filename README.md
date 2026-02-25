# httpx

Production-grade, feature-rich HTTP client library for Go.

```
go get github.com/n0l3r/httpx
```

---

## Features

| Category | Features |
|---|---|
| 🧱 Core | Configurable client, context support, fluent request builder, response wrapper, JSON helpers, custom errors, mockable interface, functional options |
| 🚀 Production | Retry (by error / status), exponential backoff + jitter, logging hook, metrics hook, middleware chain, header injection, correlation ID, body size limiter |
| 🛡 Reliability | Circuit breaker, rate limiter (global/per-host), singleflight deduplication, TTL response cache, connection pool tuning, auto-gzip, HTTP/2 |
| 🔐 Security | OAuth 1.0a signing, OAuth 2.0 Bearer token, HMAC request signing, idempotency key |
| 📊 Observability | OpenTelemetry tracing, latency measurement, error classification (timeout/network/4xx/5xx) |
| 🧰 DX | Base URL, default headers, debug mode, multi-host failover, mock transport |

---

## Quick Start

```go
import "github.com/n0l3r/httpx"

c, err := httpx.New(
    httpx.WithBaseURL("https://api.example.com"),
    httpx.WithTimeout(10 * time.Second),
    httpx.WithDefaultHeader("X-App-Name", "my-service"),
)

// GET + JSON decode
var result MyStruct
err = c.GetJSON(ctx, "/users/1", &result)

// POST + JSON
err = c.PostJSON(ctx, "/users", CreateUserRequest{Name: "alice"}, &result)
```

---

## Functional Options

```go
c, err := httpx.New(
    // Transport
    httpx.WithTimeout(15 * time.Second),
    httpx.WithTLSConfig(tlsCfg),
    httpx.WithProxy("http://proxy:8080"),
    httpx.WithConnectionPool(200, 20, 90*time.Second),
    httpx.WithHTTP2(),

    // Base URL & headers
    httpx.WithBaseURL("https://api.example.com"),
    httpx.WithDefaultHeaders(map[string]string{
        "X-App-Name":    "my-service",
        "X-Environment": "production",
    }),

    // Retry
    httpx.WithRetryPolicy(httpx.DefaultRetryPolicy()),

    // Logging
    httpx.WithSlogLogger(slog.Default()),

    // Metrics
    httpx.WithMetricsHook(httpx.MetricsHookFunc(func(e httpx.MetricsEvent) {
        myMetrics.RecordLatency(e.Method, e.StatusCode, e.Duration)
    })),

    // Circuit breaker
    httpx.WithCircuitBreaker(httpx.NewCircuitBreaker(httpx.DefaultCircuitBreakerConfig)),

    // Rate limiter
    httpx.WithRateLimiter(httpx.NewGlobalRateLimiter(rate.Limit(100), 10)),

    // Cache
    httpx.WithCache(httpx.NewMemoryCache(5 * time.Minute)),

    // Singleflight
    httpx.WithSingleflight(),

    // Body limit
    httpx.WithMaxBodyBytes(10 << 20), // 10 MB

    // Debug
    httpx.WithDebugMode(os.Stderr),
)
```

---

## Fluent Request Builder

```go
req, err := c.NewRequest(ctx, "POST", "/orders").
    Header("X-Idempotency-Key", "abc123").
    Query("dryRun", "true").
    BodyJSON(OrderRequest{Amount: 100}).
    BearerToken("my-token").
    Build()

resp, err := c.Do(req)
```

---

## Retry Policy

```go
policy := &httpx.RetryPolicy{
    MaxAttempts: 4,
    Backoff:     httpx.ExponentialBackoff(200*time.Millisecond, 10*time.Second, 0.3),
    Conditions: []httpx.RetryConditionFunc{
        httpx.RetryOnNetworkError,
        httpx.RetryOnStatus5xx,
        httpx.RetryOnStatus429,
        httpx.RetryOnStatuses(http.StatusRequestTimeout),
    },
    RetryOnlyIdempotent: true,
    OnRetry: func(attempt int, req *http.Request, resp *http.Response, err error) {
        log.Printf("retry #%d for %s", attempt, req.URL)
    },
}
```

### Backoff Strategies

```go
httpx.FullJitterBackoff(200*time.Millisecond, 10*time.Second) // default
httpx.ExponentialBackoff(100*time.Millisecond, 5*time.Second, 0.25)
httpx.ConstantBackoff(500 * time.Millisecond)
httpx.LinearBackoff(100*time.Millisecond, 100*time.Millisecond)
```

---

## Middleware

```go
// Custom middleware
loggingMW := func(next http.RoundTripper) http.RoundTripper {
    return httpx.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
        log.Println("→", req.Method, req.URL)
        resp, err := next.RoundTrip(req)
        log.Println("←", resp.StatusCode)
        return resp, err
    })
}

c, _ := httpx.New(httpx.WithMiddleware(loggingMW))

// Built-in middlewares
httpx.HeaderInjector(map[string]string{"X-Service": "api-gateway"})
httpx.CorrelationIDInjector("X-Request-ID", func() string { return uuid.New().String() })
httpx.TimeoutMiddleware(5 * time.Second)
httpx.SingleflightMiddleware()
```

---

## Circuit Breaker

### Built-in (allow/record style)

```go
cb := httpx.NewCircuitBreaker(httpx.CircuitBreakerConfig{
    FailureThreshold: 5,
    SuccessThreshold: 2,
    OpenTimeout:      10 * time.Second,
})

c, _ := httpx.New(httpx.WithCircuitBreaker(cb))
```

### sony/gobreaker (execute style)

```go
import gbadapter "github.com/n0l3r/httpx/breaker/gobreaker"
import gb "github.com/sony/gobreaker/v2"

adapter := gbadapter.New(gbadapter.Config{
    Name:        "my-api",
    Timeout:     10 * time.Second,
    MaxRequests: 2,
    ReadyToTrip: func(c gb.Counts) bool {
        return c.ConsecutiveFailures > 5
    },
    OnStateChange: func(name string, from, to gb.State) {
        log.Printf("circuit %s: %s → %s", name, from, to)
    },
})

// Use WithExecutingCircuitBreaker (not WithCircuitBreaker) for execute-style CBs.
c, _ := httpx.New(httpx.WithExecutingCircuitBreaker(adapter))
```

---

## Rate Limiter

### In-memory (token bucket)

```go
// Global: 100 req/s, burst 10
c, _ := httpx.New(httpx.WithRateLimiter(
    httpx.NewGlobalRateLimiter(rate.Limit(100), 10),
))

// Per-host
c, _ := httpx.New(httpx.WithRateLimiter(
    httpx.NewPerHostRateLimiter(rate.Limit(50), 5, map[string]*rate.Limiter{
        "slow-api.example.com": rate.NewLimiter(rate.Limit(5), 1),
    }),
))
```

### Redis (sliding window)

```go
import redisrl "github.com/n0l3r/httpx/ratelimit/redis"

rdb := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
rl := redisrl.New(rdb, redisrl.Config{
    Rate:    100,          // requests per window
    Window:  time.Second,
    Burst:   10,
    PerHost: true,         // separate limit per target host
})

c, _ := httpx.New(httpx.WithRateLimiter(rl))
```

---

## Response Cache

### In-memory

```go
cache := httpx.NewMemoryCache(5 * time.Minute)
c, _ := httpx.New(httpx.WithCache(cache))
```

### Redis

```go
import rediscache "github.com/n0l3r/httpx/cache/redis"

rdb := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
cache := rediscache.New(rdb, rediscache.Config{
    KeyPrefix:  "myapp:http:",
    DefaultTTL: 5 * time.Minute,
})

c, _ := httpx.New(httpx.WithCache(cache))
```

### Tiered (L1 memory + L2 Redis)

```go
import (
    rediscache "github.com/n0l3r/httpx/cache/redis"
    "github.com/n0l3r/httpx/cache/tiered"
)

l1 := httpx.NewMemoryCache(30 * time.Second)      // fast, short TTL
l2 := rediscache.New(rdb, rediscache.DefaultConfig) // shared, long TTL

// L1 is checked first; miss falls through to L2 and back-fills L1.
c, _ := httpx.New(httpx.WithCache(tiered.New(l1, l2)))
```

### Noop (disable caching)

```go
c, _ := httpx.New(httpx.WithCache(httpx.NoopCache{}))
```

---

## Auth Helpers

### OAuth 1.0a

```go
transport := &auth.OAuth1Transport{
    Config: auth.OAuth1Config{
        ConsumerKey:    "key",
        ConsumerSecret: "secret",
        Token:          "token",
        TokenSecret:    "tokenSecret",
    },
}
c, _ := httpx.New(httpx.WithTransport(transport))
```

### OAuth 2.0

```go
transport := &auth.OAuth2Transport{
    Source: &auth.StaticTokenSource{AccessToken: "my-token"},
}
c, _ := httpx.New(httpx.WithTransport(transport))
```

### HMAC Signing

```go
transport := &auth.HMACTransport{
    Config: auth.HMACConfig{
        KeyID:  "key-1",
        Secret: []byte("super-secret"),
        Header: "X-Signature",
    },
}
```

### Idempotency Key

```go
transport := &auth.IdempotencyTransport{Header: "Idempotency-Key"}
```

---

## OpenTelemetry Tracing

```go
import "github.com/n0l3r/httpx/tracing"

transport := &tracing.Transport{
    Tracer:     otel.Tracer("my-service"),
    Propagator: otel.GetTextMapPropagator(),
}
c, _ := httpx.New(httpx.WithTransport(transport))
```

---

## Testing with Mock Transport

```go
import "github.com/n0l3r/httpx/mock"

mt := mock.NewMockTransport().
    OnGet("/users", func(req *http.Request) (*mock.Response, error) {
        return mock.NewJSONResponse(200, []User{{ID: 1, Name: "alice"}}), nil
    }).
    OnPost("/users", func(req *http.Request) (*mock.Response, error) {
        return mock.NewJSONResponse(201, User{ID: 2, Name: "bob"}), nil
    })

c, _ := httpx.New(httpx.WithTransport(mt))

// In tests
resp, err := c.Get(ctx, "http://fake/users")
fmt.Println(mt.CallCount()) // 1
```

---

## Error Handling

```go
resp, err := c.Get(ctx, "/api/data")
if err != nil {
    switch {
    case httpx.IsTimeout(err):
        // handle timeout
    case httpx.IsNetworkError(err):
        // handle network issue
    case httpx.IsStatus5xx(err):
        // handle server error
    case httpx.IsCanceled(err):
        // request was canceled
    }
    return err
}

// Ensure 2xx
if err := resp.EnsureSuccess(); err != nil {
    return err
}
```

---

## Response Helpers

```go
resp, _ := c.Get(ctx, "/data")

resp.StatusCode()      // int
resp.IsSuccess()       // 2xx
resp.IsClientError()   // 4xx
resp.IsServerError()   // 5xx

resp.Bytes()           // []byte
resp.String()          // string
resp.JSON(&myStruct)   // unmarshal JSON
resp.Header("X-RateLimit-Remaining")
```

---

## Project Structure

```
httpx/
├── client.go               # Client, New(), convenience methods
├── options.go              # All functional options
├── request.go              # Fluent RequestBuilder
├── response.go             # Response wrapper
├── error.go                # Custom error types + classification
├── retry.go                # Retry policy + conditions
├── backoff.go              # Backoff strategies
├── middleware.go           # Middleware chain + built-in middlewares
├── logging.go              # Logging hook
├── metrics.go              # Metrics hook
├── circuit_breaker.go      # SimpleCircuitBreaker + ExecutingCircuitBreaker interface
├── rate_limiter.go         # GlobalRateLimiter + PerHostRateLimiter (in-memory)
├── singleflight.go         # Request deduplication
├── cache.go                # MemoryCache + NoopCache + TieredCache
│
├── cache/
│   ├── redis/              # Redis cache backend
│   │   └── redis.go        # RedisCache (GET/SET with TTL)
│   └── tiered/             # Two-level cache
│       └── tiered.go       # TieredCache (L1+L2, back-fill on miss)
│
├── ratelimit/
│   └── redis/              # Redis rate limiter backend
│       └── redis.go        # RedisRateLimiter (sliding window via sorted sets)
│
├── breaker/
│   └── gobreaker/          # sony/gobreaker v2 adapter
│       └── gobreaker.go    # Adapter implements ExecutingCircuitBreaker
│
├── auth/
│   └── auth.go             # OAuth1, OAuth2, HMAC, IdempotencyKey
├── tracing/
│   └── tracing.go          # OpenTelemetry transport
├── mock/
│   └── mock.go             # MockTransport + helpers
└── internal/
    └── util.go             # Internal utilities
```

### Backend comparison

| Feature | Built-in | Redis backend |
|---|---|---|
| **Cache** | `httpx.MemoryCache` (single process) | `cache/redis.Cache` (distributed, shared across instances) |
| **Rate Limiter** | `httpx.GlobalRateLimiter` / `PerHostRateLimiter` (per-process) | `ratelimit/redis.RateLimiter` (distributed, sliding window) |
| **Circuit Breaker** | `httpx.SimpleCircuitBreaker` | `breaker/gobreaker.Adapter` (sony/gobreaker with richer state machine) |
| **Tiered Cache** | `httpx.TieredCache` (in `cache.go`) | `cache/tiered.Cache` (L1 memory + L2 any backend) |

---

## Examples

See **[httpx-example](https://github.com/n0l3r/httpx-example)** for a dedicated repository with runnable examples covering every feature:

```bash
git clone https://github.com/n0l3r/httpx-example
cd httpx-example
go run main.go           # run all categories
go run main.go retry     # run a specific category
```

Categories: `basic` · `retry` · `cache` · `circuit-breaker` · `rate-limiter` · `middleware` · `auth` · `tracing` · `singleflight` · `mock`

---

## License

This project is licensed under the [MIT License](LICENSE) — Copyright (c) 2026 n0l3r.
