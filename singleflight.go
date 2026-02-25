package httpx

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"

	"golang.org/x/sync/singleflight"
)

// singleflightGroup wraps singleflight.Group for HTTP responses.
// Because singleflight shares one *http.Response across callers we must
// buffer the body before sharing.
type singleflightGroup struct {
	sg singleflight.Group
}

func newSingleflightGroup() *singleflightGroup { return &singleflightGroup{} }

type cachedResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

func (g *singleflightGroup) Do(key string, fn func() (*http.Response, error)) (*http.Response, error) {
	v, err, _ := g.sg.Do(key, func() (interface{}, error) {
		resp, err := fn()
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("httpx/singleflight: read body: %w", readErr)
		}
		return &cachedResponse{
			statusCode: resp.StatusCode,
			header:     resp.Header.Clone(),
			body:       body,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	cr := v.(*cachedResponse)
	// Reconstruct a fresh *http.Response for each caller.
	r := &http.Response{
		StatusCode: cr.statusCode,
		Header:     cr.header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(cr.body)),
	}
	return r, nil
}

// -------------------------------------------------------------------
// Singleflight RoundTripper (standalone middleware)
// -------------------------------------------------------------------

// SingleflightMiddleware deduplicates in-flight GET requests with the same URL.
func SingleflightMiddleware() Middleware {
	sfg := &singleflightMiddleware{sg: newSingleflightGroup()}
	return sfg.wrap
}

type singleflightMiddleware struct {
	mu sync.Mutex
	sg *singleflightGroup
}

func (s *singleflightMiddleware) wrap(next http.RoundTripper) http.RoundTripper {
	return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			return next.RoundTrip(req)
		}
		key := req.URL.String()
		return s.sg.Do(key, func() (*http.Response, error) {
			return next.RoundTrip(req)
		})
	})
}
