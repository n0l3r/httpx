package httpx

import (
	"net/http"
	"time"
)

// MetricsEvent contains data for a single HTTP request measurement.
type MetricsEvent struct {
	Method     string
	Host       string
	URL        string
	StatusCode int
	Duration   time.Duration
	Err        error
	Retries    int
}

// MetricsHook is implemented by anything that wants to receive metrics events.
type MetricsHook interface {
	RecordRequest(event MetricsEvent)
}

// MetricsHookFunc is a function adapter for MetricsHook.
type MetricsHookFunc func(MetricsEvent)

func (f MetricsHookFunc) RecordRequest(e MetricsEvent) { f(e) }

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

func metricsMiddleware(hook MetricsHook) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			start := time.Now()
			resp, err := next.RoundTrip(req)
			duration := time.Since(start)

			statusCode := 0
			if resp != nil {
				statusCode = resp.StatusCode
			}

			hook.RecordRequest(MetricsEvent{
				Method:     req.Method,
				Host:       req.URL.Host,
				URL:        req.URL.String(),
				StatusCode: statusCode,
				Duration:   duration,
				Err:        err,
			})

			return resp, err
		})
	}
}
