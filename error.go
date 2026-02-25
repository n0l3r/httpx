package httpx

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrorKind classifies the category of an HTTP error.
type ErrorKind string

const (
	ErrorKindTimeout    ErrorKind = "timeout"
	ErrorKindNetwork    ErrorKind = "network"
	ErrorKindStatus4xx  ErrorKind = "4xx"
	ErrorKindStatus5xx  ErrorKind = "5xx"
	ErrorKindCanceled   ErrorKind = "canceled"
	ErrorKindUnknown    ErrorKind = "unknown"
)

// Error is a structured error type that carries context about a failed HTTP request.
type Error struct {
	// Op is the operation that caused the error (e.g. "GET", "POST").
	Op string
	// URL is the request URL.
	URL string
	// StatusCode is the HTTP status code, 0 if the error occurred before receiving a response.
	StatusCode int
	// Kind classifies the error.
	Kind ErrorKind
	// Err is the underlying cause.
	Err error
}

func (e *Error) Error() string {
	var sb strings.Builder
	sb.WriteString("httpx: ")
	if e.Op != "" {
		sb.WriteString(e.Op)
		sb.WriteString(" ")
	}
	if e.URL != "" {
		sb.WriteString(e.URL)
		sb.WriteString(" — ")
	}
	if e.StatusCode != 0 {
		sb.WriteString(fmt.Sprintf("status %d", e.StatusCode))
		if e.Err != nil {
			sb.WriteString(": ")
		}
	}
	if e.Err != nil {
		sb.WriteString(e.Err.Error())
	}
	return sb.String()
}

func (e *Error) Unwrap() error { return e.Err }

// IsTimeout reports whether err represents a timeout error.
func IsTimeout(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == ErrorKindTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

// IsNetworkError reports whether err is a network-level error.
func IsNetworkError(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == ErrorKindNetwork
	}
	var ne *net.OpError
	return errors.As(err, &ne)
}

// IsStatus4xx reports whether err is due to a 4xx HTTP status code.
func IsStatus4xx(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == ErrorKindStatus4xx
}

// IsStatus5xx reports whether err is due to a 5xx HTTP status code.
func IsStatus5xx(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == ErrorKindStatus5xx
}

// IsCanceled reports whether err was caused by context cancellation.
func IsCanceled(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == ErrorKindCanceled
}

// classifyError derives an ErrorKind from a raw error and optional status code.
func classifyError(err error, statusCode int) ErrorKind {
	if statusCode >= 500 {
		return ErrorKindStatus5xx
	}
	if statusCode >= 400 {
		return ErrorKindStatus4xx
	}
	if err == nil {
		return ErrorKindUnknown
	}
	if errors.Is(err, ErrRequestCanceled) {
		return ErrorKindCanceled
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrorKindTimeout
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		return ErrorKindNetwork
	}
	return ErrorKindUnknown
}

// Sentinel errors.
var (
	ErrRequestCanceled    = errors.New("request canceled")
	ErrCircuitOpen        = errors.New("circuit breaker is open")
	ErrRateLimitExceeded  = errors.New("rate limit exceeded")
	ErrBodySizeLimitExceed = errors.New("response body size limit exceeded")
	ErrCacheMiss          = errors.New("cache miss")
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")
	ErrNoHostsAvailable   = errors.New("no hosts available")
)
