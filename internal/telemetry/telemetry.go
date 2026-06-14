// Package telemetry registers the weft-webui Prometheus metrics and
// exposes a small Recorder API the HTTP + gRPC layers call from their
// middleware.
//
// Two metric families live here :
//
//   - infra        : HTTP request rate / latency / status, gRPC call
//                    rate / latency to weft-agent, plus the standard Go
//                    process + runtime collectors.
//   - user-flavour : login outcomes, active sessions gauge, per-user
//                    action counters. Identifying labels are the
//                    OIDC `sub` (stable, opaque) ; never email or
//                    name, so a Prometheus scrape doesn't leak PII
//                    into the TSDB beyond what the operator already
//                    has from dex audit logs.
//
// The /metrics endpoint is mounted on the admin server only (see
// server.AdminHandler). The user-facing port never exposes it ; that
// way an inadvertent public scrape can't enumerate users.
package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Recorder is the small interface the rest of the codebase depends on,
// so tests can plug a no-op without standing up a real registry.
type Recorder struct {
	Registry *prometheus.Registry

	HTTPRequests   *prometheus.CounterVec
	HTTPDuration   *prometheus.HistogramVec
	HTTPInflight   prometheus.Gauge
	GRPCCalls      *prometheus.CounterVec
	GRPCDuration   *prometheus.HistogramVec
	Logins         *prometheus.CounterVec
	ActiveSessions prometheus.Gauge
	UserActions    *prometheus.CounterVec
	AuditEvents    *prometheus.CounterVec
	BuildInfo      *prometheus.GaugeVec
}

// New builds a registry with the standard Go + process collectors and
// the webui-specific metrics. Returns a ready-to-use Recorder.
//
// version is the build version string ("dev" is fine for unstamped
// builds). Recorded once as a build-info gauge so dashboards can pin
// queries to a specific release.
func New(version string) *Recorder {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	r := &Recorder{Registry: reg}

	r.HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "weft_webui", Subsystem: "http", Name: "requests_total",
		Help: "HTTP requests served, by persona and route.",
	}, []string{"persona", "method", "route", "status"})

	r.HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "weft_webui", Subsystem: "http", Name: "request_duration_seconds",
		Help:    "HTTP request duration distribution.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"persona", "method", "route"})

	r.HTTPInflight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "weft_webui", Subsystem: "http", Name: "in_flight_requests",
		Help: "Currently active HTTP requests.",
	})

	r.GRPCCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "weft_webui", Subsystem: "grpc", Name: "calls_total",
		Help: "gRPC calls to weft-agent, by method and outcome.",
	}, []string{"method", "status"})

	r.GRPCDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "weft_webui", Subsystem: "grpc", Name: "call_duration_seconds",
		Help:    "Duration of gRPC calls to weft-agent.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"method"})

	r.Logins = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "weft_webui", Subsystem: "auth", Name: "logins_total",
		Help: "OIDC login attempts by outcome.",
	}, []string{"result"})

	r.ActiveSessions = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "weft_webui", Subsystem: "auth", Name: "active_sessions",
		Help: "Sessions seen on the most recent /api/me call within the last 5 minutes (sliding).",
	})

	r.UserActions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "weft_webui", Subsystem: "user", Name: "actions_total",
		Help: "User-initiated actions (uploads, mutations) by OIDC subject + action.",
	}, []string{"sub", "action"})

	r.AuditEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "weft_webui", Subsystem: "audit", Name: "events_total",
		Help: "Audit events emitted, by action prefix (\"auth.\", \"az.\", …) and result. Surge on auth.callback.failed or auth.callback.throttled is the canonical brute-force signal.",
	}, []string{"action", "result"})

	r.BuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "weft_webui", Subsystem: "build", Name: "info",
		Help: "1 ; labels carry build version.",
	}, []string{"version"})
	r.BuildInfo.WithLabelValues(version).Set(1)

	reg.MustRegister(
		r.HTTPRequests, r.HTTPDuration, r.HTTPInflight,
		r.GRPCCalls, r.GRPCDuration,
		r.Logins, r.ActiveSessions, r.UserActions,
		r.AuditEvents,
		r.BuildInfo,
	)
	return r
}

// Handler returns the /metrics http.Handler bound to this registry.
// Mount it on the admin server only.
func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.Registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
		Registry:      r.Registry,
	})
}

// ObserveHTTP is the convenience for the HTTP middleware.
// route is the *normalised* route label (see RouteLabel), so cardinality
// stays bounded even when path parameters (resource ID, bucket name)
// would otherwise blow it up.
func (r *Recorder) ObserveHTTP(persona, method, route string, status int, dur time.Duration) {
	if r == nil {
		return
	}
	st := strconv.Itoa(status)
	r.HTTPRequests.WithLabelValues(persona, method, route, st).Inc()
	r.HTTPDuration.WithLabelValues(persona, method, route).Observe(dur.Seconds())
}

// ObserveGRPC is what the gRPC client interceptor calls after every
// call. status is "ok" or the canonical gRPC code name ("Unavailable",
// "Unauthenticated", …) — strings keep the label space small.
func (r *Recorder) ObserveGRPC(method, status string, dur time.Duration) {
	if r == nil {
		return
	}
	r.GRPCCalls.WithLabelValues(method, status).Inc()
	r.GRPCDuration.WithLabelValues(method).Observe(dur.Seconds())
}

// Login records an auth attempt. result ∈ {"success", "failure"}.
func (r *Recorder) Login(result string) {
	if r == nil {
		return
	}
	r.Logins.WithLabelValues(result).Inc()
}

// UserAction increments the per-user action counter. Pass the OIDC
// subject (stable, opaque) ; never the email.
func (r *Recorder) UserAction(sub, action string) {
	if r == nil || sub == "" {
		return
	}
	r.UserActions.WithLabelValues(sub, action).Inc()
}

// RouteLabel normalises a request path into a low-cardinality route
// label. Keep this in sync with the routes registered in
// internal/server/server.go.
//
// Anything not in the API surface collapses to "spa" so the static
// asset traffic doesn't pollute the histograms.
func RouteLabel(method, path string) string {
	if len(path) < 5 || path[:5] != "/api/" {
		return "spa"
	}
	switch {
	case path == "/api/healthz":
		return "GET /api/healthz"
	case path == "/api/readyz":
		return "GET /api/readyz"
	case path == "/api/resources":
		return "GET /api/resources"
	case path == "/api/summary":
		return "GET /api/summary"
	case path == "/api/flavors":
		return "GET /api/flavors"
	case path == "/api/me":
		return "GET /api/me"
	case path == "/api/events":
		return "GET /api/events"
	case path == "/api/quotas":
		return "GET /api/quotas"
	case path == "/api/network-topology":
		return "GET /api/network-topology"
	case path == "/api/session/scope":
		return "POST /api/session/scope"
	case path == "/api/registry/upload":
		return "POST /api/registry/upload"
	case path == "/api/buckets":
		return method + " /api/buckets"
	case path == "/api/tenants":
		return method + " /api/tenants"
	case path == "/api/auth/login":
		return "GET /api/auth/login"
	case path == "/api/auth/callback":
		return "GET /api/auth/callback"
	case path == "/api/auth/logout":
		return method + " /api/auth/logout"
	case path == "/metrics":
		return "GET /metrics"
	}
	// Parametrised routes : collapse the variable segment.
	switch {
	case prefix(path, "/api/resources/"):
		return "GET /api/resources/:id"
	case prefix(path, "/api/tenants/") && suffix(path, "/admins"):
		return "POST /api/tenants/:name/admins"
	case prefix(path, "/api/tenants/") && suffix(path, "/projects"):
		return "POST /api/tenants/:name/projects"
	case prefix(path, "/api/tenants/") && suffix(path, "/members"):
		return "POST /api/tenants/:name/members"
	case prefix(path, "/api/tenants/") && suffix(path, "/quota"):
		return method + " /api/tenants/:name/quota"
	case prefix(path, "/api/tenants/"):
		return method + " /api/tenants/:name"
	case prefix(path, "/api/projects/") && suffix(path, "/roles"):
		return "POST /api/projects/:name/roles"
	case prefix(path, "/api/projects/") && suffix(path, "/quota"):
		return method + " /api/projects/:name/quota"
	case prefix(path, "/api/microvms/") && suffix(path, "/start"):
		return "POST /api/microvms/:name/start"
	case prefix(path, "/api/microvms/") && suffix(path, "/stop"):
		return "POST /api/microvms/:name/stop"
	case prefix(path, "/api/microvms/") && suffix(path, "/status"):
		return "GET /api/microvms/:name/status"
	case prefix(path, "/api/microvms/") && suffix(path, "/timings"):
		return "GET /api/microvms/:name/timings"
	case prefix(path, "/api/microvms/") && suffix(path, "/logs"):
		return "GET /api/microvms/:name/logs"
	case prefix(path, "/api/microvms/"):
		return method + " /api/microvms/:name"
	case path == "/api/microvms":
		return method + " /api/microvms"
	case prefix(path, "/api/volumes/") && suffix(path, "/attach"):
		return "POST /api/volumes/:uuid/attach"
	case prefix(path, "/api/volumes/") && suffix(path, "/detach"):
		return "POST /api/volumes/:uuid/detach"
	case prefix(path, "/api/volumes/"):
		return method + " /api/volumes/:uuid"
	case path == "/api/volumes":
		return method + " /api/volumes"
	case prefix(path, "/api/networks/"):
		return method + " /api/networks/:uuid"
	case path == "/api/networks":
		return method + " /api/networks"
	case prefix(path, "/api/scheduling-rules/"):
		return method + " /api/scheduling-rules/:name"
	case path == "/api/scheduling-rules":
		return method + " /api/scheduling-rules"
	case path == "/api/routers":
		return method + " /api/routers"
	case prefix(path, "/api/routers/"):
		return method + " /api/routers/:uuid"
	case path == "/api/loadbalancers":
		return method + " /api/loadbalancers"
	case prefix(path, "/api/loadbalancers/") && suffix(path, "/backends"):
		return "PUT /api/loadbalancers/:uuid/backends"
	case prefix(path, "/api/loadbalancers/"):
		return method + " /api/loadbalancers/:uuid"
	case path == "/api/dns-zones":
		return method + " /api/dns-zones"
	case prefix(path, "/api/dns-zones/"):
		return method + " /api/dns-zones/:uuid"
	case path == "/api/dns-records":
		return method + " /api/dns-records"
	case prefix(path, "/api/dns-records/"):
		return method + " /api/dns-records/:uuid"
	case path == "/api/shares":
		return method + " /api/shares"
	case path == "/api/security-groups":
		return method + " /api/security-groups"
	case prefix(path, "/api/security-groups/") && suffix(path, "/rules"):
		return method + " /api/security-groups/:uuid/rules"
	case prefix(path, "/api/security-groups/"):
		return method + " /api/security-groups/:uuid"
	case path == "/api/floating-ips":
		return method + " /api/floating-ips"
	case prefix(path, "/api/floating-ips/") && suffix(path, "/map"):
		return "POST /api/floating-ips/:uuid/map"
	case prefix(path, "/api/floating-ips/") && suffix(path, "/unmap"):
		return "POST /api/floating-ips/:uuid/unmap"
	case prefix(path, "/api/floating-ips/"):
		return method + " /api/floating-ips/:uuid"

	case prefix(path, "/api/buckets/") && suffix(path, "/objects"):
		return method + " /api/buckets/:name/objects"
	case prefix(path, "/api/buckets/") && suffix(path, "/object"):
		return "GET /api/buckets/:name/object"
	case prefix(path, "/api/buckets/"):
		return method + " /api/buckets/:name"
	case prefix(path, "/api/shares/") && suffix(path, "/objects"):
		return method + " /api/shares/:name/objects"
	case prefix(path, "/api/shares/") && suffix(path, "/object"):
		return "GET /api/shares/:name/object"
	case prefix(path, "/api/shares/"):
		// DELETE /api/shares/{name} — share-level mutations.
		return method + " /api/shares/:name"
	}
	return "api-other"
}

func prefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func suffix(s, p string) bool { return len(s) >= len(p) && s[len(s)-len(p):] == p }
