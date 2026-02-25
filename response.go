package httpx

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Response wraps an *http.Response with helpers for reading the body.
type Response struct {
	// Raw is the underlying HTTP response.
	Raw *http.Response
	// body holds the already-read bytes so it can be read multiple times.
	body []byte
}

// newResponse reads the response body (up to maxBytes) and closes the original body.
func newResponse(raw *http.Response, maxBytes int64) (*Response, error) {
	if raw == nil {
		return nil, nil
	}

	r := raw.Body
	if r == nil {
		return &Response{Raw: raw}, nil
	}
	defer r.Close()

	// Decompress gzip if the server returned gzip and net/http didn't auto-decompress.
	if strings.EqualFold(raw.Header.Get("Content-Encoding"), "gzip") && raw.Uncompressed == false {
		gz, err := gzip.NewReader(r)
		if err == nil {
			defer gz.Close()
			r = gz
		}
	}

	var reader io.Reader = r
	if maxBytes > 0 {
		reader = io.LimitReader(r, maxBytes+1)
	}

	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("httpx: read body: %w", err)
	}

	if maxBytes > 0 && int64(len(b)) > maxBytes {
		return nil, ErrBodySizeLimitExceed
	}

	// Replace body with a no-op so callers that call raw.Body.Close() won't panic.
	raw.Body = io.NopCloser(strings.NewReader(string(b)))

	return &Response{Raw: raw, body: b}, nil
}

// -------------------------------------------------------------------
// Status helpers
// -------------------------------------------------------------------

// StatusCode returns the HTTP status code.
func (r *Response) StatusCode() int {
	if r.Raw == nil {
		return 0
	}
	return r.Raw.StatusCode
}

// IsSuccess returns true if the status code is 2xx.
func (r *Response) IsSuccess() bool {
	c := r.StatusCode()
	return c >= 200 && c < 300
}

// IsClientError returns true if the status code is 4xx.
func (r *Response) IsClientError() bool {
	c := r.StatusCode()
	return c >= 400 && c < 500
}

// IsServerError returns true if the status code is 5xx.
func (r *Response) IsServerError() bool {
	c := r.StatusCode()
	return c >= 500 && c < 600
}

// IsRedirect returns true if the status code is 3xx.
func (r *Response) IsRedirect() bool {
	c := r.StatusCode()
	return c >= 300 && c < 400
}

// -------------------------------------------------------------------
// Body helpers
// -------------------------------------------------------------------

// Bytes returns the raw response body bytes.
func (r *Response) Bytes() []byte {
	return r.body
}

// String returns the response body as a string.
func (r *Response) String() string {
	return string(r.body)
}

// JSON decodes the response body into v.
func (r *Response) JSON(v interface{}) error {
	if err := json.Unmarshal(r.body, v); err != nil {
		return fmt.Errorf("httpx: decode JSON (status %d): %w", r.StatusCode(), err)
	}
	return nil
}

// Header returns the value of the named response header.
func (r *Response) Header(key string) string {
	if r.Raw == nil {
		return ""
	}
	return r.Raw.Header.Get(key)
}

// EnsureSuccess returns an *Error if the response is not 2xx, nil otherwise.
func (r *Response) EnsureSuccess() error {
	if r.IsSuccess() {
		return nil
	}
	return &Error{
		StatusCode: r.StatusCode(),
		Kind:       classifyError(nil, r.StatusCode()),
		Err:        fmt.Errorf("unexpected status: %d", r.StatusCode()),
	}
}
