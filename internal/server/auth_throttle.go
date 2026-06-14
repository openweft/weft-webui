// auth_throttle.go — per-IP failure-counter for /api/auth/callback.
//
// The global rate-limiter middleware (internal/ratelimit) caps every
// /api/* request at 20 rps × burst 10 per anonymous IP. That's
// already a basic anti-flood, but it doesn't distinguish "a polite
// CLI that occasionally retries" from "an attacker spraying random
// state codes". This middleware adds a stricter, FAILURE-counted
// budget specifically on the OIDC callback :
//
//   - successful callback → counter reset
//   - failed callback (4xx / 5xx) → counter++
//   - counter ≥ threshold within window → reject pre-handler with 429
//
// The threshold (5 fails / 5 min by default) is generous enough for
// legitimate operators (who only hit the callback after a login
// redirect) and tight enough that an attacker burning state cookies
// has to switch IPs constantly.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/openweft/weft-webui/internal/audit"
)

// authThrottle is a tiny per-IP failure ring. The state is package-
// global so tests can probe + reset it without threading a handle
// through buildHandler. Production ergonomics : a 5 min sliding
// window is short enough that a bot needs to actively rotate IPs ;
// a legitimate operator who briefly fat-fingers their OIDC redirect
// is back in business after 5 min without operator intervention.
var authThrottle = &ipFailureBudget{
	window:    5 * time.Minute,
	threshold: 5,
}

type ipFailureBudget struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	entries   map[string]*ipFailures
	// nowFn is overridable from tests so time-window expiry doesn't
	// require sleeping. Production reads time.Now().
	nowFn func() time.Time
}

type ipFailures struct {
	count    int
	firstHit time.Time
}

func (b *ipFailureBudget) now() time.Time {
	if b.nowFn != nil {
		return b.nowFn()
	}
	return time.Now()
}

// gate reports whether the IP is currently rate-locked. Refreshes
// the window opportunistically so a quiescent IP recovers without
// a background sweep.
func (b *ipFailureBudget) gate(ip string) (locked bool, retryAfter time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		return false, 0
	}
	e, ok := b.entries[ip]
	if !ok {
		return false, 0
	}
	if b.now().Sub(e.firstHit) > b.window {
		delete(b.entries, ip)
		return false, 0
	}
	if e.count >= b.threshold {
		return true, b.window - b.now().Sub(e.firstHit)
	}
	return false, 0
}

// recordFailure ticks the counter. First failure stamps firstHit ;
// subsequent ones within window accumulate. After window passes the
// next failure restarts a fresh entry (handled by gate's expiry
// branch).
func (b *ipFailureBudget) recordFailure(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = map[string]*ipFailures{}
	}
	e, ok := b.entries[ip]
	if !ok || b.now().Sub(e.firstHit) > b.window {
		b.entries[ip] = &ipFailures{count: 1, firstHit: b.now()}
		return
	}
	e.count++
}

// recordSuccess clears the IP's history. A successful login is
// strong evidence the operator isn't an attacker.
func (b *ipFailureBudget) recordSuccess(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, ip)
}

// withAuthCallbackThrottle wraps the OIDC callback so we can pre-
// reject locked IPs AND observe the handler's outcome to update
// the counter. Other auth routes pass through untouched.
func withAuthCallbackThrottle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/auth/callback") {
			next.ServeHTTP(w, r)
			return
		}
		ip := remoteIP(requestFromContext(r.Context()))
		if ip == "" {
			// Best-effort fall-back : the audit hook already uses
			// remoteIP() for its events. If even that returns "" we
			// don't throttle (would lock out everyone).
			next.ServeHTTP(w, r)
			return
		}
		if locked, retry := authThrottle.gate(ip); locked {
			w.Header().Set("Retry-After", itoaSeconds(retry))
			http.Error(w, "too many auth failures from this IP", http.StatusTooManyRequests)
			// Tell the audit layer the IP just bounced off the
			// throttle so a SOC analyst grepping for "callback"
			// sees the *blocked* attempts too, not only the
			// underlying OIDC failures (which never run for
			// these requests). Tagged auth.callback.throttled so
			// the dashboard can highlight them distinctly.
			ev := audit.Event{
				Timestamp:    time.Now().UTC(),
				Action:       "auth.callback.throttled",
				ResourceKind: "session",
				Result:       "error",
				ErrorMessage: "rate-limited",
				RemoteIP:     ip,
				Extra:        map[string]string{"retry_after_s": itoaSeconds(retry)},
			}
			auditLogger.Log(r.Context(), ev)
			return
		}

		// Capture the response so we know whether to tick the counter.
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		// Replay onto the real ResponseWriter — copy status, headers,
		// body in that order so the SPA sees the proper response.
		for k, vs := range rec.Result().Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())

		if rec.Code >= 400 {
			authThrottle.recordFailure(ip)
		} else {
			// 2xx + 3xx (the success path redirects to return_to via 302).
			authThrottle.recordSuccess(ip)
		}
	})
}

// itoaSeconds renders d as a whole seconds count for the Retry-After
// HTTP header (RFC 7231 §7.1.3 — delta-seconds is the simplest form).
func itoaSeconds(d time.Duration) string {
	s := int(d.Seconds())
	if s < 1 {
		s = 1
	}
	// Inline strconv to avoid an extra import in this small helper.
	if s == 0 {
		return "0"
	}
	var out [12]byte
	i := len(out)
	for s > 0 {
		i--
		out[i] = byte('0' + s%10)
		s /= 10
	}
	return string(out[i:])
}
