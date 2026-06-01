// api_misc_readyz_test.go — exercises the enriched /api/readyz probe.
// The endpoint must :
//   1) return 200 with ok:true and mode=mock when no persistence is wired
//   2) include a probes map when state-file paths are configured
//   3) drop to 503 + ok:false + degraded when a probe fails

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReadyz_TrivialWhenNoPersistence(t *testing.T) {
	// Wipe any leftover persistence config from prior tests.
	prevInv, prevDNS, prevSec := inventoryPath, dnsPath, securityPath
	t.Cleanup(func() {
		inventoryPath, dnsPath, securityPath = prevInv, prevDNS, prevSec
	})
	inventoryPath, dnsPath, securityPath = "", "", ""

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body map[string]any
	code := hit(t, srv, "GET", "/api/readyz", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if _, has := body["probes"]; has {
		t.Errorf("probes should be absent when no path is configured ; got %v", body["probes"])
	}
}

func TestReadyz_OkWhenAllProbesPass(t *testing.T) {
	prev := inventoryPath
	t.Cleanup(func() { inventoryPath = prev })

	dir := t.TempDir()
	inventoryPath = filepath.Join(dir, "inventory.json")

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body map[string]any
	code := hit(t, srv, "GET", "/api/readyz", nil, &body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 ; body=%+v", code, body)
	}
	probes, _ := body["probes"].(map[string]any)
	if probes == nil || probes["inventory"] != "ok" {
		t.Errorf("probes[inventory] = %v, want ok", probes)
	}
}

func TestReadyz_DegradedWhenProbeFails(t *testing.T) {
	prev := inventoryPath
	t.Cleanup(func() { inventoryPath = prev })

	// Point inventoryPath at a path whose parent doesn't exist AND
	// can't be created (root-owned). Use a non-existent ancestor
	// inside a read-only-ish location : on macOS / Linux, /proc/0
	// can't be created. We use a path under /dev/null which is
	// guaranteed not a directory anywhere POSIX.
	inventoryPath = "/dev/null/inventory.json"

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body map[string]any
	code := hit(t, srv, "GET", "/api/readyz", nil, &body)
	if code != 503 {
		t.Fatalf("status = %d, want 503 ; body=%+v", code, body)
	}
	if body["ok"] != false {
		t.Errorf("ok = %v, want false", body["ok"])
	}
	deg, _ := body["degraded"].([]any)
	if len(deg) == 0 {
		t.Errorf("degraded should list the failing probe ; got %+v", body)
	}
}

func TestReadyz_ProbeRemovesTempDirsCleanly(t *testing.T) {
	// Guard against the probe leaving .readyz-probe-* directories
	// behind on disk — production replicas would accumulate them
	// across 100 probes/sec.
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		if got := probeWritable(filepath.Join(dir, "x.json")); got != "ok" {
			t.Fatalf("probeWritable: %s", got)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("probe left .tmp dir behind: %s", e.Name())
		}
	}
}
