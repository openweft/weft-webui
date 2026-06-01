// mock_persistence_test.go — round-trip coverage for the shared
// persistence helper + each per-resource flush hook (DNS, security,
// scripts). The inventory hook has its own dedicated suite in
// inventory_mock_test.go ; this file only adds the others.

package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/openweft/weft-webui/internal/wclient"
)

// ---- DNS ------------------------------------------------------

func withTempDNSPath(t *testing.T) string {
	t.Helper()
	prev := dnsPath
	zoneRows := append([]map[string]any(nil), resourceByID["dns-zones"].Rows...)
	recRows := append([]map[string]any(nil), resourceByID["dns-records"].Rows...)
	t.Cleanup(func() {
		dnsMockMu.Lock()
		dnsPath = prev
		resourceByID["dns-zones"].Rows = zoneRows
		resourceByID["dns-records"].Rows = recRows
		dnsMockMu.Unlock()
	})
	return filepath.Join(t.TempDir(), "dns.json")
}

func TestDNSPersistence_FlushOnUpdate(t *testing.T) {
	path := withTempDNSPath(t)
	SetDNSPath(path)
	// Pick the first seeded zone — update mutates it + flushes.
	if len(resourceByID["dns-zones"].Rows) == 0 {
		t.Skip("seed has no dns zones to update")
	}
	uuid := str(resourceByID["dns-zones"].Rows[0]["uuid"])
	ok := updateDNSZoneRow(uuid, func(row map[string]any) {
		row["ttl_default"] = 9999
	})
	if !ok {
		t.Fatal("updateDNSZoneRow returned false")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dns file not written: %v", err)
	}
	var snap dnsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Version != 1 {
		t.Errorf("Version: want 1, got %d", snap.Version)
	}
	// Find our row in the snapshot and check the patched ttl_default.
	found := false
	for _, row := range snap.Zones {
		if str(row["uuid"]) == uuid {
			if toInt(row["ttl_default"]) != 9999 {
				t.Errorf("ttl_default not persisted: %v", row["ttl_default"])
			}
			found = true
		}
	}
	if !found {
		t.Errorf("zone %s missing from snapshot", uuid)
	}
}

func TestDNSPersistence_DeleteFlushes(t *testing.T) {
	path := withTempDNSPath(t)
	SetDNSPath(path)
	if len(resourceByID["dns-records"].Rows) == 0 {
		t.Skip("seed has no dns records")
	}
	uuid := str(resourceByID["dns-records"].Rows[0]["uuid"])
	if !deleteDNSRecordRow(uuid) {
		t.Fatal("delete returned false")
	}
	b, _ := os.ReadFile(path)
	var snap dnsSnapshot
	_ = json.Unmarshal(b, &snap)
	for _, row := range snap.Records {
		if str(row["uuid"]) == uuid {
			t.Errorf("record %s still present in snapshot after delete", uuid)
		}
	}
}

// ---- Security groups ------------------------------------------

func withTempSecurityPath(t *testing.T) string {
	t.Helper()
	prevPath := securityPath
	groupRows := append([]map[string]any(nil), resourceByID["security-groups"].Rows...)
	prevRules := make(map[string][]wclient.SecurityRule, len(sgRules))
	for k, v := range sgRules {
		prevRules[k] = v
	}
	t.Cleanup(func() {
		sgRulesMu.Lock()
		securityPath = prevPath
		resourceByID["security-groups"].Rows = groupRows
		sgRules = prevRules
		sgRulesMu.Unlock()
	})
	return filepath.Join(t.TempDir(), "security.json")
}

func TestSecurityPersistence_SetMockSGRulesFlushes(t *testing.T) {
	path := withTempSecurityPath(t)
	SetSecurityPath(path)
	if len(resourceByID["security-groups"].Rows) == 0 {
		t.Skip("seed has no security groups")
	}
	uuid := str(resourceByID["security-groups"].Rows[0]["uuid"])
	setMockSGRules(uuid, []wclient.SecurityRule{
		{Direction: "ingress", Protocol: "tcp", PortMin: 22, PortMax: 22},
	})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("security file not written: %v", err)
	}
	var snap securitySnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := len(snap.Rules[uuid]); got != 1 {
		t.Errorf("rules for %s in snapshot: want 1, got %d", uuid, got)
	}
}

func TestSecurityPersistence_DeleteSGClearsRulesAndRow(t *testing.T) {
	path := withTempSecurityPath(t)
	SetSecurityPath(path)
	if len(resourceByID["security-groups"].Rows) == 0 {
		t.Skip("seed has no security groups")
	}
	uuid := str(resourceByID["security-groups"].Rows[0]["uuid"])
	setMockSGRules(uuid, []wclient.SecurityRule{
		{Direction: "ingress", Protocol: "tcp", PortMin: 80, PortMax: 80},
	})
	if !deleteMockSecurityGroup(uuid) {
		t.Fatal("delete returned false")
	}
	b, _ := os.ReadFile(path)
	var snap securitySnapshot
	_ = json.Unmarshal(b, &snap)
	if _, ok := snap.Rules[uuid]; ok {
		t.Errorf("rules for %s should be cleared after delete", uuid)
	}
	for _, row := range snap.Groups {
		if str(row["uuid"]) == uuid {
			t.Errorf("group row %s still present", uuid)
		}
	}
}

// ---- Scripts catalogue ----------------------------------------

func TestScriptsPersistence_SetFlushes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scripts.json")

	mem, ok := scriptsCatalogue.(*memScriptCatalogue)
	if !ok {
		t.Skip("scripts catalogue is live, persistence is weft-network's job")
	}
	mem.mu.Lock()
	prevPath := mem.path
	prevScripts := append([]Script(nil), mem.scripts...)
	mem.mu.Unlock()
	t.Cleanup(func() {
		mem.mu.Lock()
		mem.path = prevPath
		mem.scripts = prevScripts
		mem.mu.Unlock()
	})

	SetScriptsPath(path)

	if err := scriptsCatalogue.Set(context.Background(), Script{
		Name: "test-script", Description: "smoke", Body: "#!/bin/sh\necho ok\n",
	}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scripts file not written: %v", err)
	}
	var snap scriptsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Version != 1 {
		t.Errorf("Version: want 1, got %d", snap.Version)
	}
	found := false
	for _, s := range snap.Scripts {
		if s.Name == "test-script" {
			found = true
		}
	}
	if !found {
		t.Errorf("test-script missing from snapshot ; scripts in snap=%d", len(snap.Scripts))
	}

	// Delete also flushes ; the row should disappear from the file.
	if err := scriptsCatalogue.Delete(context.Background(), "test-script"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	_ = json.Unmarshal(b, &snap)
	for _, s := range snap.Scripts {
		if s.Name == "test-script" {
			t.Errorf("test-script should be gone after Delete")
		}
	}
}

// ---- Shared atomicWriteJSON ----------------------------------

func TestAtomicWriteJSON_EmptyPathIsNoop(t *testing.T) {
	atomicWriteJSON("", struct{}{})
	// No way to assert "did nothing" beyond "didn't panic" — the
	// branch was the regression risk. Test exists as a guard.
}

func TestAtomicWriteJSON_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "file.json")
	atomicWriteJSON(nested, map[string]string{"k": "v"})
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("nested file not written: %v", err)
	}
}
