// api_auth_throttle.go — admin endpoints that operate on the per-IP
// auth-callback failure budget.
//
//   GET    /api/auth/throttle           — list currently-tracked IPs
//   DELETE /api/auth/throttle/{ip}      — clear an IP's lock
//
// Admin-only ; a SOC analyst or on-call uses these to either
// confirm "yes there's an active spray attack" (list) or release
// a legitimate operator who got caught in the window (delete).
// Every clear emits an audit event so the action is itself
// traceable.

package server

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/auth"
)

func mountAuthThrottleAPI(api huma.API, scope Scope) {
	if !scope.Has(ScopeAdmin) {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-throttled-ips",
		Method:      "GET",
		Path:        "/api/auth/throttle",
		Summary:     "List IPs currently tracked by the auth-callback throttle",
		Description: "Returns every IP with a non-zero failure count in the current sliding window. An IP whose count >= 5 is currently locked ; below = it'll lock on its next failure. Use this + /api/audit-log?action=auth.callback to confirm a brute-force attempt and decide whether to extend the block list at the firewall.",
		Tags:        []string{"auth", "audit"},
	}, func(_ context.Context, _ *struct{}) (*listThrottledOutput, error) {
		now := authThrottle.now()
		snap := authThrottle.snapshot()
		out := &listThrottledOutput{}
		out.Body.Entries = make([]ThrottledIP, 0, len(snap))
		for ip, e := range snap {
			out.Body.Entries = append(out.Body.Entries, ThrottledIP{
				IP:          ip,
				Failures:    e.count,
				FirstHit:    e.firstHit.UTC().Format(time.RFC3339Nano),
				Locked:      e.count >= authThrottle.threshold,
				WindowEnds:  e.firstHit.Add(authThrottle.window).UTC().Format(time.RFC3339Nano),
				ExpiresIn:   int((authThrottle.window - now.Sub(e.firstHit)).Seconds()),
			})
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "clear-throttled-ip",
		Method:        "DELETE",
		Path:          "/api/auth/throttle/{ip}",
		Summary:       "Clear an IP's failure budget (cluster-admin)",
		Description:   "Drops the IP's entry from the throttle counter, releasing any current lock. The operator should ONLY do this when they've confirmed (out-of-band) that the IP belongs to a legitimate user who hit the window — e.g. someone fat-fingered their OIDC redirect a few times. Every clear is audited so a malicious admin can't quietly whitelist their own spray IP.",
		Tags:          []string{"auth", "audit"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *clearThrottleInput) (*clearThrottleOutput, error) {
		ip := strings.TrimSpace(in.IP)
		if ip == "" {
			return nil, huma.Error400BadRequest("ip is required")
		}
		removed := authThrottle.clear(ip)
		// Emit an audit event regardless of removal so a SOC analyst
		// can see "admin tried to clear ip X but it wasn't tracked"
		// — that's interesting by itself.
		subject := ""
		if u := auth.UserFromContext(ctx); u != nil {
			subject = u.Email
			if subject == "" {
				subject = u.Subject
			}
		}
		auditLogger.Log(ctx, audit.Event{
			Timestamp:    time.Now().UTC(),
			Action:       "auth.throttle.clear",
			ResourceKind: "throttle",
			ResourceID:   ip,
			Subject:      subject,
			Result:       boolToOK(removed),
			Extra:        map[string]string{"existed": boolToYN(removed)},
		})
		return &clearThrottleOutput{Body: clearThrottleResp{IP: ip, Cleared: removed}}, nil
	})
}

type listThrottledOutput struct {
	Body struct {
		Entries []ThrottledIP `json:"entries" doc:"One row per IP with active failure history"`
	}
}

type ThrottledIP struct {
	IP         string `json:"ip" doc:"Tracked IP" example:"1.2.3.4"`
	Failures   int    `json:"failures" doc:"Failure count in the current window"`
	FirstHit   string `json:"first_hit" doc:"RFC3339Nano of the first failure in this window"`
	WindowEnds string `json:"window_ends" doc:"RFC3339Nano when the window expires + the counter resets"`
	ExpiresIn  int    `json:"expires_in_seconds" doc:"Seconds until window_ends from server.now()"`
	Locked     bool   `json:"locked" doc:"True when Failures >= threshold ; the next callback returns 429"`
}

type clearThrottleInput struct {
	IP string `path:"ip" doc:"IPv4 / IPv6 string to clear" minLength:"1" maxLength:"64"`
}

type clearThrottleOutput struct {
	Body clearThrottleResp
}

type clearThrottleResp struct {
	IP      string `json:"ip"`
	Cleared bool   `json:"cleared" doc:"True when an entry existed and was removed ; false when the IP wasn't tracked"`
}

// boolToOK returns the audit-log result tag for a boolean outcome.
// "ok" reads idiomatically in the audit JSONL ; "noop" is the
// "tried to clear, nothing there" case.
func boolToOK(b bool) string {
	if b {
		return "ok"
	}
	return "noop"
}

func boolToYN(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
