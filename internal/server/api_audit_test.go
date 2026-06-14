// api_audit_test.go — end-to-end on /api/audit-log : returns
// enabled=false when no tailer is wired, returns events when one is,
// applies action + result filters.

package server

import (
	"net/http/httptest"
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
