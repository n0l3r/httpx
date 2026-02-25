package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// RequestOption is a functional option applied to a RequestBuilder.
type RequestOption func(*RequestBuilder)

// RequestBuilder provides a fluent API for constructing an *http.Request.
type RequestBuilder struct {
	ctx         context.Context
	method      string
	rawURL      string
	headers     http.Header
	queryParams url.Values
	body        io.ReadCloser
	err         error
}

// NewRequestBuilder creates a standalone RequestBuilder (without a Client).
func NewRequestBuilder(ctx context.Context, method, rawURL string) *RequestBuilder {
	return &RequestBuilder{
		ctx:         ctx,
		method:      strings.ToUpper(method),
		rawURL:      rawURL,
		headers:     make(http.Header),
		queryParams: make(url.Values),
	}
}

// -------------------------------------------------------------------
// Fluent setters
// -------------------------------------------------------------------

// Header sets a single request header.
func (rb *RequestBuilder) Header(key, value string) *RequestBuilder {
	rb.headers.Set(key, value)
	return rb
}

// Headers sets multiple request headers from a map.
func (rb *RequestBuilder) Headers(h map[string]string) *RequestBuilder {
	for k, v := range h {
		rb.headers.Set(k, v)
	}
	return rb
}

// AddHeader appends a value to an existing header.
func (rb *RequestBuilder) AddHeader(key, value string) *RequestBuilder {
	rb.headers.Add(key, value)
	return rb
}

// Query sets a URL query parameter.
func (rb *RequestBuilder) Query(key, value string) *RequestBuilder {
	if rb.queryParams == nil {
		rb.queryParams = make(url.Values)
	}
	rb.queryParams.Set(key, value)
	return rb
}

// QueryValues sets multiple URL query parameters.
func (rb *RequestBuilder) QueryValues(params url.Values) *RequestBuilder {
	if rb.queryParams == nil {
		rb.queryParams = make(url.Values)
	}
	for k, vs := range params {
		for _, v := range vs {
			rb.queryParams.Add(k, v)
		}
	}
	return rb
}

// Body sets the request body and optional content type.
func (rb *RequestBuilder) Body(r io.ReadCloser, contentType string) *RequestBuilder {
	rb.body = r
	if contentType != "" {
		rb.headers.Set("Content-Type", contentType)
	}
	return rb
}

// BodyBytes sets raw bytes as the request body.
func (rb *RequestBuilder) BodyBytes(b []byte, contentType string) *RequestBuilder {
	WithRawBody(b, contentType)(rb)
	return rb
}

// BodyJSON marshals v as JSON and sets it as the request body.
func (rb *RequestBuilder) BodyJSON(v interface{}) *RequestBuilder {
	WithJSONBody(v)(rb)
	return rb
}

// Accept sets the Accept header.
func (rb *RequestBuilder) Accept(mime string) *RequestBuilder {
	rb.headers.Set("Accept", mime)
	return rb
}

// ContentType sets the Content-Type header.
func (rb *RequestBuilder) ContentType(ct string) *RequestBuilder {
	rb.headers.Set("Content-Type", ct)
	return rb
}

// BearerToken sets the Authorization: Bearer <token> header.
func (rb *RequestBuilder) BearerToken(token string) *RequestBuilder {
	rb.headers.Set("Authorization", "Bearer "+token)
	return rb
}

// BasicAuth sets the HTTP Basic Authentication header.
func (rb *RequestBuilder) BasicAuth(username, password string) *RequestBuilder {
	req, _ := http.NewRequest("GET", "http://x", nil)
	req.SetBasicAuth(username, password)
	rb.headers.Set("Authorization", req.Header.Get("Authorization"))
	return rb
}

// Context sets the request context.
func (rb *RequestBuilder) Context(ctx context.Context) *RequestBuilder {
	rb.ctx = ctx
	return rb
}

// -------------------------------------------------------------------
// Apply a RequestOption
// -------------------------------------------------------------------

// Apply applies one or more RequestOptions to the builder.
func (rb *RequestBuilder) Apply(opts ...RequestOption) *RequestBuilder {
	for _, o := range opts {
		o(rb)
	}
	return rb
}

// -------------------------------------------------------------------
// Build
// -------------------------------------------------------------------

// Build constructs the *http.Request.
func (rb *RequestBuilder) Build() (*http.Request, error) {
	if rb.err != nil {
		return nil, rb.err
	}

	u, err := url.Parse(rb.rawURL)
	if err != nil {
		return nil, fmt.Errorf("httpx: parse URL %q: %w", rb.rawURL, err)
	}

	// Merge query params.
	if len(rb.queryParams) > 0 {
		existing := u.Query()
		for k, vs := range rb.queryParams {
			for _, v := range vs {
				existing.Add(k, v)
			}
		}
		u.RawQuery = existing.Encode()
	}

	ctx := rb.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, rb.method, u.String(), rb.body)
	if err != nil {
		return nil, fmt.Errorf("httpx: build request: %w", err)
	}

	for k, vs := range rb.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	return req, nil
}
