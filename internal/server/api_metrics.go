// api_metrics.go — per-microVM live metrology snapshot endpoint.
//
// Polled by the SPA's "Metrics" drawer tab every ~5 s ; one call
// returns a single MetricsSnapshot. The frontend stores the snapshot
// stream client-side (ring buffer in a Svelte store), so this
// endpoint is intentionally stateless.
//
// Wire shape — kept stable so the SPA contract doesn't break when
// the real source lands :
//
//   GET /api/microvms/{name}/metrics?project=…
//   →   200 MetricsSnapshot {
//         sampled_at_unix : number  // server clock at sample
//         cpu_percent     : number  // 0..100 (sum across all vCPUs / vCPU count)
//         mem_used_mib    : number  // resident MiB
//         mem_total_mib   : number  // configured ceiling
//         net_rx_bps      : number  // bytes/s in
//         net_tx_bps      : number  // bytes/s out
//         disk_read_bps   : number  // bytes/s
//         disk_write_bps  : number  // bytes/s
//         uptime_seconds  : number  // since last successful boot
//         mock            : bool    // true when synthetic (no live RPC yet)
//       }
//
// Source today : no GetMicroVMMetrics RPC exists on weft-proto yet
// (see CHANGELOG follow-up). Until it lands the handler synthesises a
// plausible curve deterministically derived from the VM name + sample
// time — the SPA marks the panel "mock data" so operators don't
// mistake it for a real reading. When live wclient gains
// GetMicroVMMetrics, swap the synth call for the live call and clear
// the Mock flag ; nothing else changes.

package server

import (
	"context"
	"hash/fnv"
	"math"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/wclient"
)

// MetricsSnapshot is one polling tick's worth of per-VM data. JSON
// tags use snake_case to match the rest of the public API.
//
// Units are deliberately explicit in the field names (mib / bps /
// seconds) so the SPA never has to guess. cpu_percent is the
// aggregate across all vCPUs, normalised to 0..100 — a 4-vCPU VM at
// full tilt reports 100, not 400, which matches what htop / top
// show by default.
type MetricsSnapshot struct {
	SampledAtUnix int64   `json:"sampled_at_unix" doc:"Server wall clock when the sample was taken (unix seconds)."`
	CPUPercent    float64 `json:"cpu_percent" doc:"Aggregate vCPU usage normalised to 0..100." minimum:"0" maximum:"100"`
	MemUsedMiB    uint64  `json:"mem_used_mib" doc:"Resident memory in MiB."`
	MemTotalMiB   uint64  `json:"mem_total_mib" doc:"Configured memory ceiling in MiB."`
	NetRxBps      uint64  `json:"net_rx_bps" doc:"Inbound bytes per second."`
	NetTxBps      uint64  `json:"net_tx_bps" doc:"Outbound bytes per second."`
	DiskReadBps   uint64  `json:"disk_read_bps" doc:"Disk read bytes per second."`
	DiskWriteBps  uint64  `json:"disk_write_bps" doc:"Disk write bytes per second."`
	UptimeSeconds uint64  `json:"uptime_seconds" doc:"Seconds since the last successful boot."`
	// Mock = true when the snapshot is synthesised by the webui
	// rather than read from the real weft-agent. The SPA shows a
	// "mock data" badge so operators don't mistake the curves for
	// truth. Cleared once a real GetMicroVMMetrics RPC lands.
	Mock bool `json:"mock" doc:"True when the snapshot is synthetic (no live metrics RPC available yet)."`
}

type vmMetricsOutput struct {
	Body MetricsSnapshot
}

func mountMicroVMMetricsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "vm-metrics",
		Method:      "GET",
		Path:        "/api/microvms/{name}/metrics",
		Summary:     "Read a microVM's live metrology snapshot",
		Description: "Single-shot metrology snapshot (CPU%, memory, net rx/tx, disk r/w, uptime). The SPA polls this every ~5 s ; the response is stateless. Returns synthetic data with mock=true while a real GetMicroVMMetrics RPC is not yet wired in weft-proto.",
		Tags:        []string{"microvms", "metrics"},
	}, func(ctx context.Context, in *vmProjectInput) (*vmMetricsOutput, error) {
		// We DON'T require live here : the dashboard needs a working
		// Metrics tab even in mock mode (dev / preview) so the UI is
		// exercised. When live is wired the synth still kicks in
		// because the wclient method returns Unimplemented today —
		// the future swap is a one-liner inside getOrSynthMetrics.
		var (
			snap *MetricsSnapshot
			err  error
		)
		if live != nil {
			snap, err = tryLiveMetrics(ctx, live, in.Name, in.Project)
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if snap == nil {
			synth := synthesiseMetrics(in.Name, time.Now())
			snap = &synth
		}
		return &vmMetricsOutput{Body: *snap}, nil
	})
}

// tryLiveMetrics calls the live client's GetMicroVMMetrics. Today the
// method always returns codes.Unimplemented (no proto RPC yet) ; the
// caller treats (nil, nil) as "fall back to synth". When the proto
// gains the RPC, this is the only function that changes.
func tryLiveMetrics(ctx context.Context, c *wclient.Client, name, project string) (*MetricsSnapshot, error) {
	info, err := c.GetMicroVMMetrics(ctx, name, project)
	if err != nil {
		if wclient.IsUnimplemented(err) {
			return nil, nil // fall through to synth
		}
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	return &MetricsSnapshot{
		SampledAtUnix: info.SampledAtUnix,
		CPUPercent:    info.CPUPercent,
		MemUsedMiB:    info.MemUsedMiB,
		MemTotalMiB:   info.MemTotalMiB,
		NetRxBps:      info.NetRxBps,
		NetTxBps:      info.NetTxBps,
		DiskReadBps:   info.DiskReadBps,
		DiskWriteBps:  info.DiskWriteBps,
		UptimeSeconds: info.UptimeSeconds,
		Mock:          false,
	}, nil
}

// synthesiseMetrics produces a plausible-looking sample for a given
// VM name and wall-clock time. The curves are deterministic w.r.t.
// (name, second-resolution time) so a refresh hitting the endpoint
// repeatedly yields a smooth time series instead of pure noise — the
// SPA's chart looks alive, not random.
//
// Shape per channel :
//   cpu       : 25% baseline + 30% slow sine (period 60 s) + per-VM offset → 5..85
//   mem_used  : 60% of mem_total baseline + 15% slow drift
//   net_rx/tx : 200..2_500_000 bytes/s, anti-correlated sine + phase offset
//   disk      : 100..800_000 bytes/s, decoupled from net so the curves don't overlap
//   uptime    : modulo a day so the badge cycles without sitting at 0
//
// The per-VM offset hashes the name through FNV-32 — different VMs
// open in different drawer tabs show different curves at the same
// second so it's clear the data is per-VM (even if synthetic).
func synthesiseMetrics(name string, now time.Time) MetricsSnapshot {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	offset := float64(h.Sum32()%360) * math.Pi / 180 // 0..2π
	t := float64(now.Unix())

	// CPU : 25% baseline + 30% sin(t/9.5 + offset). Clamp to 5..95.
	cpu := 25 + 30*math.Sin(t/9.5+offset)
	if cpu < 5 {
		cpu = 5
	}
	if cpu > 95 {
		cpu = 95
	}

	memTotal := uint64(1024) // 1 GiB stub ceiling
	memUsed := uint64(float64(memTotal) * (0.45 + 0.15*math.Sin(t/30+offset)))

	netBase := 400_000.0
	netRx := netBase * (1 + math.Sin(t/7+offset))
	netTx := netBase * (1 + math.Cos(t/11+offset))
	if netRx < 200 {
		netRx = 200
	}
	if netTx < 200 {
		netTx = 200
	}

	diskBase := 120_000.0
	diskR := diskBase * (1 + math.Sin(t/17+offset+1.3))
	diskW := diskBase * (1 + math.Cos(t/13+offset+0.7))
	if diskR < 100 {
		diskR = 100
	}
	if diskW < 100 {
		diskW = 100
	}

	const day = 24 * 3600
	uptime := uint64(now.Unix()) % day

	return MetricsSnapshot{
		SampledAtUnix: now.Unix(),
		CPUPercent:    math.Round(cpu*10) / 10,
		MemUsedMiB:    memUsed,
		MemTotalMiB:   memTotal,
		NetRxBps:      uint64(netRx),
		NetTxBps:      uint64(netTx),
		DiskReadBps:   uint64(diskR),
		DiskWriteBps:  uint64(diskW),
		UptimeSeconds: uptime,
		Mock:          true,
	}
}
