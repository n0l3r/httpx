package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"golang.org/x/net/http2"

	"github.com/n0l3r/httpx/internal"
)

// Doer is the minimal interface for executing HTTP requests.
// It is satisfied by *http.Client and by Client.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is the main HTTP client with all production features.
type Client struct {
	cfg        *clientConfig
	httpClient *http.Client
	transport  http.RoundTripper
	sf         *singleflightGroup
}

// New creates a new Client with the provided options.
func New(opts ...Option) (*Client, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, fmt.Errorf("httpx: build transport: %w", err)
	}

	// Wrap transport with middleware chain.
	wrapped := buildMiddlewareChain(transport, cfg)

	httpClient := &http.Client{
		Timeout:   cfg.timeout,
		Transport: wrapped,
	}

	c := &Client{
		cfg:        cfg,
		httpClient: httpClient,
		transport:  wrapped,
	}

	if cfg.singleflightEnabled {
		c.sf = newSingleflightGroup()
	}

	return c, nil
}

// MustNew creates a new Client and panics on error.
func MustNew(opts ...Option) *Client {
	c, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return c
}

// -------------------------------------------------------------------
// Core Do
// -------------------------------------------------------------------

// Do executes the given *http.Request and returns a *Response.
func (c *Client) Do(req *http.Request) (*Response, error) {
	return c.doWithContext(req.Context(), req)
}

// DoRaw executes a raw *http.Request and returns a raw *http.Response.
// The caller is responsible for closing the body.
func (c *Client) DoRaw(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

func (c *Client) doWithContext(ctx context.Context, req *http.Request) (*Response, error) {
	if c.cfg.beforeRequest != nil {
		c.cfg.beforeRequest(ctx, req)
	}

	var (
		rawResp *http.Response
		err     error
	)

	key := req.Method + ":" + req.URL.String()

	// Singleflight deduplication for GET.
	if c.sf != nil && req.Method == http.MethodGet {
		rawResp, err = c.sf.Do(key, func() (*http.Response, error) {
			return c.httpClient.Do(req)
		})
	} else {
		rawResp, err = c.httpClient.Do(req)
	}

	if c.cfg.afterResponse != nil {
		c.cfg.afterResponse(ctx, req, rawResp, err)
	}

	if err != nil {
		return nil, c.wrapError(req, 0, err)
	}

	resp, err := newResponse(rawResp, c.cfg.maxBodyBytes)
	if err != nil {
		return nil, c.wrapError(req, rawResp.StatusCode, err)
	}

	if c.cfg.debugMode && c.cfg.debugOutput != nil {
		c.dumpRequestResponse(req, rawResp)
	}

	return resp, nil
}

// -------------------------------------------------------------------
// Convenience Methods (fluent-style shortcuts)
// -------------------------------------------------------------------

// Get performs a GET request.
func (c *Client) Get(ctx context.Context, path string, opts ...RequestOption) (*Response, error) {
	return c.Execute(ctx, http.MethodGet, path, opts...)
}

// Post performs a POST request.
func (c *Client) Post(ctx context.Context, path string, opts ...RequestOption) (*Response, error) {
	return c.Execute(ctx, http.MethodPost, path, opts...)
}

// Put performs a PUT request.
func (c *Client) Put(ctx context.Context, path string, opts ...RequestOption) (*Response, error) {
	return c.Execute(ctx, http.MethodPut, path, opts...)
}

// Patch performs a PATCH request.
func (c *Client) Patch(ctx context.Context, path string, opts ...RequestOption) (*Response, error) {
	return c.Execute(ctx, http.MethodPatch, path, opts...)
}

// Delete performs a DELETE request.
func (c *Client) Delete(ctx context.Context, path string, opts ...RequestOption) (*Response, error) {
	return c.Execute(ctx, http.MethodDelete, path, opts...)
}

// Head performs a HEAD request.
func (c *Client) Head(ctx context.Context, path string, opts ...RequestOption) (*Response, error) {
	return c.Execute(ctx, http.MethodHead, path, opts...)
}

// Execute builds and sends a request using the fluent RequestBuilder.
func (c *Client) Execute(ctx context.Context, method, path string, opts ...RequestOption) (*Response, error) {
	rb := c.NewRequest(ctx, method, path)
	for _, o := range opts {
		o(rb)
	}
	req, err := rb.Build()
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// -------------------------------------------------------------------
// JSON Helpers
// -------------------------------------------------------------------

// GetJSON performs a GET request and decodes the JSON response into v.
func (c *Client) GetJSON(ctx context.Context, path string, v interface{}, opts ...RequestOption) error {
	opts = append(opts, WithAcceptJSON())
	resp, err := c.Get(ctx, path, opts...)
	if err != nil {
		return err
	}
	return resp.JSON(v)
}

// PostJSON marshals v as JSON, sends it as a POST body, and decodes the JSON response into out.
func (c *Client) PostJSON(ctx context.Context, path string, v, out interface{}, opts ...RequestOption) error {
	opts = append(opts, WithJSONBody(v), WithAcceptJSON())
	resp, err := c.Post(ctx, path, opts...)
	if err != nil {
		return err
	}
	if out != nil {
		return resp.JSON(out)
	}
	return nil
}

// PutJSON marshals v as JSON, sends it as a PUT body, and decodes the JSON response into out.
func (c *Client) PutJSON(ctx context.Context, path string, v, out interface{}, opts ...RequestOption) error {
	opts = append(opts, WithJSONBody(v), WithAcceptJSON())
	resp, err := c.Put(ctx, path, opts...)
	if err != nil {
		return err
	}
	if out != nil {
		return resp.JSON(out)
	}
	return nil
}

// PatchJSON marshals v as JSON, sends it as a PATCH body, and decodes the JSON response into out.
func (c *Client) PatchJSON(ctx context.Context, path string, v, out interface{}, opts ...RequestOption) error {
	opts = append(opts, WithJSONBody(v), WithAcceptJSON())
	resp, err := c.Patch(ctx, path, opts...)
	if err != nil {
		return err
	}
	if out != nil {
		return resp.JSON(out)
	}
	return nil
}

// -------------------------------------------------------------------
// Request Builder factory
// -------------------------------------------------------------------

// NewRequest creates a new RequestBuilder for the given method and path.
func (c *Client) NewRequest(ctx context.Context, method, path string) *RequestBuilder {
	fullURL := c.resolveURL(path)
	rb := &RequestBuilder{
		ctx:     ctx,
		method:  strings.ToUpper(method),
		rawURL:  fullURL,
		headers: make(http.Header),
	}
	// Apply default headers.
	for k, v := range c.cfg.defaultHeaders {
		rb.headers.Set(k, v)
	}
	// Inject correlation ID if not present.
	if rb.headers.Get(c.cfg.correlationKey) == "" {
		rb.headers.Set(c.cfg.correlationKey, internal.GenerateID(8))
	}
	return rb
}

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

func (c *Client) resolveURL(path string) string {
	if c.cfg.baseURL == "" || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	base := strings.TrimRight(c.cfg.baseURL, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func (c *Client) wrapError(req *http.Request, statusCode int, err error) *Error {
	rawURL := ""
	if req != nil && req.URL != nil {
		rawURL = req.URL.String()
	}
	method := ""
	if req != nil {
		method = req.Method
	}
	return &Error{
		Op:         method,
		URL:        rawURL,
		StatusCode: statusCode,
		Kind:       classifyError(err, statusCode),
		Err:        err,
	}
}

func (c *Client) dumpRequestResponse(req *http.Request, resp *http.Response) {
	reqDump, _ := httputil.DumpRequestOut(req, true)
	respDump, _ := httputil.DumpResponse(resp, true)
	_, _ = fmt.Fprintf(c.cfg.debugOutput, "\n--- REQUEST ---\n%s\n--- RESPONSE ---\n%s\n", reqDump, respDump)
}

// -------------------------------------------------------------------
// Transport builder
// -------------------------------------------------------------------

func buildTransport(cfg *clientConfig) (http.RoundTripper, error) {
	if cfg.transport != nil {
		return cfg.transport, nil
	}

	base := &http.Transport{
		MaxIdleConns:        cfg.maxIdleConns,
		MaxConnsPerHost:     cfg.maxConnsPerHost,
		IdleConnTimeout:     cfg.idleConnTimeout,
		DisableCompression:  false, // enable auto-gzip
	}

	if cfg.tlsConfig != nil {
		base.TLSClientConfig = cfg.tlsConfig
	}

	if cfg.proxyURL != nil {
		base.Proxy = http.ProxyURL(cfg.proxyURL)
	}

	if cfg.forceHTTP2 {
		if err := http2.ConfigureTransport(base); err != nil {
			return nil, err
		}
	}

	return base, nil
}

// -------------------------------------------------------------------
// RequestOption shortcuts
// -------------------------------------------------------------------

// WithJSONBody marshals v and sets it as the request body with Content-Type: application/json.
func WithJSONBody(v interface{}) RequestOption {
	return func(rb *RequestBuilder) {
		b, _ := json.Marshal(v)
		rb.body = io.NopCloser(bytes.NewReader(b))
		rb.headers.Set("Content-Type", "application/json")
	}
}

// WithAcceptJSON sets the Accept: application/json header.
func WithAcceptJSON() RequestOption {
	return func(rb *RequestBuilder) {
		rb.headers.Set("Accept", "application/json")
	}
}

// WithRawBody sets raw bytes as the request body.
func WithRawBody(b []byte, contentType string) RequestOption {
	return func(rb *RequestBuilder) {
		rb.body = io.NopCloser(bytes.NewReader(b))
		if contentType != "" {
			rb.headers.Set("Content-Type", contentType)
		}
	}
}

// WithFormBody encodes fields as application/x-www-form-urlencoded.
func WithFormBody(fields url.Values) RequestOption {
	return func(rb *RequestBuilder) {
		rb.BodyForm(fields)
	}
}

// WithMultipartBody builds a multipart/form-data body from text fields and file uploads.
func WithMultipartBody(fields map[string]string, files []FormFile) RequestOption {
	return func(rb *RequestBuilder) {
		rb.BodyMultipart(fields, files)
	}
}

// WithQueryParam adds a URL query parameter.
func WithQueryParam(key, value string) RequestOption {
	return func(rb *RequestBuilder) {
		rb.queryParams.Set(key, value)
	}
}

// WithQueryParams adds multiple URL query parameters.
func WithQueryParams(params url.Values) RequestOption {
	return func(rb *RequestBuilder) {
		for k, vs := range params {
			for _, v := range vs {
				rb.queryParams.Add(k, v)
			}
		}
	}
}

// WithHeader adds a request header.
func WithHeader(key, value string) RequestOption {
	return func(rb *RequestBuilder) {
		rb.headers.Set(key, value)
	}
}
