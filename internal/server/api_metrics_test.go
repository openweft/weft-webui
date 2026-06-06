// api_metrics_test.go — covers the synthetic-data fallback the
// /api/microvms/{name}/metrics endpoint serves while a real
// GetMicroVMMetrics RPC is missing. Once the proto bump lands and
// tryLiveMetrics returns a real snapshot, these cases stay green (they
// only assert the synth shape) and add separate live-path coverage.

package server

import (
	"testing"
	"time"
)

func TestSynthesiseMetrics_FieldsInBounds(t *testing.T) {
	snap := synthesiseMetrics("vm-alpha", time.Unix(1_700_000_000, 0))

	if snap.CPUPercent < 0 || snap.CPUPercent > 100 {
		t.Errorf("cpu_percent %v out of [0,100]", snap.CPUPercent)
	}
	if snap.MemTotalMiB == 0 {
		t.Error("mem_total_mib is zero ; the SPA's % used calc would NaN")
	}
	if snap.MemUsedMiB > snap.MemTotalMiB {
		t.Errorf("mem_used %d > mem_total %d", snap.MemUsedMiB, snap.MemTotalMiB)
	}
	if snap.NetRxBps == 0 || snap.NetTxBps == 0 {
		t.Error("net_*_bps is zero ; baseline floor should prevent dead-flat curves")
	}
	if snap.DiskReadBps == 0 || snap.DiskWriteBps == 0 {
		t.Error("disk_*_bps is zero ; baseline floor should prevent dead-flat curves")
	}
	if !snap.Mock {
		t.Error("synth snapshot must have Mock=true so the UI shows the badge")
	}
	if snap.SampledAtUnix != 1_700_000_000 {
		t.Errorf("sampled_at_unix %d ; want %d", snap.SampledAtUnix, 1_700_000_000)
	}
}

func TestSynthesiseMetrics_DeterministicGivenInputs(t *testing.T) {
	// Same (name, time) → identical output. Property the SPA relies on
	// when it polls quickly : two requests in the same second should
	// not jitter the chart.
	at := time.Unix(1_700_000_042, 0)
	a := synthesiseMetrics("vm-bravo", at)
	b := synthesiseMetrics("vm-bravo", at)
	if a != b {
		t.Errorf("synth not deterministic : a=%+v b=%+v", a, b)
	}
}

func TestSynthesiseMetrics_DiffersAcrossVMs(t *testing.T) {
	// Different VM names at the same wall-clock should produce
	// different CPU curves — the per-name FNV offset is the only knob
	// that decorrelates them, so this guards against accidentally
	// dropping it.
	at := time.Unix(1_700_000_100, 0)
	a := synthesiseMetrics("vm-alpha", at)
	b := synthesiseMetrics("vm-bravo", at)
	if a.CPUPercent == b.CPUPercent && a.NetRxBps == b.NetRxBps {
		t.Errorf("synth produced identical curves for two distinct VMs : a=%+v b=%+v", a, b)
	}
}

func TestSynthesiseMetrics_VariesOverTime(t *testing.T) {
	// Same VM at two different times → different sample. Catches a
	// regression where the time arg accidentally gets dropped from the
	// hash → flat-line chart.
	a := synthesiseMetrics("vm-charlie", time.Unix(1_700_000_000, 0))
	b := synthesiseMetrics("vm-charlie", time.Unix(1_700_000_020, 0))
	if a == b {
		t.Errorf("synth produced identical samples 20s apart : %+v", a)
	}
}
