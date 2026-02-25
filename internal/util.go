// Package internal provides shared utilities for the httpx package.
package internal

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
)

// GenerateID generates a random hex-encoded ID of the given byte length.
func GenerateID(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// DrainAndClose reads all remaining bytes from r and closes it.
// This ensures the underlying TCP connection can be reused.
func DrainAndClose(r io.ReadCloser) {
	if r == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()
}

// IsRetryableMethod returns true for HTTP methods that are considered safe to retry.
func IsRetryableMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "OPTIONS", "DELETE", "PUT":
		return true
	}
	return false
}
