package httpx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// FormFile represents a file field in a multipart form upload.
type FormFile struct {
	// FieldName is the form field name (e.g. "avatar").
	FieldName string
	// FileName is the filename sent to the server (e.g. "photo.jpg").
	FileName string
	// Content is the file content reader.
	Content io.Reader
	// ContentType is the MIME type of the file (e.g. "image/jpeg").
	// Defaults to "application/octet-stream" if empty.
	ContentType string
}

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

// BodyForm encodes fields as application/x-www-form-urlencoded and sets it as the request body.
func (rb *RequestBuilder) BodyForm(fields url.Values) *RequestBuilder {
	rb.body = io.NopCloser(strings.NewReader(fields.Encode()))
	rb.headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return rb
}

// BodyMultipart builds a multipart/form-data body from text fields and file uploads.
// fields contains plain form values; files contains file attachments (may be nil).
func (rb *RequestBuilder) BodyMultipart(fields map[string]string, files []FormFile) *RequestBuilder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			rb.err = fmt.Errorf("httpx: write multipart field %q: %w", k, err)
			return rb
		}
	}

	for _, f := range files {
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{
			fmt.Sprintf(`form-data; name="%s"; filename="%s"`, f.FieldName, f.FileName),
		}
		h["Content-Type"] = []string{ct}
		part, err := mw.CreatePart(h)
		if err != nil {
			rb.err = fmt.Errorf("httpx: create multipart part %q: %w", f.FieldName, err)
			return rb
		}
		if _, err := io.Copy(part, f.Content); err != nil {
			rb.err = fmt.Errorf("httpx: write multipart file %q: %w", f.FieldName, err)
			return rb
		}
	}

	if err := mw.Close(); err != nil {
		rb.err = fmt.Errorf("httpx: close multipart writer: %w", err)
		return rb
	}

	rb.body = io.NopCloser(&buf)
	rb.headers.Set("Content-Type", mw.FormDataContentType())
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
