// audit_hook.go — Audit() is the one-stop helper handlers call right
// after a mutation. It enriches the caller-supplied (action, kind, id,
// result, err, extra) with subject / tenant / project (from the
// session-in-context), request-id (from the chi-style middleware), and
// remote IP (from the *http.Request, when one was injected by
// withHTTPRequest middleware).
//
// The signature deliberately takes a Logger argument so callers don't
// hardcode the package-global ; the same helper is called from tests
// with a NopLogger or a captured logger.
package server

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/auth"
)

// httpRequestKey carries the originating *http.Request through the
// context so non-http-aware handlers (huma) can still read RemoteAddr.
// withHTTPRequest middleware sets it ; Audit reads it.
type httpRequestKey struct{}

// withHTTPRequest stamps the originating *http.Request into the
// request context. Mounted alongside withRequestID so every handler
// (including the huma ones) sees both.
func withHTTPRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), httpRequestKey{}, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestFromContext returns the *http.Request stowed in ctx, or nil.
func requestFromContext(ctx context.Context) *http.Request {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(httpRequestKey{}).(*http.Request)
	return r
}

// remoteIP returns the IP portion of r.RemoteAddr (host without port).
// Falls back to the raw RemoteAddr when SplitHostPort can't make sense
// of it (unix sockets, IPv6 brackets quirks).
func remoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Audit emits one audit event through logger. nil logger collapses to
// audit.NopLogger so the call site never needs the `if logger != nil`
// dance. result defaults to "ok" when empty + err == nil ; "error"
// when err != nil. The helper is intentionally permissive about
// missing context — better a thin event than a dropped one.
func Audit(ctx context.Context, logger audit.Logger, action, kind, id, result string, err error, extra map[string]string) {
	if logger == nil {
		logger = audit.NopLogger{}
	}
	ev := audit.Event{
		Timestamp:    time.Now().UTC(),
		Action:       action,
		ResourceKind: kind,
		ResourceID:   id,
		Result:       result,
		Extra:        extra,
	}
	if u := auth.UserFromContext(ctx); u != nil {
		ev.Subject = u.Subject
		ev.Tenant = u.Tenant
		ev.Project = u.Project
	}
	if r := requestFromContext(ctx); r != nil {
		ev.RemoteIP = remoteIP(r)
	}
	ev.RequestID = requestIDFromContext(ctx)
	if err != nil {
		ev.ErrorMessage = err.Error()
		if ev.Result == "" {
			ev.Result = "error"
		}
	} else if ev.Result == "" {
		ev.Result = "ok"
	}
	logger.Log(ctx, ev)
	// Mirror to the prometheus AuditEvents counter (when wired) so
	// operators can alert on surge — auth.callback.failed +
	// auth.callback.throttled are the canonical brute-force
	// signals. Labels are kept low-cardinality : the action prefix
	// up to the first dot (auth, az, rack, host, vm, …) + the
	// result, never the full action string + never the subject.
	if metrics != nil && metrics.AuditEvents != nil {
		metrics.AuditEvents.WithLabelValues(auditActionPrefix(ev.Action), ev.Result).Inc()
	}
}

// auditActionPrefix returns the bit of `action` before the first
// dot, so a label cardinality of ~dozens (auth, az, rack, host, vm,
// volume, dns, security-group, scheduling-rule, plugin, …) instead
// of thousands. "az.create" → "az". No dot → the action itself.
func auditActionPrefix(action string) string {
	for i := 0; i < len(action); i++ {
		if action[i] == '.' {
			return action[:i]
		}
	}
	return action
}
