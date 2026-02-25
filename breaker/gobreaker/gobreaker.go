// Package gobreaker provides an adapter that wraps sony/gobreaker v2
// as an httpx.ExecutingCircuitBreaker.
//
// Because sony/gobreaker uses an "execute" pattern, use WithExecutingCircuitBreaker
// (not WithCircuitBreaker) when registering this adapter.
//
// Usage:
//
//	adapter := gobreakeradapter.New(gobreakeradapter.Config{
//	    Name:    "my-api",
//	    Timeout: 5 * time.Second,
//	    ReadyToTrip: func(c gobreaker.Counts) bool {
//	        return c.ConsecutiveFailures > 5
//	    },
//	})
//	c, _ := httpx.New(httpx.WithExecutingCircuitBreaker(adapter))
package gobreaker

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	gb "github.com/sony/gobreaker/v2"
)

// Config mirrors gobreaker.Settings with friendly defaults.
type Config struct {
	// Name is the base circuit breaker name. Host is appended per-breaker.
	Name string
	// MaxRequests allowed in half-open state. Default: 1.
	MaxRequests uint32
	// Interval is the cyclic period for the closed state. Default: 10s.
	Interval time.Duration
	// Timeout before transitioning from open to half-open. Default: 10s.
	Timeout time.Duration
	// ReadyToTrip is called on every failure to decide whether to open.
	// Default: opens after 5 consecutive failures.
	ReadyToTrip func(counts gb.Counts) bool
	// OnStateChange is called when the circuit transitions state.
	OnStateChange func(name string, from, to gb.State)
	// IsSuccessful determines if an HTTP response counts as a success.
	// Default: status < 500 and no error.
	IsSuccessful func(resp *http.Response, err error) bool
}

// Adapter wraps a per-host map of gobreaker.CircuitBreaker instances.
// It implements httpx.ExecutingCircuitBreaker.
type Adapter struct {
	mu       sync.Mutex
	breakers map[string]*gb.CircuitBreaker[*http.Response]
	config   Config
}

// New creates a new gobreaker-backed circuit breaker adapter.
func New(cfg Config) *Adapter {
	if cfg.Name == "" {
		cfg.Name = "httpx"
	}
	if cfg.MaxRequests == 0 {
		cfg.MaxRequests = 1
	}
	if cfg.Interval == 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.ReadyToTrip == nil {
		cfg.ReadyToTrip = func(c gb.Counts) bool {
			return c.ConsecutiveFailures > 5
		}
	}
	if cfg.IsSuccessful == nil {
		cfg.IsSuccessful = func(resp *http.Response, err error) bool {
			return err == nil && resp != nil && resp.StatusCode < 500
		}
	}
	return &Adapter{
		breakers: make(map[string]*gb.CircuitBreaker[*http.Response]),
		config:   cfg,
	}
}

func (a *Adapter) getBreaker(host string) *gb.CircuitBreaker[*http.Response] {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.breakers[host]
	if !ok {
		settings := gb.Settings{
			Name:        fmt.Sprintf("%s:%s", a.config.Name, host),
			MaxRequests: a.config.MaxRequests,
			Interval:    a.config.Interval,
			Timeout:     a.config.Timeout,
			ReadyToTrip: a.config.ReadyToTrip,
			IsSuccessful: func(err error) bool {
				return err == nil
			},
			OnStateChange: a.config.OnStateChange,
		}
		b = gb.NewCircuitBreaker[*http.Response](settings)
		a.breakers[host] = b
	}
	return b
}

// Execute runs fn inside the circuit breaker for the given host.
// Implements httpx.ExecutingCircuitBreaker.
func (a *Adapter) Execute(host string, fn func() (*http.Response, error)) (*http.Response, error) {
	b := a.getBreaker(host)

	resp, err := b.Execute(func() (*http.Response, error) {
		r, e := fn()
		if !a.config.IsSuccessful(r, e) {
			if e != nil {
				return r, e
			}
			// Treat non-successful HTTP status as an error for the circuit breaker.
			return r, fmt.Errorf("circuit: unhealthy response status %d", r.StatusCode)
		}
		return r, nil
	})

	// If the circuit returned an error wrapping a real HTTP response
	// (e.g. 5xx), still return the response so callers can inspect it.
	return resp, err
}

// State returns the current circuit state for a host.
func (a *Adapter) State(host string) gb.State {
	return a.getBreaker(host).State()
}

