// Package mock provides test utilities for httpx.
package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// Response is a canned HTTP response returned by MockTransport.
type Response struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

// NewResponse creates a mock response with the given status and body.
func NewResponse(statusCode int, body []byte) *Response {
	return &Response{StatusCode: statusCode, Body: body}
}

// NewJSONResponse creates a mock response that marshals v as JSON.
func NewJSONResponse(statusCode int, v interface{}) *Response {
	b, _ := json.Marshal(v)
	return &Response{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       b,
	}
}

// ToHTTPResponse converts the mock response to a real *http.Response.
func (r *Response) ToHTTPResponse(req *http.Request) *http.Response {
	resp := &http.Response{
		StatusCode: r.StatusCode,
		Header:     make(http.Header),
		Request:    req,
	}
	if r.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	for k, v := range r.Headers {
		resp.Header.Set(k, v)
	}
	if r.Body != nil {
		resp.Body = io.NopCloser(bytes.NewReader(r.Body))
	} else {
		resp.Body = http.NoBody
	}
	return resp
}

// -------------------------------------------------------------------
// MockTransport
// -------------------------------------------------------------------

// Handler is a function that handles a mock request.
type Handler func(req *http.Request) (*Response, error)

// MockTransport is an http.RoundTripper that intercepts requests for testing.
type MockTransport struct {
	mu       sync.Mutex
	handlers []handler
	// Default is called when no route matches. Returns 404 by default.
	Default Handler
	// Requests records all requests that were processed.
	Requests []*http.Request
}

type handler struct {
	method  string // empty = any
	path    string // empty = any
	fn      Handler
	matched bool
}

// NewMockTransport creates an empty MockTransport.
func NewMockTransport() *MockTransport {
	return &MockTransport{}
}

// On registers a handler for the given HTTP method and path.
// Use empty strings to match any method or any path.
func (m *MockTransport) On(method, path string, fn Handler) *MockTransport {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = append(m.handlers, handler{method: method, path: path, fn: fn})
	return m
}

// OnGet registers a GET handler.
func (m *MockTransport) OnGet(path string, fn Handler) *MockTransport {
	return m.On(http.MethodGet, path, fn)
}

// OnPost registers a POST handler.
func (m *MockTransport) OnPost(path string, fn Handler) *MockTransport {
	return m.On(http.MethodPost, path, fn)
}

// OnPut registers a PUT handler.
func (m *MockTransport) OnPut(path string, fn Handler) *MockTransport {
	return m.On(http.MethodPut, path, fn)
}

// OnDelete registers a DELETE handler.
func (m *MockTransport) OnDelete(path string, fn Handler) *MockTransport {
	return m.On(http.MethodDelete, path, fn)
}

// RoundTrip implements http.RoundTripper.
func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	m.Requests = append(m.Requests, req)
	var matched *handler
	for i := range m.handlers {
		h := &m.handlers[i]
		methodOK := h.method == "" || h.method == req.Method
		pathOK := h.path == "" || h.path == req.URL.Path
		if methodOK && pathOK {
			matched = h
			h.matched = true
			break
		}
	}
	m.mu.Unlock()

	if matched != nil {
		r, err := matched.fn(req)
		if err != nil {
			return nil, err
		}
		return r.ToHTTPResponse(req), nil
	}

	if m.Default != nil {
		r, err := m.Default(req)
		if err != nil {
			return nil, err
		}
		return r.ToHTTPResponse(req), nil
	}

	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

// Reset clears all registered handlers and recorded requests.
func (m *MockTransport) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = nil
	m.Requests = nil
}

// CallCount returns the number of recorded requests.
func (m *MockTransport) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Requests)
}
