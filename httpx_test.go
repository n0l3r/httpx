package httpx_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/n0l3r/httpx"
	"github.com/n0l3r/httpx/mock"
)

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

func newTestServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func clientWithTransport(t *testing.T, transport http.RoundTripper) *httpx.Client {
	t.Helper()
	c, err := httpx.New(httpx.WithTransport(transport))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	return c
}

// -------------------------------------------------------------------
// Client basic GET
// -------------------------------------------------------------------

func TestClientGet(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	})
	defer srv.Close()

	c, _ := httpx.New(httpx.WithBaseURL(srv.URL))
	resp, err := c.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !resp.IsSuccess() {
		t.Fatalf("expected success, got %d", resp.StatusCode())
	}
	if !strings.Contains(resp.String(), "hello") {
		t.Fatalf("unexpected body: %s", resp.String())
	}
}

func TestClientGetJSON(t *testing.T) {
	type payload struct{ Hello string }
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload{Hello: "world"})
	})
	defer srv.Close()

	c, _ := httpx.New(httpx.WithBaseURL(srv.URL))
	var out payload
	if err := c.GetJSON(context.Background(), "/", &out); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if out.Hello != "world" {
		t.Fatalf("expected world, got %q", out.Hello)
	}
}

func TestClientPostJSON(t *testing.T) {
	type body struct{ Name string }
	var received body

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	defer srv.Close()

	c, _ := httpx.New(httpx.WithBaseURL(srv.URL))
	var out map[string]string
	if err := c.PostJSON(context.Background(), "/", body{Name: "test"}, &out); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if received.Name != "test" {
		t.Fatalf("expected test, got %q", received.Name)
	}
}

// -------------------------------------------------------------------
// Request builder
// -------------------------------------------------------------------

func TestRequestBuilder(t *testing.T) {
	var capturedHeader string
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("X-Custom")
		q := r.URL.Query().Get("foo")
		if q != "bar" {
			t.Errorf("expected query foo=bar, got %q", q)
		}
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c, _ := httpx.New()
	req, err := c.NewRequest(context.Background(), "GET", srv.URL+"/").
		Header("X-Custom", "myvalue").
		Query("foo", "bar").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsSuccess() {
		t.Fatalf("expected success, got %d", resp.StatusCode())
	}
	if capturedHeader != "myvalue" {
		t.Fatalf("header not injected: %q", capturedHeader)
	}
}

// -------------------------------------------------------------------
// Response helpers
// -------------------------------------------------------------------

func TestResponseEnsureSuccess(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	c, _ := httpx.New(httpx.WithBaseURL(srv.URL))
	resp, err := c.Get(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.EnsureSuccess(); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

// -------------------------------------------------------------------
// Mock transport
// -------------------------------------------------------------------

func TestMockTransport(t *testing.T) {
	mt := mock.NewMockTransport().
		OnGet("/api/users", func(req *http.Request) (*mock.Response, error) {
			return mock.NewJSONResponse(200, []string{"alice", "bob"}), nil
		})

	c := clientWithTransport(t, mt)
	resp, err := c.Get(context.Background(), "http://example.com/api/users")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsSuccess() {
		t.Fatalf("expected 200, got %d", resp.StatusCode())
	}
	if mt.CallCount() != 1 {
		t.Fatalf("expected 1 call, got %d", mt.CallCount())
	}
}

// -------------------------------------------------------------------
// Retry
// -------------------------------------------------------------------

func TestRetryOn5xx(t *testing.T) {
	attempts := 0
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	policy := &httpx.RetryPolicy{
		MaxAttempts: 3,
		Backoff:     httpx.ConstantBackoff(0),
		Conditions:  []httpx.RetryConditionFunc{httpx.RetryOnStatus5xx},
	}
	c, _ := httpx.New(httpx.WithBaseURL(srv.URL), httpx.WithRetryPolicy(policy))
	resp, err := c.Get(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsSuccess() {
		t.Fatalf("expected success after retries, got %d", resp.StatusCode())
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// -------------------------------------------------------------------
// Backoff
// -------------------------------------------------------------------

func TestExponentialBackoff(t *testing.T) {
	bo := httpx.ExponentialBackoff(100*time.Millisecond, 5*time.Second, 0)
	d0 := bo(0)
	d1 := bo(1)
	d2 := bo(2)
	if d0 >= d1 || d1 >= d2 {
		t.Fatalf("expected increasing delays: %v %v %v", d0, d1, d2)
	}
}

func TestFullJitterBackoff(t *testing.T) {
	bo := httpx.FullJitterBackoff(100*time.Millisecond, 5*time.Second)
	for i := range 10 {
		d := bo(i)
		if d < 0 || d > 5*time.Second {
			t.Fatalf("unexpected delay at attempt %d: %v", i, d)
		}
	}
}

// -------------------------------------------------------------------
// Circuit breaker
// -------------------------------------------------------------------

func TestCircuitBreaker(t *testing.T) {
	cb := httpx.NewCircuitBreaker(httpx.CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenTimeout:      50 * time.Millisecond,
	})

	// Two failures should open the circuit.
	cb.RecordFailure("example.com")
	cb.RecordFailure("example.com")

	if err := cb.Allow("example.com"); err == nil {
		t.Fatal("expected circuit to be open")
	}

	// Wait for open timeout.
	time.Sleep(60 * time.Millisecond)

	// Should be half-open now.
	if err := cb.Allow("example.com"); err != nil {
		t.Fatalf("expected half-open, got: %v", err)
	}
	cb.RecordSuccess("example.com")

	// Should be closed again.
	if err := cb.Allow("example.com"); err != nil {
		t.Fatalf("expected closed, got: %v", err)
	}
}

// -------------------------------------------------------------------
// Cache
// -------------------------------------------------------------------

func TestMemoryCache(t *testing.T) {
	calls := 0
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("cached"))
	})
	defer srv.Close()

	cache := httpx.NewMemoryCache(5 * time.Second)
	c, _ := httpx.New(httpx.WithBaseURL(srv.URL), httpx.WithCache(cache))

	// First request — should hit the server.
	_, _ = c.Get(context.Background(), "/data")
	// Second request — should be served from cache.
	_, _ = c.Get(context.Background(), "/data")

	if calls != 1 {
		t.Fatalf("expected 1 server call (cache hit on 2nd), got %d", calls)
	}
}

// -------------------------------------------------------------------
// Logging hook
// -------------------------------------------------------------------

func TestLogHook(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	var logged []httpx.LogEvent
	hook := httpx.LogHookFunc(func(e httpx.LogEvent) { logged = append(logged, e) })

	c, _ := httpx.New(httpx.WithBaseURL(srv.URL), httpx.WithLogHook(hook))
	_, _ = c.Get(context.Background(), "/")

	if len(logged) != 1 {
		t.Fatalf("expected 1 log event, got %d", len(logged))
	}
	if logged[0].Method != "GET" {
		t.Fatalf("unexpected method: %s", logged[0].Method)
	}
}

// -------------------------------------------------------------------
// Metrics hook
// -------------------------------------------------------------------

func TestMetricsHook(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	var events []httpx.MetricsEvent
	hook := httpx.MetricsHookFunc(func(e httpx.MetricsEvent) { events = append(events, e) })

	c, _ := httpx.New(httpx.WithBaseURL(srv.URL), httpx.WithMetricsHook(hook))
	_, _ = c.Get(context.Background(), "/")

	if len(events) != 1 {
		t.Fatalf("expected 1 metrics event, got %d", len(events))
	}
	if events[0].StatusCode != 200 {
		t.Fatalf("unexpected status code: %d", events[0].StatusCode)
	}
}

// -------------------------------------------------------------------
// Default headers & correlation ID
// -------------------------------------------------------------------

func TestDefaultHeaders(t *testing.T) {
	var got string
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-App-Name")
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c, _ := httpx.New(
		httpx.WithBaseURL(srv.URL),
		httpx.WithDefaultHeader("X-App-Name", "my-service"),
	)
	_, _ = c.Get(context.Background(), "/")
	if got != "my-service" {
		t.Fatalf("expected my-service, got %q", got)
	}
}

// -------------------------------------------------------------------
// Error classification
// -------------------------------------------------------------------

func TestIsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := httpx.New(httpx.WithTimeout(50 * time.Millisecond))
	_, err := c.Get(context.Background(), srv.URL+"/")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !httpx.IsTimeout(err) {
		t.Fatalf("expected timeout classification, got: %v", err)
	}
}

func TestIsStatus4xx(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	defer srv.Close()

	// GetJSON causes an error if status is not 2xx is NOT automatic — we need EnsureSuccess.
	// Test that the Error type has correct Kind when wrapping.
	e := &httpx.Error{StatusCode: 400, Kind: "4xx"}
	if !httpx.IsStatus4xx(e) {
		t.Fatal("IsStatus4xx should return true")
	}
}

// -------------------------------------------------------------------
// Middleware
// -------------------------------------------------------------------

func TestHeaderInjectorMiddleware(t *testing.T) {
	var got string
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Injected")
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c, _ := httpx.New(
		httpx.WithBaseURL(srv.URL),
		httpx.WithMiddleware(httpx.HeaderInjector(map[string]string{"X-Injected": "yes"})),
	)
	_, _ = c.Get(context.Background(), "/")
	if got != "yes" {
		t.Fatalf("expected X-Injected: yes, got %q", got)
	}
}

// -------------------------------------------------------------------
// Context cancellation
// -------------------------------------------------------------------

func TestContextCancellation(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	c, _ := httpx.New()
	_, err := c.Get(ctx, srv.URL+"/")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestClientBodyForm(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if got := r.FormValue("username"); got != "alice" {
			http.Error(w, "unexpected username: "+got, 400)
			return
		}
		ct := r.Header.Get("Content-Type")
		if ct != "application/x-www-form-urlencoded" {
			http.Error(w, "unexpected content-type: "+ct, 400)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c, _ := httpx.New()
	resp, err := c.Execute(context.Background(), "POST", srv.URL+"/login",
		httpx.WithFormBody(url.Values{"username": {"alice"}, "password": {"secret"}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsSuccess() {
		t.Fatalf("expected 200, got %d", resp.StatusCode())
	}
}

func TestClientBodyMultipart(t *testing.T) {
	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if got := r.FormValue("title"); got != "hello" {
			http.Error(w, "unexpected title: "+got, 400)
			return
		}
		f, fh, err := r.FormFile("upload")
		if err != nil {
			http.Error(w, "missing file: "+err.Error(), 400)
			return
		}
		defer f.Close()
		if fh.Filename != "test.txt" {
			http.Error(w, "unexpected filename: "+fh.Filename, 400)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c, _ := httpx.New()
	resp, err := c.Execute(context.Background(), "POST", srv.URL+"/upload",
		httpx.WithMultipartBody(
			map[string]string{"title": "hello"},
			[]httpx.FormFile{
				{FieldName: "upload", FileName: "test.txt", Content: strings.NewReader("file content"), ContentType: "text/plain"},
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsSuccess() {
		t.Fatalf("expected 200, got %d", resp.StatusCode())
	}
}
