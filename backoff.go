package httpx

import (
	"math"
	"math/rand/v2"
	"time"
)

// BackoffStrategy computes the wait duration for a given attempt number (0-indexed).
type BackoffStrategy func(attempt int) time.Duration

// ExponentialBackoff returns a BackoffStrategy that doubles the delay on each attempt,
// starting from base and capped at max. A random jitter of ±jitterFactor is added.
//
// Formula: min(base * 2^attempt, max) * (1 ± jitterFactor)
func ExponentialBackoff(base, max time.Duration, jitterFactor float64) BackoffStrategy {
	return func(attempt int) time.Duration {
		exp := math.Pow(2, float64(attempt))
		delay := float64(base) * exp
		if delay > float64(max) {
			delay = float64(max)
		}
		// Add jitter: delay * [1-jitter, 1+jitter]
		if jitterFactor > 0 {
			jitter := jitterFactor * (rand.Float64()*2 - 1) // [-jitter, +jitter]
			delay = delay * (1 + jitter)
		}
		if delay < 0 {
			delay = 0
		}
		return time.Duration(delay)
	}
}

// FullJitterBackoff implements the "Full Jitter" strategy:
// sleep = random between 0 and min(cap, base * 2^attempt)
// Described in https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
func FullJitterBackoff(base, cap time.Duration) BackoffStrategy {
	return func(attempt int) time.Duration {
		ceiling := math.Min(float64(cap), float64(base)*math.Pow(2, float64(attempt)))
		return time.Duration(rand.Float64() * ceiling)
	}
}

// ConstantBackoff returns a BackoffStrategy that always waits the same duration.
func ConstantBackoff(d time.Duration) BackoffStrategy {
	return func(_ int) time.Duration { return d }
}

// LinearBackoff returns a BackoffStrategy that increases delay linearly.
func LinearBackoff(base, increment time.Duration) BackoffStrategy {
	return func(attempt int) time.Duration {
		return base + time.Duration(attempt)*increment
	}
}

// DefaultBackoff is the default exponential backoff with full jitter.
var DefaultBackoff = FullJitterBackoff(200*time.Millisecond, 10*time.Second)
