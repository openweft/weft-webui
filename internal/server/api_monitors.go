// api_monitors.go — cross-host respawn HA topology surface for the
// dashboard.
//
//   GET /api/monitors  — current set of live weft-agent monitors
//
// Background. weft v0.4.1 ships respawn V0.1.3 : every weft-agent
// runs an in-process monitor + lease on etcd at
// `/weft/coord/hosts/<host_uuid>`. When a host's lease expires,
// surviving monitors elect a leader per SchedulingRule and the
// leader claims the orphan VMs. The number of live monitors equals
// the number of healthy weft agents, which equals the number of
// reachable hosts. A drop is the canonical operational signal of a
// DC partition or rack outage — surface it prominently.
//
// Wiring : the Prometheus gauge `weft_monitors_live` carries the
// scalar count ; the etcd prefix `/weft/coord/hosts/` carries the
// per-host metadata (hostname, hypervisor, version, started_at).
// We read etcd directly via the `monitorsSource` injected at server
// startup (see main.go). When no source is wired (dev mode, mock
// fallback, etcd unreachable) the endpoint returns an empty list and
// `expected_count = 0` so the SPA can render "monitors offline"
// instead of a misleading "0 of N".
//
// `expected_count` defaults to the etcd member count surfaced by the
// source ; operators can override via WEFT_WEBUI_EXPECTED_MONITORS or
// the cluster-level HCL setting (so a static 3-DC expectation survives
// an etcd member roster shuffle during day-2 ops).
package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// monitorsSource is the read surface the api_monitors handler needs.
// Production wiring satisfies this via an etcd client (see
// internal/etcdsource) ; tests use an in-memory fake.
//
// Hosts returns the deserialised JSON values from /weft/coord/hosts/.
// MemberCount returns the etcd cluster member count — used as the
// default expected_count when the operator hasn't pinned a static
// value via env / HCL.
type monitorsSource interface {
	Hosts(ctx context.Context) ([]MonitorHost, error)
	MemberCount(ctx context.Context) (int, error)
}

// monitorsSrc holds the active source. nil = offline ; the endpoint
// stays registered but returns an empty list. Package-global so the
// handler closure stays light — same shape as `live` and `liveNet`.
var monitorsSrc monitorsSource

// SetMonitorsSource wires the etcd-backed source from main.go. nil
// detaches and the endpoint reverts to offline behaviour.
func SetMonitorsSource(s monitorsSource) { monitorsSrc = s }

// expectedMonitorsOverride is the static count an operator can pin via
// WEFT_WEBUI_EXPECTED_MONITORS or the HCL setting. 0 = no override,
// fall back to the etcd member count.
var expectedMonitorsOverride atomic.Int32

// SetExpectedMonitorsOverride pins the expected_count returned by
// /api/monitors. Pass 0 to clear (revert to the etcd member count).
// Atomic so a hot reload doesn't tear.
func SetExpectedMonitorsOverride(n int) {
	if n < 0 {
		n = 0
	}
	expectedMonitorsOverride.Store(int32(n))
}

// MonitorHost is one row of /api/monitors. Shape mirrors the JSON
// stored at `/weft/coord/hosts/<host_uuid>` by weft-agent. StartedAt
// is RFC-3339 so the SPA can compute "running for 2h 15m" without
// timezone surprises.
type MonitorHost struct {
	HostUUID   string `json:"host_uuid"   doc:"Host identifier — matches the etcd lease key suffix and the weft-agent --host-uuid flag"`
	Hostname   string `json:"hostname"    doc:"Operator-visible hostname (e.g. dc1-r1-h1)"`
	Hypervisor string `json:"hypervisor"  doc:"'qemu' | 'vz' | 'vmd' | 'dcs' — the backend driver this host runs"`
	Version    string `json:"version"     doc:"weft-agent version string (e.g. v0.4.1) — surfaces version skew across the fleet"`
	StartedAt  string `json:"started_at"  doc:"RFC-3339 timestamp of the monitor's last (re)start ; uptime is derived client-side"`
}

// MonitorsBody is the response envelope. Count + ExpectedCount let
// the SPA color-code the badge without reading the array length :
//
//   - count == expected_count          → badge-success (every monitor up)
//   - count < expected_count, quorum-ok → badge-warning (degraded)
//   - count < ceil(expected_count/2)   → badge-error (quorum lost)
type MonitorsBody struct {
	Monitors      []MonitorHost `json:"monitors"       doc:"Live monitor set ordered by hostname for stable rendering"`
	Count         int           `json:"count"          doc:"Number of live monitors at the moment of the read"`
	ExpectedCount int           `json:"expected_count" doc:"Expected monitor count — operator-pinned override or the etcd member count"`
}

type listMonitorsOutput struct {
	Body MonitorsBody
}

func mountMonitorsAPI(api huma.API, scope Scope) {
	// Monitors surface is operationally privileged : it leaks the
	// per-host hostname + hypervisor + version skew, which a regular
	// tenant user has no business seeing. Keep it Infra-only ; the
	// user + tenant portals return 404.
	if !scope.Has(ScopeAdmin) {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-monitors",
		Method:      "GET",
		Path:        "/api/monitors",
		Summary:     "List the live weft-agent monitor set (cross-host respawn HA topology)",
		Description: "Reads /weft/coord/hosts/<host_uuid> from etcd — one entry per healthy weft-agent in the cluster. A drop in count vs expected_count is the canonical signal of a DC partition or rack outage. expected_count defaults to the etcd member count ; pin a static value via WEFT_WEBUI_EXPECTED_MONITORS for clusters where the static topology should drive the badge regardless of etcd roster churn.",
		Tags:        []string{"monitors"},
	}, func(ctx context.Context, _ *struct{}) (*listMonitorsOutput, error) {
		out := &listMonitorsOutput{}
		out.Body = listMonitors(ctx)
		return out, nil
	})
}

// listMonitors gathers the live monitor set + expected count. When
// the source is nil (dev / preview / etcd unreachable) we return an
// empty list rather than 502 so the dashboard panel can render an
// honest "monitors offline" state.
func listMonitors(ctx context.Context) MonitorsBody {
	body := MonitorsBody{Monitors: []MonitorHost{}}
	if monitorsSrc == nil {
		// Override still applies offline so a misconfiguration that
		// kills etcd reads doesn't also kill the expected baseline.
		if n := int(expectedMonitorsOverride.Load()); n > 0 {
			body.ExpectedCount = n
		}
		return body
	}

	// Short read deadline — the SPA polls every 5s, so a 3s ceiling
	// keeps the handler responsive even when etcd is laggy.
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	hosts, err := monitorsSrc.Hosts(rctx)
	if err == nil {
		body.Monitors = hosts
		body.Count = len(hosts)
	}

	if n := int(expectedMonitorsOverride.Load()); n > 0 {
		body.ExpectedCount = n
	} else {
		// Member count fallback. A failure here is non-fatal —
		// expected_count stays at 0 (badge-warning) so the operator
		// knows the baseline is unknown, not that the cluster is
		// healthy by accident.
		if mc, mcErr := monitorsSrc.MemberCount(rctx); mcErr == nil {
			body.ExpectedCount = mc
		}
	}
	return body
}

// DecodeMonitorHost parses one /weft/coord/hosts/<uuid> JSON value
// into a MonitorHost. Exported so the etcdsource package can reuse
// it without an internal/-only seam (one source of truth for the
// wire shape).
//
// The on-wire shape weft-agent writes carries `started_at_unix_ns`
// (int64) rather than a string — we convert to RFC-3339 here so the
// SPA stays simple. A missing / zero timestamp drops the field so
// the client can render "n/a" rather than 1970-01-01.
func DecodeMonitorHost(raw []byte) (MonitorHost, error) {
	var wire struct {
		HostUUID         string `json:"host_uuid"`
		Hostname         string `json:"hostname"`
		Hypervisor       string `json:"hypervisor"`
		Version          string `json:"version"`
		StartedAtUnixNS  int64  `json:"started_at_unix_ns"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return MonitorHost{}, err
	}
	out := MonitorHost{
		HostUUID:   wire.HostUUID,
		Hostname:   wire.Hostname,
		Hypervisor: wire.Hypervisor,
		Version:    wire.Version,
	}
	if wire.StartedAtUnixNS > 0 {
		out.StartedAt = time.Unix(0, wire.StartedAtUnixNS).UTC().Format(time.RFC3339)
	}
	return out, nil
}
