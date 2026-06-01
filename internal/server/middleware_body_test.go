// middleware_body_test.go — pins the withMaxBodyBytes middleware's
// behaviour : oversized payloads on /api/* are rejected by the
// MaxBytesReader wrap before any handler runs ; small payloads
// pass through ; non-/api/ paths are exempt.

package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func echoBodyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			// MaxBytesReader returns a *http.MaxBytesError beyond the
			// cap ; the handler typically signals 413.
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestMaxBodyBytes_PassesUnderLimit(t *testing.T) {
	mw := withMaxBodyBytes(1024, echoBodyHandler())
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	body := bytes.NewReader([]byte(strings.Repeat("x", 500)))
	resp, err := http.Post(srv.URL+"/api/foo", "application/octet-stream", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("under-limit POST: status %d, want 200", resp.StatusCode)
	}
}

func TestMaxBodyBytes_RejectsOverLimit(t *testing.T) {
	mw := withMaxBodyBytes(100, echoBodyHandler())
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	body := bytes.NewReader([]byte(strings.Repeat("x", 1000)))
	resp, err := http.Post(srv.URL+"/api/foo", "application/octet-stream", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("over-limit POST: status %d, want 413", resp.StatusCode)
	}
}

func TestMaxBodyBytes_SkipsNonAPIPaths(t *testing.T) {
	// A static asset POST (theoretical) shouldn't be wrapped — the
	// middleware scopes itself to /api/ so SPA tools that upload
	// outside that surface (none today, but future-proof) keep their
	// own limits.
	mw := withMaxBodyBytes(10, echoBodyHandler())
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	body := bytes.NewReader([]byte(strings.Repeat("x", 1000)))
	resp, err := http.Post(srv.URL+"/dashboard/upload", "application/octet-stream", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("non-/api/ POST: status %d, want 200 (path exempt)", resp.StatusCode)
	}
}

func TestMaxBodyBytes_ZeroLimitDisablesWrap(t *testing.T) {
	// limit <= 0 returns next unchanged. Verify by sending a huge
	// body and confirming the inner handler reads all of it.
	mw := withMaxBodyBytes(0, echoBodyHandler())
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	body := bytes.NewReader([]byte(strings.Repeat("x", 1<<16)))
	resp, err := http.Post(srv.URL+"/api/foo", "application/octet-stream", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("zero-limit POST: status %d, want 200 (wrap should be disabled)", resp.StatusCode)
	}
}
