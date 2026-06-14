// api_audit.go — typed read endpoint for the JSONL audit log.
//
// Operators see the trail in the dashboard rather than ssh+jq+grep.
// Admin-scope only — the user listener returns 404, so a regular user
// never even sees the route exists. Live reads tail the configured
// audit-log file backwards in chunks ; without --audit-log-path the
// endpoint returns an empty list.
//
// Pagination is "tail-N" with a client-controlled limit (1..1000). Real
// time-range pagination lands once an operator asks for it ; today's
// audit volume is comfortable with a single-shot tail.

package server

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/auth"
)

// auditTailer is the seam server.New() can set so the endpoint sees
// the live FileLogger without coupling api_audit.go to the audit
// package's concrete type. Set by SetAuditTailer (called from main).
type auditTailer interface {
	Tail(n int) ([]audit.Event, error)
}

var auditTail auditTailer

// SetAuditTailer hands the (optional) audit-log reader to the
// server. nil = endpoint serves an empty list. Safe to call once at
// startup ; never called from a request hot path.
func SetAuditTailer(t auditTailer) {
	auditTail = t
}

type auditTailInput struct {
	Limit   int    `query:"limit" doc:"How many recent events to return" minimum:"1" maximum:"1000"`
	Action  string `query:"action" doc:"Optional substring filter on event.action (e.g. \"auth.\", \"az.\")" maxLength:"64"`
	Result  string `query:"result" doc:"Optional exact-match filter on event.result (\"ok\", \"error\")" enum:",ok,error"`
	Subject string `query:"subject" doc:"Optional substring filter on event.subject (the OIDC sub / email of the actor)" maxLength:"128"`
	Since   string `query:"since" doc:"Optional RFC3339 lower bound (inclusive) on event.ts. Example : 2026-06-02T00:00:00Z" maxLength:"40"`
	Until   string `query:"until" doc:"Optional RFC3339 upper bound (exclusive) on event.ts. Example : 2026-06-03T00:00:00Z" maxLength:"40"`
}

// AuditEventDTO mirrors audit.Event but with JSON-friendly defaults
// (RFC3339 timestamp, all-optional extras) so the openapi-typescript
// generator emits a clean shape. audit.Event itself can't be reused
// because huma can't introspect map[string]string for OpenAPI.
type AuditEventDTO struct {
	Timestamp    string            `json:"ts"`
	Subject      string            `json:"subject,omitempty"`
	Tenant       string            `json:"tenant,omitempty"`
	Project      string            `json:"project,omitempty"`
	Action       string            `json:"action"`
	ResourceKind string            `json:"resource_kind,omitempty"`
	ResourceID   string            `json:"resource_id,omitempty"`
	Result       string            `json:"result,omitempty"`
	ErrorMessage string            `json:"error,omitempty"`
	RemoteIP     string            `json:"remote_ip,omitempty"`
	RequestID    string            `json:"request_id,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

type auditTailOutput struct {
	Body struct {
		Events  []AuditEventDTO `json:"events" doc:"Newest first"`
		Enabled bool            `json:"enabled" doc:"True when an audit-log file is wired up. When false, the events list is always empty even if the operator stored history previously."`
	}
}

// mountAuditCSVExport wires the stdlib (non-huma) CSV handler onto
// the mux. Called from server.go alongside the other stdlib routes
// so the same scope filter applies.
func mountAuditCSVExport(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/audit-log/export.csv", handleAuditCSVExport)
}

func mountAuditAPI(api huma.API, scope Scope) {
	// Tenant + Infra portals both expose /api/audit-log : the tenant
	// portal narrows the view to tenant-scoped entries via per-row
	// filtering inside the handler ; the infra portal sees the
	// cluster-wide stream. The user portal never sees this surface.
	if !scope.Has(ScopeTenant) && !scope.Has(ScopeAdmin) {
		return
	}
	// Capture the mount-time scope in the closure so the handler
	// knows which portal it's serving. Infra-portal callers
	// (ScopeAdmin) get the cluster-wide stream ; tenant-portal
	// callers see only entries whose ev.Tenant matches the session's
	// own tenant — a tenant-admin must not learn about other
	// tenants' actions via the audit trail.
	infra := scope.Has(ScopeAdmin)
	huma.Register(api, huma.Operation{
		OperationID: "tail-audit-log",
		Method:      "GET",
		Path:        "/api/audit-log",
		Summary:     "Tail the recent audit log entries",
		Description: "Returns the most recent N events from the audit JSONL file (newest first). The tenant portal narrows the view to events tagged with the caller's own tenant ; the infra portal sees every event. Without --audit-log-path the endpoint returns enabled=false + an empty list, so the dashboard can show a friendly \"audit log not enabled\" panel instead of 503.",
		Tags:        []string{"audit"},
	}, func(ctx context.Context, in *auditTailInput) (*auditTailOutput, error) {
		out := &auditTailOutput{}
		if auditTail == nil {
			out.Body.Enabled = false
			out.Body.Events = []AuditEventDTO{}
			return out, nil
		}
		// Tenant filter : non-admin callers see only their own
		// tenant. Empty session.Tenant on a non-admin path collapses
		// to "no events" rather than "all events" — we never want
		// to leak cross-tenant on a misconfigured caller.
		var tenantFilter string
		if !infra {
			if u := auth.UserFromContext(ctx); u != nil {
				tenantFilter = u.Tenant
			}
		}
		limit := in.Limit
		if limit == 0 {
			limit = 100
		}
		// Tail more than requested when we'll be filtering — the
		// filter can drop the count below the limit. Cap the
		// pre-filter window at 10x the limit so a tenant with very
		// little activity doesn't pin the reader on a huge log.
		// Parse the time bounds once before iterating. Bad input is
		// a 400 — we don't silently ignore a malformed RFC3339 string.
		var sinceT, untilT time.Time
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, huma.Error400BadRequest("since: invalid RFC3339 timestamp: " + err.Error())
			}
			sinceT = t
		}
		if in.Until != "" {
			t, err := time.Parse(time.RFC3339, in.Until)
			if err != nil {
				return nil, huma.Error400BadRequest("until: invalid RFC3339 timestamp: " + err.Error())
			}
			untilT = t
		}
		if !sinceT.IsZero() && !untilT.IsZero() && !untilT.After(sinceT) {
			return nil, huma.Error400BadRequest("until must be after since")
		}

		fetch := limit
		hasFilter := tenantFilter != "" || in.Action != "" ||
			in.Result != "" || in.Subject != "" ||
			!sinceT.IsZero() || !untilT.IsZero()
		if hasFilter {
			fetch = limit * 10
			if fetch > 10000 {
				fetch = 10000
			}
		}
		events, err := auditTail.Tail(fetch)
		if err != nil {
			return nil, huma.Error500InternalServerError("audit: tail: " + err.Error())
		}
		out.Body.Enabled = true
		out.Body.Events = make([]AuditEventDTO, 0, limit)
		for _, ev := range events {
			if in.Action != "" && !containsFold(ev.Action, in.Action) {
				continue
			}
			if in.Result != "" && ev.Result != in.Result {
				continue
			}
			if in.Subject != "" && !containsFold(ev.Subject, in.Subject) {
				continue
			}
			if tenantFilter != "" && ev.Tenant != tenantFilter {
				continue
			}
			if !sinceT.IsZero() && ev.Timestamp.Before(sinceT) {
				continue
			}
			if !untilT.IsZero() && !ev.Timestamp.Before(untilT) {
				continue
			}
			out.Body.Events = append(out.Body.Events, AuditEventDTO{
				Timestamp:    ev.Timestamp.UTC().Format(time.RFC3339Nano),
				Subject:      ev.Subject,
				Tenant:       ev.Tenant,
				Project:      ev.Project,
				Action:       ev.Action,
				ResourceKind: ev.ResourceKind,
				ResourceID:   ev.ResourceID,
				Result:       ev.Result,
				ErrorMessage: ev.ErrorMessage,
				RemoteIP:     ev.RemoteIP,
				RequestID:    ev.RequestID,
				Extra:        ev.Extra,
			})
			if len(out.Body.Events) >= limit {
				break
			}
		}
		return out, nil
	})
}

// handleAuditCSVExport serves a CSV dump of the audit events that
// match the same filters as GET /api/audit-log. Mounted outside
// huma because huma's body model is JSON-only — CSV needs a
// hand-rolled writer + Content-Type. Admin-only at the auth layer ;
// scope-gating is done in server.go by guarding the mount.
//
// Useful for the compliance hand-off : an operator drops the file
// into a ticket / shared-drive / SIEM ingest without having to
// post-process JSON.
func handleAuditCSVExport(w http.ResponseWriter, r *http.Request) {
	if auditTail == nil {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("error\naudit log not enabled (set WEBUI_AUDIT_LOG_PATH)\n"))
		return
	}
	q := r.URL.Query()
	limit := 1000
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			http.Error(w, "limit: must be int 1..10000", http.StatusBadRequest)
			return
		}
		limit = n
	}
	var sinceT, untilT time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "since: invalid RFC3339", http.StatusBadRequest)
			return
		}
		sinceT = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "until: invalid RFC3339", http.StatusBadRequest)
			return
		}
		untilT = t
	}
	action := q.Get("action")
	result := q.Get("result")
	subject := q.Get("subject")

	// Tenant scope : the dashboard reads it the same way as the
	// JSON endpoint via auth.UserFromContext.
	var tenantFilter string
	if u := auth.UserFromContext(r.Context()); u != nil {
		// Heuristic : if the caller is cluster-admin (Admin group
		// or DevMode) we don't filter. Otherwise narrow to their
		// own tenant. Mirrors the JSON endpoint's `infra` capture.
		if !isClusterAdmin(u) {
			tenantFilter = u.Tenant
		}
	}

	events, err := auditTail.Tail(limit * 5)
	if err != nil {
		http.Error(w, "audit: tail: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-`+time.Now().UTC().Format("20060102-150405")+`.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"ts", "subject", "tenant", "project", "action",
		"resource_kind", "resource_id", "result", "error",
		"remote_ip", "request_id",
	})
	written := 0
	for _, ev := range events {
		if action != "" && !containsFold(ev.Action, action) {
			continue
		}
		if result != "" && ev.Result != result {
			continue
		}
		if subject != "" && !containsFold(ev.Subject, subject) {
			continue
		}
		if tenantFilter != "" && ev.Tenant != tenantFilter {
			continue
		}
		if !sinceT.IsZero() && ev.Timestamp.Before(sinceT) {
			continue
		}
		if !untilT.IsZero() && !ev.Timestamp.Before(untilT) {
			continue
		}
		_ = cw.Write([]string{
			ev.Timestamp.UTC().Format(time.RFC3339Nano),
			ev.Subject, ev.Tenant, ev.Project, ev.Action,
			ev.ResourceKind, ev.ResourceID, ev.Result, ev.ErrorMessage,
			ev.RemoteIP, ev.RequestID,
		})
		written++
		if written >= limit {
			break
		}
	}
}

// containsFold returns true when needle is a case-insensitive
// substring of hay. Tiny helper so we don't pull strings.EqualFold +
// strings.Contains on lowercased copies.
func containsFold(hay, needle string) bool {
	if len(needle) > len(hay) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := hay[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
