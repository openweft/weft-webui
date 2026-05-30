// middleware.go — cross-cutting HTTP wrappers : security headers,
// no-store for /api/*, request logging, request-ID propagation, and a
// simple panic recovery.
//
// The security-headers set is the conservative baseline for a SPA
// served from the same origin as its API : a single 'self' CSP plus
// the usual three-letter headers. Tighter directives (script-src
// nonce, connect-src for an external IdP) belong in a downstream
// reverse proxy where the deployment knows its real origins.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/openweft/weft-webui/internal/telemetry"
)

// withSecurityHeaders sets a conservative set of defensive HTTP
// headers on every response. The dev-mode flag relaxes the CSP enough
// for Vite's HMR (inline scripts + ws:) so `task dev:web` still works.
func withSecurityHeaders(dev bool, next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"img-src 'self' data:",
		"style-src 'self' 'unsafe-inline'", // Tailwind utility class generation
		"script-src 'self'",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
	if dev {
		csp = "default-src 'self' 'unsafe-inline' 'unsafe-eval' ws: data:; img-src 'self' data: blob:; connect-src *"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// withJSONDefaults sets a no-store policy on API responses so the
// dashboard always reflects current state. (Lives here so all wraps
// stay in one file.)
func withJSONDefaults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDKey is the context key for the per-request ID propagated
// through logs ; clients can also send their own via X-Request-ID.
type requestIDKey struct{}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// withMetrics records request count + duration histograms for every
// handled request. persona ("user" / "admin") lets dashboards split
// the two listeners ; the route label is normalised so that high-card
// path parameters (resource id, bucket name) don't blow the TSDB.
//
// Skips itself when rec is nil so the package stays testable without
// a real registry.
func withMetrics(rec *telemetry.Recorder, persona string, next http.Handler) http.Handler {
	if rec == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.HTTPInflight.Inc()
		defer rec.HTTPInflight.Dec()
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		rec.ObserveHTTP(persona, r.Method, telemetry.RouteLabel(r.Method, r.URL.Path), sr.status, time.Since(start))
	})
}

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		logger.Info("http",
			"method", r.Method, "path", r.URL.Path,
			"status", sr.status, "dur", time.Since(start).Round(time.Microsecond),
			"rid", requestIDFromContext(r.Context()),
		)
	})
}

func withPanicRecovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				logger.Error("panic", "value", v, "path", r.URL.Path, "rid", requestIDFromContext(r.Context()))
				if w.Header().Get("Content-Type") == "" {
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush delegates to the inner ResponseWriter so SSE handlers can
// push each event straight through this wrapper. Without it, the
// flusher type assertion in handleEvents would fail because
// statusRecorder doesn't itself implement http.Flusher.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
