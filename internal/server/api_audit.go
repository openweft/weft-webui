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
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/audit"
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
	Limit  int    `query:"limit" doc:"How many recent events to return" minimum:"1" maximum:"1000"`
	Action string `query:"action" doc:"Optional substring filter on event.action (e.g. \"auth.\", \"az.\")" maxLength:"64"`
	Result string `query:"result" doc:"Optional exact-match filter on event.result (\"ok\", \"error\")" enum:",ok,error"`
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

func mountAuditAPI(api huma.API, scope Scope) {
	if scope != ScopeAdmin {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "tail-audit-log",
		Method:      "GET",
		Path:        "/api/audit-log",
		Summary:     "Tail the recent audit log entries (cluster-admin)",
		Description: "Returns the most recent N events from the audit JSONL file (newest first). Without --audit-log-path the endpoint returns enabled=false + an empty list, so the dashboard can show a friendly \"audit log not enabled\" panel instead of 503.",
		Tags:        []string{"audit"},
	}, func(_ context.Context, in *auditTailInput) (*auditTailOutput, error) {
		out := &auditTailOutput{}
		if auditTail == nil {
			out.Body.Enabled = false
			out.Body.Events = []AuditEventDTO{}
			return out, nil
		}
		limit := in.Limit
		if limit == 0 {
			limit = 100
		}
		events, err := auditTail.Tail(limit)
		if err != nil {
			return nil, huma.Error500InternalServerError("audit: tail: " + err.Error())
		}
		out.Body.Enabled = true
		out.Body.Events = make([]AuditEventDTO, 0, len(events))
		for _, ev := range events {
			if in.Action != "" && !containsFold(ev.Action, in.Action) {
				continue
			}
			if in.Result != "" && ev.Result != in.Result {
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
		}
		return out, nil
	})
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
