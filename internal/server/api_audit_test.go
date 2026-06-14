// api_audit_test.go — end-to-end on /api/audit-log : returns
// enabled=false when no tailer is wired, returns events when one is,
// applies action + result filters.

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openweft/weft-webui/internal/audit"
)

// stubTailer implements auditTailer for tests so we don't pull in a
// real FileLogger + temp directory.
type stubTailer struct{ events []audit.Event }

func (s *stubTailer) Tail(_ int) ([]audit.Event, error) { return s.events, nil }

func TestAuditLog_DisabledWhenNoTailer(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })
	auditTail = nil

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Enabled bool             `json:"enabled"`
		Events  []map[string]any `json:"events"`
	}
	code := hit(t, srv, "GET", "/api/audit-log?limit=50", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if body.Enabled {
		t.Errorf("Enabled = true, want false (no tailer)")
	}
	if len(body.Events) != 0 {
		t.Errorf("len(Events) = %d, want 0", len(body.Events))
	}
}

func TestAuditLog_ReturnsRecentEvents(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	auditTail = &stubTailer{events: []audit.Event{
		{Timestamp: time.Date(2026, 6, 2, 12, 0, 1, 0, time.UTC), Action: "az.create", Subject: "alice", Result: "ok"},
		{Timestamp: time.Date(2026, 6, 2, 12, 0, 2, 0, time.UTC), Action: "az.delete", Subject: "alice", Result: "ok"},
		{Timestamp: time.Date(2026, 6, 2, 12, 0, 3, 0, time.UTC), Action: "auth.callback.failed", Result: "error",
			ErrorMessage: "state_mismatch"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Enabled bool             `json:"enabled"`
		Events  []map[string]any `json:"events"`
	}
	code := hit(t, srv, "GET", "/api/audit-log?limit=50", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if !body.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if len(body.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(body.Events))
	}
}

func TestAuditLog_FilterByAction(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	auditTail = &stubTailer{events: []audit.Event{
		{Action: "az.create"},
		{Action: "rack.create"},
		{Action: "host.create"},
		{Action: "auth.login.start"},
		{Action: "auth.callback.failed"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	code := hit(t, srv, "GET", "/api/audit-log?action=auth.", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Events) != 2 {
		t.Errorf("want 2 auth.* events, got %d", len(body.Events))
	}
}

func TestAuditLog_FilterByResult(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	auditTail = &stubTailer{events: []audit.Event{
		{Action: "az.create", Result: "ok"},
		{Action: "az.delete", Result: "ok"},
		{Action: "auth.callback.failed", Result: "error"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	code := hit(t, srv, "GET", "/api/audit-log?result=error", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Events) != 1 {
		t.Errorf("want 1 error event, got %d", len(body.Events))
	}
}

func TestAuditLog_FilterBySinceUntil(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	d := func(h int) time.Time {
		return time.Date(2026, 6, 2, h, 0, 0, 0, time.UTC)
	}
	auditTail = &stubTailer{events: []audit.Event{
		{Action: "vm.create", Timestamp: d(8), Subject: "alice"},
		{Action: "vm.delete", Timestamp: d(10), Subject: "alice"},
		{Action: "az.create", Timestamp: d(13), Subject: "bob"},
		{Action: "az.delete", Timestamp: d(15), Subject: "bob"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	cases := []struct {
		name     string
		query    string
		wantHits int
	}{
		// 09:00..14:00 = the 10:00 + 13:00 events.
		{"since+until window", "since=2026-06-02T09:00:00Z&until=2026-06-02T14:00:00Z", 2},
		// since only : drop events before 12:00 → keep 13:00 + 15:00.
		{"since only", "since=2026-06-02T12:00:00Z", 2},
		// until only : drop 13:00 + 15:00 → keep 08:00 + 10:00.
		{"until only", "until=2026-06-02T12:00:00Z", 2},
		// boundary : until is EXCLUSIVE — until=13:00 should NOT
		// include the 13:00 event.
		{"until exclusive", "until=2026-06-02T13:00:00Z", 2},
		// boundary : since is INCLUSIVE — since=13:00 keeps the
		// 13:00 event.
		{"since inclusive", "since=2026-06-02T13:00:00Z", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body struct {
				Events []map[string]any `json:"events"`
			}
			code := hit(t, srv, "GET", "/api/audit-log?"+tc.query, nil, &body)
			if code != 200 {
				t.Fatalf("status = %d", code)
			}
			if len(body.Events) != tc.wantHits {
				t.Errorf("got %d events, want %d : %+v", len(body.Events), tc.wantHits, body.Events)
			}
		})
	}
}

func TestAuditLog_RejectsBadTimestamps(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })
	auditTail = &stubTailer{events: nil}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	// Malformed since → 400, not a silent "no filter".
	if code := hit(t, srv, "GET", "/api/audit-log?since=yesterday", nil, nil); code != 400 {
		t.Errorf("bad since: status %d, want 400", code)
	}
	// until before since → 400.
	if code := hit(t, srv, "GET",
		"/api/audit-log?since=2026-06-02T12:00:00Z&until=2026-06-02T10:00:00Z",
		nil, nil); code != 400 {
		t.Errorf("inverted window: status %d, want 400", code)
	}
}

func TestAuditLog_FilterBySubject(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	auditTail = &stubTailer{events: []audit.Event{
		{Action: "vm.create", Subject: "alice@acme.example"},
		{Action: "vm.delete", Subject: "bob@globex.example"},
		{Action: "az.update", Subject: "alice@acme.example"},
		{Action: "az.create", Subject: "carol@platform.example"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	// Substring match : "alice" hits two events.
	code := hit(t, srv, "GET", "/api/audit-log?subject=alice", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Events) != 2 {
		t.Errorf("want 2 alice events, got %d : %+v", len(body.Events), body.Events)
	}
	for _, ev := range body.Events {
		if !strings.Contains(str(ev["subject"]), "alice") {
			t.Errorf("event without alice in subject : %+v", ev)
		}
	}
}

func TestAuditLog_SubjectFilterIsCaseInsensitive(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	auditTail = &stubTailer{events: []audit.Event{
		{Action: "vm.create", Subject: "Alice@ACME.example"},
		{Action: "vm.create", Subject: "bob@globex.example"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	// Search "ALICE" — capital — must still match the mixed-case
	// Subject. Operators dealing with OIDC subs that have varied
	// case shouldn't have to retype with the exact casing.
	code := hit(t, srv, "GET", "/api/audit-log?subject=ALICE", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Events) != 1 {
		t.Errorf("case-insensitive ALICE should match Alice@ACME — got %d events", len(body.Events))
	}
}

func TestAuditLog_TenantPortalFiltersByOwnTenant(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	// Three tenants' events in the underlying log ; the tenant
	// portal called with Tenant=acme MUST only see acme's events.
	auditTail = &stubTailer{events: []audit.Event{
		{Action: "vm.create", Tenant: "acme", Subject: "alice@acme"},
		{Action: "vm.create", Tenant: "globex", Subject: "bob@globex"},
		{Action: "vm.create", Tenant: "acme", Subject: "alice@acme"},
		{Action: "az.create", Tenant: "", Subject: "platform-admin"},
	}}

	srv := httptest.NewServer(newE2EHandlerForTenant(t, "acme"))
	t.Cleanup(srv.Close)

	// Dev-mode middleware reads the tenant from /api/session/scope
	// rather than the MockUser fields (intentional — operators
	// switch scope live without re-logging-in). Post the scope
	// before the audit query.
	if c := hit(t, srv, "POST", "/api/session/scope",
		map[string]any{"tenant": "acme", "project": ""}, nil); c != 200 && c != 204 {
		t.Fatalf("set scope : status %d", c)
	}

	var body struct {
		Events []map[string]any `json:"events"`
	}
	code := hit(t, srv, "GET", "/api/audit-log?limit=50", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Events) != 2 {
		t.Fatalf("want 2 acme events, got %d (cross-tenant leak ?) ; events=%+v", len(body.Events), body.Events)
	}
	for _, ev := range body.Events {
		if ev["tenant"] != "acme" {
			t.Errorf("tenant %q leaked into acme portal", ev["tenant"])
		}
	}
}

func TestAuditLog_InfraPortalSeesAllTenants(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })

	// Same fixture as the previous test ; infra portal sees every
	// event regardless of Tenant.
	auditTail = &stubTailer{events: []audit.Event{
		{Action: "vm.create", Tenant: "acme"},
		{Action: "vm.create", Tenant: "globex"},
		{Action: "vm.create", Tenant: "acme"},
		{Action: "az.create", Tenant: ""},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	code := hit(t, srv, "GET", "/api/audit-log?limit=50", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Events) != 4 {
		t.Errorf("infra portal should see all 4 events, got %d", len(body.Events))
	}
}

func TestAuditLog_UserListenerHidesEndpoint(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeUser))
	t.Cleanup(srv.Close)

	code := hit(t, srv, "GET", "/api/audit-log", nil, nil)
	// Scope-gated registration → user listener returns 404 (not 403)
	// so a stale SPA never sees the route exists.
	if code != 404 {
		t.Errorf("status = %d, want 404 (admin-only)", code)
	}
}
