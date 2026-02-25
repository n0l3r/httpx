package httpx

import (
	"log/slog"
	"net/http"
	"time"
)

// LogHook is called after each HTTP request completes.
type LogHook interface {
	LogRequest(event LogEvent)
}

// LogEvent contains information about a completed HTTP request/response cycle.
type LogEvent struct {
	Method     string
	URL        string
	StatusCode int
	Duration   time.Duration
	Attempt    int
	Err        error
}

// LogHookFunc is a function adapter for LogHook.
type LogHookFunc func(LogEvent)

func (f LogHookFunc) LogRequest(e LogEvent) { f(e) }

// -------------------------------------------------------------------
// slog hook
// -------------------------------------------------------------------

type slogHook struct{ l *slog.Logger }

func newSlogHook(l *slog.Logger) LogHook { return &slogHook{l: l} }

func (h *slogHook) LogRequest(e LogEvent) {
	attrs := []any{
		slog.String("method", e.Method),
		slog.String("url", e.URL),
		slog.Int("status", e.StatusCode),
		slog.Duration("duration", e.Duration),
	}
	if e.Attempt > 0 {
		attrs = append(attrs, slog.Int("attempt", e.Attempt))
	}
	if e.Err != nil {
		attrs = append(attrs, slog.String("error", e.Err.Error()))
		h.l.Error("http request", attrs...)
	} else {
		h.l.Info("http request", attrs...)
	}
}

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

func loggingMiddleware(hook LogHook) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			start := time.Now()
			resp, err := next.RoundTrip(req)
			duration := time.Since(start)

			statusCode := 0
			if resp != nil {
				statusCode = resp.StatusCode
			}

			hook.LogRequest(LogEvent{
				Method:     req.Method,
				URL:        req.URL.String(),
				StatusCode: statusCode,
				Duration:   duration,
				Err:        err,
			})

			return resp, err
		})
	}
}
