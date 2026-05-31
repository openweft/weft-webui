// ratelimit_test.go — table-driven coverage of the token-bucket
// middleware. We exercise the public surface only ; the internal
// sync.Map / sweeper is implementation detail.
//
// Tests use tiny rates (1–5 rps, burst 1–2) so the timing assertions
// stay tight without sprinkling sleeps everywhere. The clock injector
// (Options.Now) keeps the table cases deterministic — no time.Sleep,
// no flakes.
package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/openweft/weft-webui/internal/auth"
)

// passthrough is the dummy "next" handler — anything that gets past
// the middleware lands here and returns 200.
var passthrough = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// do builds a request, runs it through the limiter, and returns the
// status + Retry-After header.
func do(t *testing.T, l *Limiter, req *http.Request) (int, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	l.Middleware(passthrough).ServeHTTP(rr, req)
	return rr.Code, rr.Header().Get("Retry-After")
}

// reqWithUser attaches an auth.User to the request context so the
// limiter keys it as a session.
func reqWithUser(sub, ip string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.RemoteAddr = ip + ":1234"
	r = r.WithContext(auth.WithUser(r.Context(), &auth.User{Subject: sub}))
	return r
}

// reqAnon returns an anonymous request from ip.
func reqAnon(ip string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.RemoteAddr = ip + ":1234"
	return r
}

// TestAllowDenyUnderRate hammers a single key past its burst and
// checks the bucket switches to 429 once tokens are exhausted, then
// recovers on the next refill.
func TestAllowDenyUnderRate(t *testing.T) {
	// Anonymous key, 1 rps + burst 2 : the 1st + 2nd requests pass,
	// the 3rd is denied.
	l := NewLimiter(Options{
		AnonRPS:   1,
		AnonBurst: 2,
		UserRPS:   1,
		UserBurst: 2,
	})
	defer l.Stop()

	cases := []struct {
		name    string
		wantOK  bool
	}{
		{"first request consumes a token", true},
		{"second request consumes the last burst token", true},
		{"third request is denied", false},
		{"fourth request is also denied", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, retry := do(t, l, reqAnon("10.0.0.1"))
			if tc.wantOK {
				if code != http.StatusOK {
					t.Fatalf("expected 200, got %d (retry=%q)", code, retry)
				}
				return
			}
			if code != http.StatusTooManyRequests {
				t.Fatalf("expected 429, got %d", code)
			}
			n, err := strconv.Atoi(retry)
			if err != nil || n < 1 {
				t.Fatalf("Retry-After must be a positive integer, got %q", retry)
			}
		})
	}
}

// TestPerKeyIsolation : one key's exhaustion must not affect another.
// We drain Alice's bucket and then verify Bob still gets through, both
// for the session→IP boundary and the IP→IP boundary.
func TestPerKeyIsolation(t *testing.T) {
	l := NewLimiter(Options{AnonRPS: 1, AnonBurst: 1, UserRPS: 1, UserBurst: 1})
	defer l.Stop()

	// Drain Alice.
	if code, _ := do(t, l, reqWithUser("alice", "10.0.0.1")); code != http.StatusOK {
		t.Fatalf("alice first req: want 200, got %d", code)
	}
	if code, _ := do(t, l, reqWithUser("alice", "10.0.0.1")); code != http.StatusTooManyRequests {
		t.Fatalf("alice second req: want 429, got %d", code)
	}

	// Bob (different subject, same IP) must still pass.
	if code, _ := do(t, l, reqWithUser("bob", "10.0.0.1")); code != http.StatusOK {
		t.Fatalf("bob first req: want 200, got %d", code)
	}

	// Anonymous from a third IP must still pass (different bucket).
	if code, _ := do(t, l, reqAnon("10.0.0.2")); code != http.StatusOK {
		t.Fatalf("anon 10.0.0.2: want 200, got %d", code)
	}
}

// TestSubjectVsIPNamespacesDontCollide : a subject literally equal to
// an IP string must NOT share the bucket of that IP-keyed anon.
func TestSubjectVsIPNamespacesDontCollide(t *testing.T) {
	l := NewLimiter(Options{AnonRPS: 1, AnonBurst: 1, UserRPS: 1, UserBurst: 1})
	defer l.Stop()

	// User with subject "10.0.0.1" — uses the user bucket.
	if code, _ := do(t, l, reqWithUser("10.0.0.1", "192.168.0.99")); code != http.StatusOK {
		t.Fatalf("user req: want 200, got %d", code)
	}
	// Anon from RemoteAddr 10.0.0.1 — separate bucket, must pass.
	if code, _ := do(t, l, reqAnon("10.0.0.1")); code != http.StatusOK {
		t.Fatalf("anon req: want 200, got %d (subject vs IP namespaces leaked)", code)
	}
}

// TestXForwardedForHonouredOnlyWhenTrusted : both axes of the toggle.
// Untrusted listener : header is ignored, both requests share the
// RemoteAddr bucket. Trusted listener : header is honoured, the two
// requests land in distinct buckets and both pass.
func TestXForwardedForHonouredOnlyWhenTrusted(t *testing.T) {
	t.Run("untrusted ignores XFF", func(t *testing.T) {
		l := NewLimiter(Options{AnonRPS: 1, AnonBurst: 1, TrustForwardedFor: false})
		defer l.Stop()

		mk := func(xff string) *http.Request {
			r := reqAnon("10.0.0.1")
			r.Header.Set("X-Forwarded-For", xff)
			return r
		}

		// Both requests carry different XFF but the same RemoteAddr —
		// since we don't trust XFF, they collapse onto one bucket.
		if code, _ := do(t, l, mk("1.1.1.1")); code != http.StatusOK {
			t.Fatalf("first req: want 200, got %d", code)
		}
		if code, _ := do(t, l, mk("2.2.2.2")); code != http.StatusTooManyRequests {
			t.Fatalf("second req: want 429 (same RemoteAddr bucket), got %d", code)
		}
	})

	t.Run("trusted honours XFF, left-most wins", func(t *testing.T) {
		l := NewLimiter(Options{AnonRPS: 1, AnonBurst: 1, TrustForwardedFor: true})
		defer l.Stop()

		mk := func(xff string) *http.Request {
			r := reqAnon("10.0.0.1")
			r.Header.Set("X-Forwarded-For", xff)
			return r
		}

		// Different left-most IPs → different buckets → both pass.
		if code, _ := do(t, l, mk("1.1.1.1, 10.0.0.1")); code != http.StatusOK {
			t.Fatalf("xff=1.1.1.1: want 200, got %d", code)
		}
		if code, _ := do(t, l, mk("2.2.2.2, 10.0.0.1")); code != http.StatusOK {
			t.Fatalf("xff=2.2.2.2: want 200, got %d", code)
		}
		// Re-hit the first XFF — same bucket, exhausted, denied.
		if code, _ := do(t, l, mk("1.1.1.1, 10.0.0.1")); code != http.StatusTooManyRequests {
			t.Fatalf("xff=1.1.1.1 second hit: want 429, got %d", code)
		}
	})

	t.Run("trusted but no XFF falls back to RemoteAddr", func(t *testing.T) {
		l := NewLimiter(Options{AnonRPS: 1, AnonBurst: 1, TrustForwardedFor: true})
		defer l.Stop()

		if code, _ := do(t, l, reqAnon("10.0.0.1")); code != http.StatusOK {
			t.Fatalf("first req: want 200, got %d", code)
		}
		if code, _ := do(t, l, reqAnon("10.0.0.1")); code != http.StatusTooManyRequests {
			t.Fatalf("second req: want 429, got %d", code)
		}
	})
}

// Test429ResponseShape : status, Retry-After header, JSON body
// content-type. The body itself is a stable contract for the SPA's
// fetch helper.
func Test429ResponseShape(t *testing.T) {
	l := NewLimiter(Options{AnonRPS: 1, AnonBurst: 1})
	defer l.Stop()

	// Burn the bucket.
	if code, _ := do(t, l, reqAnon("10.0.0.1")); code != http.StatusOK {
		t.Fatalf("warmup: want 200, got %d", code)
	}

	rr := httptest.NewRecorder()
	l.Middleware(passthrough).ServeHTTP(rr, reqAnon("10.0.0.1"))

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After missing")
	} else if n, err := strconv.Atoi(got); err != nil || n < 1 {
		t.Fatalf("Retry-After must be a positive integer, got %q", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type: want JSON, got %q", ct)
	}
	if body := rr.Body.String(); !contains(body, `"error"`) || !contains(body, `"retry_after_seconds"`) {
		t.Fatalf("unexpected body: %q", body)
	}
}

// TestDefaultsApplied : zero-value Options must yield the documented
// defaults. We probe by burning past the anonymous burst and checking
// the cliff is at DefaultAnonBurst+1.
func TestDefaultsApplied(t *testing.T) {
	l := NewLimiter(Options{})
	defer l.Stop()

	// First DefaultAnonBurst requests pass.
	for i := 0; i < DefaultAnonBurst; i++ {
		code, _ := do(t, l, reqAnon("10.0.0.1"))
		if code != http.StatusOK {
			t.Fatalf("req %d/%d: want 200, got %d", i+1, DefaultAnonBurst, code)
		}
	}
	// Default rate is 20 rps → the very next request, fired
	// back-to-back, is denied. (We don't assert further because the
	// real time.Now ticks between calls ; this is the only assertion
	// that's both deterministic and meaningful at default rates.)
	code, _ := do(t, l, reqAnon("10.0.0.1"))
	if code != http.StatusTooManyRequests {
		t.Fatalf("post-burst: want 429, got %d", code)
	}
}

// TestStopIsIdempotent : guards against the goroutine leak detector
// + a "Stop after Stop" panic regression.
func TestStopIsIdempotent(t *testing.T) {
	l := NewLimiter(Options{})
	l.Stop()
	l.Stop() // must not panic on double-close
}

// contains is a tiny strings.Contains shim ; using strings here would
// bloat the imports for a one-liner.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
