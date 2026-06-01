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
	"net/url"
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

// withMaxBodyBytes wraps every /api/* request body in an
// http.MaxBytesReader so an oversized payload can't pin memory or
// hold a connection forever. Hits over the limit get a 413 + an
// empty body — handlers downstream just see io.EOF / "http: request
// body too large" when they try to read.
//
// Default 1 MiB is generous for the typed huma surface (the largest
// real bodies are SSH-key import lists at ~80 KiB) ; raise per
// deployment if a future endpoint needs more (large script bodies,
// SBOM uploads, etc.).
//
// limit <= 0 disables the wrap entirely (back-compat / unit tests).
func withMaxBodyBytes(limit int64, next http.Handler) http.Handler {
	if limit <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && strings.HasPrefix(r.URL.Path, "/api/") {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// withOriginCheck rejects state-changing /api/* requests whose Origin
// (preferred) or Referer doesn't match the server's Host or one of
// the optional allow-list entries. The same-origin invariant under
// SameSite=Lax cookies already mitigates the classic form-submit
// CSRF, but a logged-in operator visiting a hostile page can still
// be tricked into JSON POSTs via fetch() if the malicious origin
// can guess the API shape. This middleware closes that hole at the
// outermost layer.
//
// Method skipping :
//   - GET, HEAD, OPTIONS — safe / preflight ; never mutate
//   - everything else (POST, PUT, PATCH, DELETE) — Origin/Referer must
//     match. Browsers always set at least one of these on
//     cross-origin requests, so a missing header from a non-CLI
//     client is itself suspicious — we reject.
//
// CLI tools (curl, terraform-provider-weft, internal automation) MUST
// either set the Origin header explicitly or be added to the
// allowed-origins list. Same-origin browser SPAs need no extra config.
//
// The /api/auth/* routes are exempt — they're hit pre-session, the
// OIDC layer has its own state-cookie check, and adding origin
// enforcement to the callback redirect would break OIDC IdPs that
// don't forward the originating Origin header.
func withOriginCheck(allowed []string, next http.Handler) http.Handler {
	// Precompute a set lookup for the allow-list. Empty list = same-
	// origin only (still enforced via Host).
	allow := map[string]struct{}{}
	for _, o := range allowed {
		o = strings.TrimSpace(o)
		o = strings.TrimSuffix(o, "/")
		if o != "" {
			allow[o] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// Auth routes carry the OIDC state cookie, which the IdP echoes
		// back ; CSRF-token validation lives in the OIDC handler itself.
		if strings.HasPrefix(r.URL.Path, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		// Non-API paths (the SPA static bundle) are read-only, but a
		// future SPA-side mutation route would also benefit. We scope
		// the check to /api/ for now — that's where every mutation
		// lives.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		expected := schemeFromRequest(r) + "://" + r.Host

		// Normalise scheme://host[:port] from any raw header value.
		// url.Parse handles both "Origin: https://x.com" (no path) and
		// "Referer: https://x.com/foo/bar" (path that must be stripped).
		// Returns "" when parsing fails — rejection.
		normalise := func(raw string) string {
			u, err := url.Parse(raw)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return ""
			}
			out := u.Scheme + "://" + u.Host
			// Strip default ports browsers omit from Origin headers.
			out = strings.TrimSuffix(out, ":80")
			out = strings.TrimSuffix(out, ":443")
			return out
		}

		check := func(raw string) bool {
			n := normalise(raw)
			if n == "" {
				return false
			}
			if n == expected {
				return true
			}
			_, ok := allow[n]
			return ok
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			if !check(origin) {
				http.Error(w, "cross-origin request denied", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if ref := r.Header.Get("Referer"); ref != "" {
			if !check(ref) {
				http.Error(w, "cross-origin request denied", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// Neither Origin nor Referer was sent — browsers always set
		// one of these on a cross-origin POST, so a missing pair is
		// either a same-origin fetch with referrerpolicy=no-referrer
		// (allowed) or a server-side CLI tool that didn't opt in.
		// Reject : CLI tools must declare themselves.
		http.Error(w, "origin/referer header required on mutating requests", http.StatusForbidden)
	})
}

// schemeFromRequest returns "https" when the connection used TLS or
// the configured upstream proxy advertised it, "http" otherwise.
// Mirrors the X-Forwarded-Proto handling in the OIDC redirect-URL
// builder so the two layers agree on the scheme they reconstruct.
func schemeFromRequest(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" {
		return "https"
	}
	return "http"
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
