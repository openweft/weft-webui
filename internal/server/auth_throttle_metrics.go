// auth_throttle_metrics.go — prometheus collectors that scrape the
// per-IP failure budget live. Operators wire a Grafana alert like :
//
//	increase(weft_webui_auth_throttle_locked[5m]) > 0
//
// to detect a sustained brute-force burst.
//
// Two distinct metrics so a dashboard can chart "many tracked, few
// locked" (normal noise) vs. "tracked ~= locked" (active spray) :
//
//	weft_webui_auth_throttle_tracked  — IPs with at least 1 failure in the window
//	weft_webui_auth_throttle_locked   — subset of those whose count >= threshold
//
// Implemented as a Collector that reads the snapshot at scrape time
// rather than a Gauge that's set on every mutation, so the values
// are always live with no risk of drift if a counter update path is
// added later without ticking the gauge.

package server

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// authThrottleOnce guards the prometheus registration so re-running
// buildHandler (e.g. one User + one Infra portal in the same
// process) doesn't double-register the collector — prometheus
// panics on dup descriptors.
var authThrottleOnce sync.Once

type throttleCollector struct {
	tracked *prometheus.Desc
	locked  *prometheus.Desc
}

func newThrottleCollector() *throttleCollector {
	return &throttleCollector{
		tracked: prometheus.NewDesc(
			"weft_webui_auth_throttle_tracked",
			"Number of IPs with at least one auth-callback failure in the current window.",
			nil, nil,
		),
		locked: prometheus.NewDesc(
			"weft_webui_auth_throttle_locked",
			"Number of IPs currently locked out by the auth-callback throttle (failures >= threshold).",
			nil, nil,
		),
	}
}

func (c *throttleCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.tracked
	ch <- c.locked
}

func (c *throttleCollector) Collect(ch chan<- prometheus.Metric) {
	snap := authThrottle.snapshot()
	now := authThrottle.now()
	var tracked, locked float64
	for _, e := range snap {
		// Honour the same window-expiry logic gate() uses : a
		// stale entry past its window doesn't count as tracked.
		if now.Sub(e.firstHit) > authThrottle.window {
			continue
		}
		tracked++
		if e.count >= authThrottle.threshold {
			locked++
		}
	}
	ch <- prometheus.MustNewConstMetric(c.tracked, prometheus.GaugeValue, tracked)
	ch <- prometheus.MustNewConstMetric(c.locked, prometheus.GaugeValue, locked)
}

// RegisterAuthThrottleMetrics adds the per-IP throttle gauges to the
// telemetry registry. Called once from server.New() / NewAdmin() if
// metrics is wired ; the collector reads authThrottle live on every
// scrape.
func RegisterAuthThrottleMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	authThrottleOnce.Do(func() {
		reg.MustRegister(newThrottleCollector())
	})
}
