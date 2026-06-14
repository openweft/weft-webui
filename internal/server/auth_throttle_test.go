// auth_throttle_test.go — pins the per-IP failure budget that
// protects /api/auth/callback from a brute-force spray.

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openweft/weft-webui/internal/audit"
)

// stampHTTPReqCtx mirrors withHTTPRequest's job in the production
// middleware chain : put the *http.Request on the context so
// requestFromContext() can pull RemoteAddr back out.
func stampHTTPReqCtx(r *http.Request) context.Context {
	return context.WithValue(r.Context(), httpRequestKey{}, r)
}

// resetThrottle wipes the package-global counter between tests so
// they don't see each other's state. The throttle isn't reset on
// test teardown by the production wiring (there's nothing to tear
// down — it's a sync.Map'd singleton).
func resetThrottle(t *testing.T) {
	t.Helper()
	authThrottle.mu.Lock()
	authThrottle.entries = nil
	authThrottle.nowFn = nil
	authThrottle.mu.Unlock()
}

func TestAuthThrottle_BelowThresholdPasses(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized) // simulate a failed callback
	})
	mw := withAuthCallbackThrottle(inner)

	// Threshold is 5 ; fire 4 failures, the 5th should still hit the
	// inner handler (and tick the counter past the threshold).
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
		req.RemoteAddr = "1.2.3.4:1024"
		req = req.WithContext(stampHTTPReqCtx(req))
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}
	if calls != 4 {
		t.Errorf("inner calls = %d, want 4", calls)
	}
}

func TestAuthThrottle_BlockedAttemptEmitsAuditEvent(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	// Capture audit emissions so we can assert the throttle wrote
	// one for the blocked request.
	captured := &captureAuditLogger{}
	prev := auditLogger
	auditLogger = captured
	t.Cleanup(func() { auditLogger = prev })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mw := withAuthCallbackThrottle(inner)

	// Burn the budget : 5 fails get through and tick the counter.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
		req.RemoteAddr = "1.2.3.4:1024"
		req = req.WithContext(stampHTTPReqCtx(req))
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}
	prevEventCount := len(captured.events)

	// 6th attempt blocked.
	req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
	req.RemoteAddr = "1.2.3.4:1024"
	req = req.WithContext(stampHTTPReqCtx(req))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if len(captured.events) != prevEventCount+1 {
		t.Fatalf("blocked attempt should emit exactly 1 audit event ; got %d new", len(captured.events)-prevEventCount)
	}
	ev := captured.events[len(captured.events)-1]
	if ev.Action != "auth.callback.throttled" {
		t.Errorf("Action = %q, want auth.callback.throttled", ev.Action)
	}
	if ev.Result != "error" {
		t.Errorf("Result = %q, want error", ev.Result)
	}
	if ev.RemoteIP != "1.2.3.4" {
		t.Errorf("RemoteIP = %q, want 1.2.3.4", ev.RemoteIP)
	}
	if ev.Extra["retry_after_s"] == "" {
		t.Errorf("Extra.retry_after_s should be populated")
	}
}

// captureAuditLogger collects audit events into a slice so tests
// can assert their contents.
type captureAuditLogger struct{ events []audit.Event }

func (c *captureAuditLogger) Log(_ context.Context, ev audit.Event) {
	c.events = append(c.events, ev)
}

func TestAuthThrottle_BlocksAfterThreshold(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	})
	mw := withAuthCallbackThrottle(inner)

	// 5 failures fill the budget ; the 6th must 429 without hitting
	// the inner handler.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
		req.RemoteAddr = "1.2.3.4:1024"
		req = req.WithContext(stampHTTPReqCtx(req))
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}
	if calls != 5 {
		t.Fatalf("after 5 attempts, inner = %d", calls)
	}
	// 6th attempt :
	req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
	req.RemoteAddr = "1.2.3.4:1024"
	req = req.WithContext(stampHTTPReqCtx(req))
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if calls != 5 {
		t.Errorf("6th attempt reached inner (calls=%d, want still 5)", calls)
	}
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Errorf("Retry-After header missing on 429")
	}
}

func TestAuthThrottle_SuccessResetsCounter(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	state := 0 // 0 = fail, 1 = succeed
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state == 0 {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.WriteHeader(http.StatusFound) // success path 302
		}
	})
	mw := withAuthCallbackThrottle(inner)

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
		req.RemoteAddr = "1.2.3.4:1024"
		req = req.WithContext(stampHTTPReqCtx(req))
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}
	// One success → counter reset → 5 more failures must be allowed.
	state = 1
	req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
	req.RemoteAddr = "1.2.3.4:1024"
	req = req.WithContext(stampHTTPReqCtx(req))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	state = 0
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
		req.RemoteAddr = "1.2.3.4:1024"
		req = req.WithContext(stampHTTPReqCtx(req))
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			t.Errorf("attempt %d after success was throttled (want allowed)", i+1)
		}
	}
}

func TestAuthThrottle_PerIPIsolation(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mw := withAuthCallbackThrottle(inner)

	// Burn IP A's budget.
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
		req.RemoteAddr = "1.1.1.1:1234"
		req = req.WithContext(stampHTTPReqCtx(req))
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}

	// IP B should be unaffected.
	req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
	req.RemoteAddr = "2.2.2.2:1234"
	req = req.WithContext(stampHTTPReqCtx(req))
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code == http.StatusTooManyRequests {
		t.Errorf("IP B was throttled despite IP A being the offender")
	}
}

func TestAuthThrottle_WindowExpiry(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	// Inject a controllable clock so we can advance past the window
	// without sleeping.
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	authThrottle.mu.Lock()
	authThrottle.nowFn = func() time.Time { return now }
	authThrottle.mu.Unlock()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mw := withAuthCallbackThrottle(inner)

	// 6 failures inside the window → locked.
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
		req.RemoteAddr = "1.2.3.4:1024"
		req = req.WithContext(stampHTTPReqCtx(req))
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}
	// Advance past the window.
	now = now.Add(6 * time.Minute)

	// 7th attempt should be allowed — the window expired.
	req := httptest.NewRequest("GET", "/api/auth/callback?state=x", nil)
	req.RemoteAddr = "1.2.3.4:1024"
	req = req.WithContext(stampHTTPReqCtx(req))
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code == http.StatusTooManyRequests {
		t.Errorf("attempt after window expiry was throttled")
	}
}

func TestAuthThrottle_NonCallbackRoutePassesThrough(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := withAuthCallbackThrottle(inner)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/api/auth/login", nil)
		req.RemoteAddr = "1.2.3.4:1024"
		req = req.WithContext(stampHTTPReqCtx(req))
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("/api/auth/login unaffected by throttle, got %d", rr.Code)
		}
	}
}

