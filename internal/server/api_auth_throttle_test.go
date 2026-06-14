// api_auth_throttle_test.go — pin the admin endpoints that
// inspect + clear the per-IP auth-callback throttle.

package server

import (
	"net/http/httptest"
	"testing"
)

func TestAuthThrottle_ListExposesEntries(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	// Seed two IPs : one below threshold, one locked.
	authThrottle.recordFailure("1.1.1.1")
	authThrottle.recordFailure("1.1.1.1")
	for i := 0; i < 5; i++ {
		authThrottle.recordFailure("2.2.2.2")
	}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Entries []map[string]any `json:"entries"`
	}
	if c := hit(t, srv, "GET", "/api/auth/throttle", nil, &body); c != 200 {
		t.Fatalf("status = %d", c)
	}
	if len(body.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(body.Entries))
	}
	// Identify by IP since order isn't guaranteed (map iteration).
	for _, e := range body.Entries {
		switch e["ip"] {
		case "1.1.1.1":
			if e["locked"] != false {
				t.Errorf("1.1.1.1 should not be locked")
			}
		case "2.2.2.2":
			if e["locked"] != true {
				t.Errorf("2.2.2.2 should be locked (5 hits)")
			}
		}
	}
}

func TestAuthThrottle_ClearReleasesLock(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })
	for i := 0; i < 6; i++ {
		authThrottle.recordFailure("1.2.3.4")
	}
	// Sanity : gate should be locked before clear.
	if locked, _ := authThrottle.gate("1.2.3.4"); !locked {
		t.Fatal("IP should be locked before clear")
	}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		IP      string `json:"ip"`
		Cleared bool   `json:"cleared"`
	}
	if c := hit(t, srv, "DELETE", "/api/auth/throttle/1.2.3.4", nil, &body); c != 200 {
		t.Fatalf("status = %d", c)
	}
	if !body.Cleared {
		t.Errorf("Cleared = false, want true")
	}
	// Now gate should pass.
	if locked, _ := authThrottle.gate("1.2.3.4"); locked {
		t.Errorf("IP still locked after clear")
	}
}

func TestAuthThrottle_ClearMissingIPIsNoop(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Cleared bool `json:"cleared"`
	}
	if c := hit(t, srv, "DELETE", "/api/auth/throttle/9.9.9.9", nil, &body); c != 200 {
		t.Fatalf("status = %d", c)
	}
	if body.Cleared {
		t.Errorf("Cleared = true for a never-tracked IP")
	}
}

func TestAuthThrottle_ClearEmitsAuditEvent(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })
	authThrottle.recordFailure("1.2.3.4")

	// buildHandler stamps auditLogger from Deps.Audit (NopLogger when
	// the test fixture leaves it nil), so any earlier override would
	// be wiped by newE2EHandler. Spin the server FIRST, then plug
	// the capture-sink for the request that follows.
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	captured := &captureAuditLogger{}
	prevLogger := auditLogger
	auditLogger = captured
	t.Cleanup(func() { auditLogger = prevLogger })

	if c := hit(t, srv, "DELETE", "/api/auth/throttle/1.2.3.4", nil, nil); c != 200 {
		t.Fatalf("status = %d", c)
	}
	if len(captured.events) == 0 {
		t.Fatal("expected at least one audit event")
	}
	ev := captured.events[len(captured.events)-1]
	if ev.Action != "auth.throttle.clear" {
		t.Errorf("Action = %q, want auth.throttle.clear", ev.Action)
	}
	if ev.ResourceID != "1.2.3.4" {
		t.Errorf("ResourceID = %q, want 1.2.3.4", ev.ResourceID)
	}
	if ev.Result != "ok" {
		t.Errorf("Result = %q, want ok", ev.Result)
	}
	if ev.Extra["existed"] != "yes" {
		t.Errorf("Extra.existed = %q, want yes", ev.Extra["existed"])
	}
}

func TestAuthThrottle_NonAdminScopeGetsNoSuchRoute(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeUser))
	t.Cleanup(srv.Close)

	if c := hit(t, srv, "GET", "/api/auth/throttle", nil, nil); c != 404 {
		t.Errorf("user portal : GET status = %d, want 404 (admin-only)", c)
	}
	if c := hit(t, srv, "DELETE", "/api/auth/throttle/1.2.3.4", nil, nil); c != 404 {
		t.Errorf("user portal : DELETE status = %d, want 404 (admin-only)", c)
	}
}
