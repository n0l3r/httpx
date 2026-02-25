package httpx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/n0l3r/httpx/internal"
)

// RetryConditionFunc is a function that decides whether a given response/error
// warrants a retry. Return true to retry.
type RetryConditionFunc func(resp *http.Response, err error) bool

// RetryPolicy describes how and when to retry failed requests.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts (including the first).
	MaxAttempts int
	// Backoff computes the wait duration before attempt n (0-indexed).
	Backoff BackoffStrategy
	// Conditions is a list of functions; if any returns true the request is retried.
	// If empty, a sensible default is used.
	Conditions []RetryConditionFunc
	// OnRetry is an optional callback invoked before each retry.
	OnRetry func(attempt int, req *http.Request, resp *http.Response, err error)
	// RetryOnlyIdempotent, when true, skips retrying non-idempotent methods (POST, PATCH).
	RetryOnlyIdempotent bool
}

// DefaultRetryPolicy returns a sensible default retry policy:
// 3 attempts, exponential backoff, retry on network errors and 5xx/429.
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts: 3,
		Backoff:     DefaultBackoff,
		Conditions:  []RetryConditionFunc{RetryOnNetworkError, RetryOnStatus5xx, RetryOnStatus429},
	}
}

// RetryOnNetworkError retries when there was a network-level error (no response).
func RetryOnNetworkError(resp *http.Response, err error) bool {
	return err != nil && resp == nil
}

// RetryOnStatus5xx retries on 5xx status codes.
func RetryOnStatus5xx(resp *http.Response, err error) bool {
	return resp != nil && resp.StatusCode >= 500 && resp.StatusCode < 600
}

// RetryOnStatus429 retries on HTTP 429 Too Many Requests.
func RetryOnStatus429(resp *http.Response, err error) bool {
	return resp != nil && resp.StatusCode == http.StatusTooManyRequests
}

// RetryOnStatuses returns a RetryConditionFunc that retries on the given status codes.
func RetryOnStatuses(codes ...int) RetryConditionFunc {
	set := make(map[int]struct{}, len(codes))
	for _, c := range codes {
		set[c] = struct{}{}
	}
	return func(resp *http.Response, err error) bool {
		if resp == nil {
			return false
		}
		_, ok := set[resp.StatusCode]
		return ok
	}
}

// RetryOnErrors returns a RetryConditionFunc that retries when err matches any of targets.
func RetryOnErrors(targets ...error) RetryConditionFunc {
	return func(_ *http.Response, err error) bool {
		for _, t := range targets {
			if errors.Is(err, t) {
				return true
			}
		}
		return false
	}
}

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

func retryMiddleware(policy *RetryPolicy) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if policy.RetryOnlyIdempotent && !internal.IsRetryableMethod(req.Method) {
				return next.RoundTrip(req)
			}

			// Buffer the body so we can replay it on retries.
			var bodyBytes []byte
			if req.Body != nil && req.Body != http.NoBody {
				var err error
				bodyBytes, err = io.ReadAll(req.Body)
				if err != nil {
					return nil, fmt.Errorf("httpx/retry: read body: %w", err)
				}
				_ = req.Body.Close()
			}

			maxAttempts := policy.MaxAttempts
			if maxAttempts <= 0 {
				maxAttempts = 1
			}

			conditions := policy.Conditions
			if len(conditions) == 0 {
				conditions = []RetryConditionFunc{RetryOnNetworkError, RetryOnStatus5xx, RetryOnStatus429}
			}

			var (
				resp *http.Response
				err  error
			)

			for attempt := 0; attempt < maxAttempts; attempt++ {
				// Restore body for each attempt.
				if bodyBytes != nil {
					req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				}

				resp, err = next.RoundTrip(req)

				// Check if we should retry.
				if attempt < maxAttempts-1 && shouldRetry(conditions, resp, err) {
					// Drain and close before retry to allow connection reuse.
					if resp != nil {
						internal.DrainAndClose(resp.Body)
					}

					if policy.OnRetry != nil {
						policy.OnRetry(attempt+1, req, resp, err)
					}

					backoffDuration := policy.Backoff(attempt)
					if backoffDuration > 0 {
						select {
						case <-req.Context().Done():
							return nil, req.Context().Err()
						case <-time.After(backoffDuration):
						}
					}
					continue
				}
				break
			}

			if err != nil {
				return nil, err
			}
			return resp, nil
		})
	}
}

func shouldRetry(conditions []RetryConditionFunc, resp *http.Response, err error) bool {
	for _, c := range conditions {
		if c(resp, err) {
			return true
		}
	}
	return false
}
