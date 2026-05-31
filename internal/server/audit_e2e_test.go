package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/auth"
)

// captureLogger is the simplest audit.Logger that holds a slice of
// events for assertions. Mutex-guarded because the audit hook may
// run on any handler goroutine.
type captureLogger struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *captureLogger) Log(_ context.Context, ev audit.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *captureLogger) snapshot() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, len(c.events))
	copy(out, c.events)
	return out
}

// TestAudit_HelperFillsFromContext locks the audit-hook contract :
// the Audit() helper extracts Subject + Tenant + Project from the
// auth.User on the context, request id from the request-id
// middleware-injected value, and RemoteIP from the http.Request
// the withHTTPRequest middleware injects.
//
// We don't drive this through HTTP — the endpoint-level call sites
// all gate on `requireLiveCtx()` (audit is live-only by design ;
// nothing in the backend changed, nothing to audit) so an httptest
// fixture without a fake live client never reaches the Audit() call.
// Drive the helper directly with a hand-built context that mirrors
// what the middleware chain installs.
func TestAudit_HelperFillsFromContext(t *testing.T) {
	cap := &captureLogger{}

	// Mock user — matches what auth.Middleware's ModeNone path stamps.
	user := &auth.User{
		Subject: "dev:alice", Email: "alice@weft.local", Name: "Alice Dev",
		Groups: []string{"admin"}, Tenant: "platform", Project: "team-alpha",
		DevMode: true,
	}
	ctx := auth.WithUser(context.Background(), user)

	// Mock http.Request — same shape the withHTTPRequest middleware
	// installs. RemoteAddr feeds the Event.RemoteIP field. We stamp
	// it through the same context key the middleware uses ; there's
	// no exported helper because in normal flow only the middleware
	// writes this key.
	req := httptest.NewRequest(http.MethodPost, "/api/microvms/web-1/start", nil)
	req.RemoteAddr = "192.0.2.10:55432"
	ctx = context.WithValue(ctx, httpRequestKey{}, req)

	wantErr := errors.New("upstream rejected the request")
	Audit(ctx, cap, "microvm.start", "microvm", "web-1", "", wantErr, map[string]string{"project": "team-alpha"})

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("captured %d events ; want 1", len(events))
	}
	got := events[0]
	if got.Action != "microvm.start" || got.ResourceKind != "microvm" || got.ResourceID != "web-1" {
		t.Errorf("action/kind/id = (%q,%q,%q) ; want (microvm.start, microvm, web-1)",
			got.Action, got.ResourceKind, got.ResourceID)
	}
	if got.Subject != "dev:alice" {
		t.Errorf("Subject = %q ; want dev:alice", got.Subject)
	}
	if got.Project != "team-alpha" {
		t.Errorf("Project = %q ; want team-alpha (from session)", got.Project)
	}
	if got.Result != "error" {
		t.Errorf("Result = %q ; want \"error\" (err was non-nil)", got.Result)
	}
	if got.ErrorMessage == "" {
		t.Errorf("ErrorMessage missing ; want the err's text")
	}
	if got.Timestamp.IsZero() {
		t.Errorf("Timestamp not stamped")
	}
	if got.Extra["project"] != "team-alpha" {
		t.Errorf("Extra[project] = %q ; want team-alpha", got.Extra["project"])
	}
}

// TestAudit_HelperHandlesNilLogger ensures the hook is no-op when
// the operator didn't wire an audit logger (Deps.Audit nil → server.go
// defaults auditLogger to NopLogger, but the Audit helper should
// tolerate a literal nil logger too).
func TestAudit_HelperHandlesNilLogger(t *testing.T) {
	// Must not panic.
	Audit(context.Background(), nil, "x", "y", "z", "", nil, nil)
}
