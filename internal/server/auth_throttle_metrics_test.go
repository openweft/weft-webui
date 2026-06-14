// auth_throttle_metrics_test.go — pin the per-IP throttle
// prometheus gauges : tracked counts every IP with a non-zero
// failure history, locked is the subset whose count is at or above
// threshold. Expired window entries don't count (Collect mirrors
// gate()'s skip logic).

package server

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestThrottleCollector_ZeroWhenEmpty(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	c := newThrottleCollector()
	if got := readGauge(t, c, "tracked"); got != 0 {
		t.Errorf("tracked = %v, want 0", got)
	}
	if got := readGauge(t, c, "locked"); got != 0 {
		t.Errorf("locked = %v, want 0", got)
	}
}

func TestThrottleCollector_CountsTrackedAndLocked(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	// 3 IPs : one below threshold (tracked-only), two at-or-above (both).
	for i := 0; i < 2; i++ {
		authThrottle.recordFailure("1.1.1.1")
	}
	for i := 0; i < 5; i++ {
		authThrottle.recordFailure("2.2.2.2")
	}
	for i := 0; i < 7; i++ {
		authThrottle.recordFailure("3.3.3.3")
	}

	c := newThrottleCollector()
	if got := readGauge(t, c, "tracked"); got != 3 {
		t.Errorf("tracked = %v, want 3", got)
	}
	if got := readGauge(t, c, "locked"); got != 2 {
		t.Errorf("locked = %v, want 2 (2.2.2.2 + 3.3.3.3)", got)
	}
}

func TestThrottleCollector_ExpiredEntryDoesNotCount(t *testing.T) {
	resetThrottle(t)
	t.Cleanup(func() { resetThrottle(t) })

	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	authThrottle.mu.Lock()
	authThrottle.nowFn = func() time.Time { return now }
	authThrottle.mu.Unlock()
	authThrottle.recordFailure("1.2.3.4")

	now = now.Add(6 * time.Minute) // past the 5-minute window

	c := newThrottleCollector()
	if got := readGauge(t, c, "tracked"); got != 0 {
		t.Errorf("tracked = %v, want 0 (entry expired)", got)
	}
}

func TestThrottleCollector_RegisterIsIdempotent(t *testing.T) {
	// Two consecutive RegisterAuthThrottleMetrics on the same
	// registry must not panic — the package-global sync.Once
	// guards the underlying MustRegister.
	reg := prometheus.NewRegistry()
	RegisterAuthThrottleMetrics(reg)
	RegisterAuthThrottleMetrics(reg) // second call : must be a no-op
	// success = no panic
}

// readGauge collects from c and returns the value of the gauge
// whose FQN matches `want` (substring on the descriptor). Uses the
// prometheus testutil helpers to avoid wrestling with the
// io_prometheus_client proto type by hand.
func readGauge(t *testing.T, c prometheus.Collector, want string) float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if !strings.Contains(mf.GetName(), want) {
			continue
		}
		ms := mf.GetMetric()
		if len(ms) == 0 {
			t.Fatalf("no samples for %q", want)
		}
		return ms[0].GetGauge().GetValue()
	}
	t.Fatalf("no metric matching %q", want)
	return 0
}

var _ = testutil.CollectAndCount // keep import live ; future tests likely
