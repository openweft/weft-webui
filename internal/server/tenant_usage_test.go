package server

import (
	"net/http/httptest"
	"testing"
)

// TestTenantUsage_RollUp confirms the usage endpoint joins microvms
// and volumes by project → tenant, sums cpu/mem/disk, and surfaces
// the cap alongside the totals. The seed in resources.go + tenants.go
// gives deterministic numbers for "acme" (projects team-alpha + team-beta) :
//
//   microvms : web-1 / web-2 (alpha, 2 CPU + 4 GiB + 10 disk each)
//              ci-job-7f3 (beta, 4 CPU + 8 GiB + 30 disk)
//   volumes  : pg-data 200 GiB (alpha), scratch-1 50 GiB (alpha),
//              cubefs-d0 500 GiB (beta)
//
// Total : 3 VMs, 8 CPU, 16 GiB RAM (16384 MiB / 1024), 3 volumes,
// 50 disk + 750 volume = 800 storage GiB.
func TestTenantUsage_RollUp(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var usage TenantUsageView
	if code := hit(t, srv, "GET", "/api/tenants/acme/usage", nil, &usage); code != 200 {
		t.Fatalf("usage status = %d", code)
	}
	if usage.Tenant != "acme" {
		t.Errorf("tenant = %q", usage.Tenant)
	}
	if usage.VMs != 3 {
		t.Errorf("vms = %d, want 3", usage.VMs)
	}
	if usage.CPUCores != 8 {
		t.Errorf("cpu_cores = %d, want 8", usage.CPUCores)
	}
	if usage.RAMGiB != 16 {
		t.Errorf("ram_gib = %d, want 16 (16384 MiB / 1024)", usage.RAMGiB)
	}
	if usage.Volumes != 3 {
		t.Errorf("volumes = %d, want 3", usage.Volumes)
	}
	if usage.StorageGiB != 800 {
		t.Errorf("storage_gib = %d, want 800 (50 disk + 750 vol)", usage.StorageGiB)
	}
	if usage.Cap == nil {
		t.Errorf("cap should be set for seeded tenant acme")
	} else if usage.Cap.VCPU == 0 {
		t.Errorf("cap.VCPU should be non-zero from seed")
	}
}

// TestTenantUsage_NotFoundUnknownTenant returns 404 for both
// non-members and unknown names (don't-acknowledge pattern).
func TestTenantUsage_NotFoundUnknownTenant(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	// Dev user is cluster admin → can see every tenant. So we probe
	// with a name that simply doesn't exist.
	if code := hit(t, srv, "GET", "/api/tenants/does-not-exist/usage", nil, nil); code != 404 {
		t.Errorf("unknown tenant status = %d, want 404", code)
	}
}
