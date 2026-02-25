// Package auth provides authentication helpers for httpx.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// -------------------------------------------------------------------
// OAuth 1.0a
// -------------------------------------------------------------------

// OAuth1Config holds the credentials for OAuth 1.0a request signing.
type OAuth1Config struct {
	ConsumerKey    string
	ConsumerSecret string
	Token          string
	TokenSecret    string
}

// OAuth1Transport wraps an http.RoundTripper and signs requests with OAuth 1.0a.
type OAuth1Transport struct {
	Config OAuth1Config
	Base   http.RoundTripper
}

func (t *OAuth1Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

// RoundTrip signs the request with OAuth 1.0a and forwards it.
func (t *OAuth1Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	if err := t.sign(r); err != nil {
		return nil, err
	}
	return t.base().RoundTrip(r)
}

func (t *OAuth1Transport) sign(req *http.Request) error {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, err := generateNonce(16)
	if err != nil {
		return fmt.Errorf("oauth1: generate nonce: %w", err)
	}

	params := map[string]string{
		"oauth_consumer_key":     t.Config.ConsumerKey,
		"oauth_nonce":            nonce,
		"oauth_signature_method": "HMAC-SHA256",
		"oauth_timestamp":        timestamp,
		"oauth_token":            t.Config.Token,
		"oauth_version":          "1.0",
	}

	sig, err := t.buildSignature(req, params)
	if err != nil {
		return err
	}
	params["oauth_signature"] = sig

	var parts []string
	for k, v := range params {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, v))
	}
	req.Header.Set("Authorization", "OAuth "+strings.Join(parts, ", "))
	return nil
}

func (t *OAuth1Transport) buildSignature(req *http.Request, params map[string]string) (string, error) {
	// Build parameter string (sorted).
	paramStr := buildOAuth1ParamString(params)
	baseStr := strings.ToUpper(req.Method) + "&" +
		percentEncode(req.URL.String()) + "&" +
		percentEncode(paramStr)

	signingKey := percentEncode(t.Config.ConsumerSecret) + "&" + percentEncode(t.Config.TokenSecret)
	mac := hmac.New(sha256.New, []byte(signingKey))
	_, _ = mac.Write([]byte(baseStr))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func buildOAuth1ParamString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	// Sort manually to avoid importing sort for a small map.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	return strings.Join(parts, "&")
}

func percentEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if isUnreserved(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~'
}

// -------------------------------------------------------------------
// OAuth 2.0 Bearer Token
// -------------------------------------------------------------------

// TokenSource provides an OAuth2 access token.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticTokenSource returns a fixed token.
type StaticTokenSource struct{ AccessToken string }

func (s *StaticTokenSource) Token(_ context.Context) (string, error) { return s.AccessToken, nil }

// OAuth2Transport wraps an http.RoundTripper and injects a Bearer token.
type OAuth2Transport struct {
	Source TokenSource
	Base   http.RoundTripper
}

func (t *OAuth2Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

// RoundTrip injects the OAuth2 Bearer token and forwards the request.
func (t *OAuth2Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.Source.Token(req.Context())
	if err != nil {
		return nil, fmt.Errorf("oauth2: get token: %w", err)
	}
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	return t.base().RoundTrip(r)
}

// -------------------------------------------------------------------
// HMAC Request Signing
// -------------------------------------------------------------------

// HMACConfig holds the configuration for HMAC request signing.
type HMACConfig struct {
	// KeyID is included in the Signature header so the server can look up the key.
	KeyID  string
	Secret []byte
	// Header is the name of the header that carries the signature.
	// Default: "X-Signature"
	Header string
}

// HMACTransport signs requests with HMAC-SHA256.
// The signature covers: METHOD + \n + URL + \n + timestamp.
type HMACTransport struct {
	Config HMACConfig
	Base   http.RoundTripper
}

func (t *HMACTransport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

// RoundTrip signs the request and forwards it.
func (t *HMACTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	ts := strconv.FormatInt(time.Now().UnixNano(), 10)
	msg := r.Method + "\n" + r.URL.String() + "\n" + ts

	mac := hmac.New(sha256.New, t.Config.Secret)
	_, _ = mac.Write([]byte(msg))
	sig := hex.EncodeToString(mac.Sum(nil))

	headerName := t.Config.Header
	if headerName == "" {
		headerName = "X-Signature"
	}
	r.Header.Set(headerName, fmt.Sprintf("keyId=%s,ts=%s,sig=%s", t.Config.KeyID, ts, sig))
	return t.base().RoundTrip(r)
}

// -------------------------------------------------------------------
// Idempotency Key
// -------------------------------------------------------------------

// IdempotencyTransport injects an idempotency key header into every non-GET request.
type IdempotencyTransport struct {
	// Header is the header name. Default: "Idempotency-Key"
	Header string
	Base   http.RoundTripper
}

func (t *IdempotencyTransport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

// RoundTrip injects an idempotency key and forwards the request.
func (t *IdempotencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	headerName := t.Header
	if headerName == "" {
		headerName = "Idempotency-Key"
	}
	if r.Header.Get(headerName) == "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
		key, err := generateNonce(16)
		if err == nil {
			r.Header.Set(headerName, key)
		}
	}
	return t.base().RoundTrip(r)
}

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

func generateNonce(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
