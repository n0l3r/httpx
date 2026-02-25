package httpx

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // Normal operation.
	CircuitOpen                         // Failing; requests are rejected.
	CircuitHalfOpen                     // Trial period; one request is allowed through.
)

// CircuitBreakerProvider decides whether a request should be allowed.
type CircuitBreakerProvider interface {
	// Allow returns nil if the request may proceed, ErrCircuitOpen otherwise.
	Allow(host string) error
	// RecordSuccess records a successful call for the given host.
	RecordSuccess(host string)
	// RecordFailure records a failed call for the given host.
	RecordFailure(host string)
}

// CircuitBreakerConfig controls the behaviour of the default circuit breaker.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures needed to open the circuit.
	FailureThreshold int
	// SuccessThreshold is the number of consecutive successes in half-open state needed to close.
	SuccessThreshold int
	// OpenTimeout is how long the circuit stays open before switching to half-open.
	OpenTimeout time.Duration
}

// DefaultCircuitBreakerConfig is a sensible default.
var DefaultCircuitBreakerConfig = CircuitBreakerConfig{
	FailureThreshold: 5,
	SuccessThreshold: 2,
	OpenTimeout:      10 * time.Second,
}

// circuitBreakerState tracks state per host.
type circuitBreakerState struct {
	mu               sync.Mutex
	state            CircuitState
	failures         int
	successes        int
	lastFailure      time.Time
	config           CircuitBreakerConfig
}

func (s *circuitBreakerState) allow() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.state {
	case CircuitOpen:
		if time.Since(s.lastFailure) >= s.config.OpenTimeout {
			s.state = CircuitHalfOpen
			s.successes = 0
			return nil
		}
		return fmt.Errorf("%w", ErrCircuitOpen)
	case CircuitHalfOpen:
		return nil
	default: // Closed
		return nil
	}
}

func (s *circuitBreakerState) recordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = 0
	if s.state == CircuitHalfOpen {
		s.successes++
		if s.successes >= s.config.SuccessThreshold {
			s.state = CircuitClosed
			s.successes = 0
		}
	}
}

func (s *circuitBreakerState) recordFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	s.lastFailure = time.Now()
	if s.state == CircuitHalfOpen || s.failures >= s.config.FailureThreshold {
		s.state = CircuitOpen
	}
}

// SimpleCircuitBreaker is a per-host circuit breaker.
type SimpleCircuitBreaker struct {
	mu     sync.Mutex
	states map[string]*circuitBreakerState
	config CircuitBreakerConfig
}

// NewCircuitBreaker creates a new SimpleCircuitBreaker with the given config.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *SimpleCircuitBreaker {
	return &SimpleCircuitBreaker{
		states: make(map[string]*circuitBreakerState),
		config: cfg,
	}
}

func (cb *SimpleCircuitBreaker) getState(host string) *circuitBreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	s, ok := cb.states[host]
	if !ok {
		s = &circuitBreakerState{config: cb.config}
		cb.states[host] = s
	}
	return s
}

func (cb *SimpleCircuitBreaker) Allow(host string) error {
	return cb.getState(host).allow()
}

func (cb *SimpleCircuitBreaker) RecordSuccess(host string) {
	cb.getState(host).recordSuccess()
}

func (cb *SimpleCircuitBreaker) RecordFailure(host string) {
	cb.getState(host).recordFailure()
}

// -------------------------------------------------------------------
// ExecutingCircuitBreaker — for execute-style implementations (e.g. gobreaker)
// -------------------------------------------------------------------

// ExecutingCircuitBreaker is an alternative interface for circuit breakers
// that follow an "execute" pattern rather than "allow/record".
// sony/gobreaker and similar libraries use this model.
type ExecutingCircuitBreaker interface {
	// Execute runs fn inside the circuit breaker.
	// Returns ErrCircuitOpen (or the library's own error) if the circuit is open.
	Execute(host string, fn func() (*http.Response, error)) (*http.Response, error)
}

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

func circuitBreakerMiddleware(cb CircuitBreakerProvider) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			host := req.URL.Host
			if err := cb.Allow(host); err != nil {
				return nil, err
			}

			resp, err := next.RoundTrip(req)

			if err != nil || (resp != nil && resp.StatusCode >= 500) {
				cb.RecordFailure(host)
			} else {
				cb.RecordSuccess(host)
			}

			return resp, err
		})
	}
}

func executingCircuitBreakerMiddleware(cb ExecutingCircuitBreaker) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return cb.Execute(req.URL.Host, func() (*http.Response, error) {
				return next.RoundTrip(req)
			})
		})
	}
}
