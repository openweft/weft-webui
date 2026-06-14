// audit_metrics_test.go — pin the weft_webui_audit_events_total
// counter. Every Audit() call ticks the (action_prefix, result)
// label set ; the prefix is everything before the first dot so the
// label cardinality stays bounded (auth, az, rack, host, vm, …).

package server

import (
	"context"
	"testing"

	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestAuditMetric_IncrementsOnEmit(t *testing.T) {
	rec := telemetry.New("test")
	prev := metrics
	t.Cleanup(func() { metrics = prev })
	metrics = rec

	Audit(context.Background(), audit.NopLogger{}, "az.create", "az", "u1", "", nil, nil)
	Audit(context.Background(), audit.NopLogger{}, "az.delete", "az", "u1", "", nil, nil)
	Audit(context.Background(), audit.NopLogger{}, "auth.callback.failed", "session", "", "error", nil, nil)

	// az/ok should have 2 hits, auth/error should have 1.
	if got := testutil.ToFloat64(rec.AuditEvents.WithLabelValues("az", "ok")); got != 2 {
		t.Errorf("az/ok = %v, want 2", got)
	}
	if got := testutil.ToFloat64(rec.AuditEvents.WithLabelValues("auth", "error")); got != 1 {
		t.Errorf("auth/error = %v, want 1", got)
	}
}

func TestAuditMetric_HonoursPrefix(t *testing.T) {
	if got := auditActionPrefix("az.create"); got != "az" {
		t.Errorf("az.create → %q, want az", got)
	}
	if got := auditActionPrefix("auth.callback.failed"); got != "auth" {
		t.Errorf("auth.callback.failed → %q, want auth", got)
	}
	if got := auditActionPrefix("no-dot-here"); got != "no-dot-here" {
		t.Errorf("no-dot → %q, want no-dot-here", got)
	}
	if got := auditActionPrefix(""); got != "" {
		t.Errorf("empty → %q, want \"\"", got)
	}
}

func TestAuditMetric_NoCrashWhenMetricsNil(t *testing.T) {
	// Defensive : Audit() must never panic on a nil metrics
	// recorder. Production unwinds Deps.Metrics into the package
	// global ; a test that doesn't construct one (e.g. an isolated
	// unit test for some other handler) shouldn't trip the audit
	// path.
	prev := metrics
	t.Cleanup(func() { metrics = prev })
	metrics = nil

	Audit(context.Background(), audit.NopLogger{}, "az.create", "az", "u1", "", nil, nil)
	// success = no panic
}
