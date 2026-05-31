package server

import (
	"net/http/httptest"
	"testing"
)

// TestSchedulingRule_CRUD walks the full CRUD round-trip through the
// huma surface : create → list (via /api/resources rows) → patch
// (placement + count) → delete → confirm gone. Targets the in-memory
// store path (no liveNet in dev mode).
func TestSchedulingRule_CRUD(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	// --- create ---
	createBody := map[string]any{
		"name":     "e2e-rule",
		"selector": "app=foo",
		"count":    2,
		"az":       "different",
		"host":     "different",
		"project":  "platform",
	}
	var createResp CreateSchedRuleResp
	if code := hit(t, srv, "POST", "/api/scheduling-rules", createBody, &createResp); code != 201 {
		t.Fatalf("create status = %d", code)
	}
	if createResp.Name != "e2e-rule" || createResp.Project != "platform" {
		t.Errorf("create resp = %+v", createResp)
	}

	// Re-creating the same name must conflict.
	if code := hit(t, srv, "POST", "/api/scheduling-rules", createBody, nil); code != 409 {
		t.Errorf("duplicate create status = %d, want 409", code)
	}

	// --- list (via the resource rows handler) ---
	var listResp struct {
		Rows  []map[string]any `json:"rows"`
		Total int              `json:"total"`
	}
	if code := hit(t, srv, "GET", "/api/resources/scheduling-rules?limit=100", nil, &listResp); code != 200 {
		t.Fatalf("list status = %d", code)
	}
	var seen bool
	for _, r := range listResp.Rows {
		if r["name"] == "e2e-rule" {
			seen = true
			if r["count"] != "0/2" {
				t.Errorf("count column = %v, want '0/2'", r["count"])
			}
		}
	}
	if !seen {
		t.Errorf("e2e-rule missing from listing")
	}

	// --- patch (raise count, clear host axis) ---
	hostEmpty := ""
	patchBody := map[string]any{
		"count": 5,
		"host":  hostEmpty,
	}
	var patchResp CreateSchedRuleResp
	if code := hit(t, srv, "PATCH", "/api/scheduling-rules/e2e-rule", patchBody, &patchResp); code != 200 {
		t.Fatalf("patch status = %d", code)
	}

	// Re-list to confirm the patch landed.
	if code := hit(t, srv, "GET", "/api/resources/scheduling-rules?limit=100", nil, &listResp); code != 200 {
		t.Fatalf("list-2 status = %d", code)
	}
	for _, r := range listResp.Rows {
		if r["name"] == "e2e-rule" {
			if r["count"] != "0/5" {
				t.Errorf("after patch count = %v, want '0/5'", r["count"])
			}
			// host axis was cleared ; placement should not mention host=.
			if placement, _ := r["placement"].(string); placement != "az=different" {
				t.Errorf("after patch placement = %q, want 'az=different'", placement)
			}
		}
	}

	// Patching a missing rule must 404.
	if code := hit(t, srv, "PATCH", "/api/scheduling-rules/not-a-rule", patchBody, nil); code != 404 {
		t.Errorf("patch missing status = %d, want 404", code)
	}

	// Negative count must 422 (huma validation kicks before the handler).
	if code := hit(t, srv, "PATCH", "/api/scheduling-rules/e2e-rule",
		map[string]any{"count": -1}, nil); code != 422 {
		t.Errorf("negative count status = %d, want 422", code)
	}

	// --- delete ---
	if code := hit(t, srv, "DELETE", "/api/scheduling-rules/e2e-rule", nil, nil); code != 204 {
		t.Errorf("delete status = %d", code)
	}
	// Confirm gone.
	if code := hit(t, srv, "DELETE", "/api/scheduling-rules/e2e-rule", nil, nil); code != 404 {
		t.Errorf("re-delete status = %d, want 404", code)
	}
}

// TestSchedulingRule_PatchPreservesProject confirms a partial PATCH
// keeps fields the caller didn't mention. Guards against a regression
// where a typed-struct decode would zero them.
func TestSchedulingRule_PatchPreservesProject(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	_ = hit(t, srv, "POST", "/api/scheduling-rules", map[string]any{
		"name": "patch-keep", "selector": "app=x", "count": 1, "project": "research",
	}, nil)
	t.Cleanup(func() { _ = hit(t, srv, "DELETE", "/api/scheduling-rules/patch-keep", nil, nil) })

	// Touch only selector — project should survive.
	if code := hit(t, srv, "PATCH", "/api/scheduling-rules/patch-keep",
		map[string]any{"selector": "app=y"}, nil); code != 200 {
		t.Fatalf("patch status = %d", code)
	}

	var listResp struct {
		Rows []map[string]any `json:"rows"`
	}
	_ = hit(t, srv, "GET", "/api/resources/scheduling-rules?limit=100", nil, &listResp)
	for _, r := range listResp.Rows {
		if r["name"] == "patch-keep" {
			if r["project"] != "research" {
				t.Errorf("project lost after patch : %v", r["project"])
			}
			if r["selector"] != "app=y" {
				t.Errorf("selector not updated : %v", r["selector"])
			}
		}
	}
}
