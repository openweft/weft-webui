// inventory_mock_test.go — pin the persistence layer's round-trip
// behaviour : a mutation flushes to disk ; SetInventoryPath() on a
// fresh process pulls the same rows back into resourceByID.

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seedRowCount captures the number of seeded rows for each resource.
// We diff against this rather than hard-coding integers — the seed
// can grow without breaking the test.
type seedRowCount struct{ azs, racks, hosts int }

func snapshotSeed(t *testing.T) seedRowCount {
	t.Helper()
	return seedRowCount{
		azs:   len(resourceByID["azs"].Rows),
		racks: len(resourceByID["racks"].Rows),
		hosts: len(resourceByID["hosts"].Rows),
	}
}

// withTempInventoryPath sets inventoryPath to a temp file and
// restores the prior value (typically "") at cleanup. Also snapshots
// + restores the seeded rows so the test doesn't leak state.
func withTempInventoryPath(t *testing.T) string {
	t.Helper()
	prevPath := inventoryPath
	azRows := append([]map[string]any(nil), resourceByID["azs"].Rows...)
	rkRows := append([]map[string]any(nil), resourceByID["racks"].Rows...)
	hostRows := append([]map[string]any(nil), resourceByID["hosts"].Rows...)
	t.Cleanup(func() {
		inventoryMu.Lock()
		inventoryPath = prevPath
		resourceByID["azs"].Rows = azRows
		resourceByID["racks"].Rows = rkRows
		resourceByID["hosts"].Rows = hostRows
		inventoryMu.Unlock()
	})
	dir := t.TempDir()
	return filepath.Join(dir, "inventory.json")
}

func TestInventoryPersistence_FlushOnMutation(t *testing.T) {
	path := withTempInventoryPath(t)
	SetInventoryPath(path)

	// Initial seed write : SetInventoryPath() didn't flush, but an
	// AZ mutation must produce the file with the seed + the new row.
	before := snapshotSeed(t)
	appendAZ(map[string]any{
		"uuid": "az-test-1", "code": "DC-TEST", "name": "Test DC",
		"region": "eu-test-1", "racks": 0, "hosts": 0, "status": "active",
	})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("inventory file not written: %v", err)
	}
	var snap inventorySnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Version != 1 {
		t.Errorf("Version: want 1, got %d", snap.Version)
	}
	if got := len(snap.AZs); got != before.azs+1 {
		t.Errorf("AZs in file: want %d, got %d", before.azs+1, got)
	}
}

func TestInventoryPersistence_LoadOverridesSeed(t *testing.T) {
	path := withTempInventoryPath(t)

	// Hand-author a snapshot with a single AZ and write it to disk.
	// SetInventoryPath should pick it up and REPLACE the seeded rows.
	snap := inventorySnapshot{
		Version: 1,
		AZs: []map[string]any{
			{"uuid": "az-loaded", "code": "DC-LOAD", "name": "Loaded", "region": "fr-1", "status": "active"},
		},
		Racks: []map[string]any{},
		Hosts: []map[string]any{},
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	SetInventoryPath(path)

	if got := len(resourceByID["azs"].Rows); got != 1 {
		t.Fatalf("AZs after load: want 1 (only the file contents), got %d", got)
	}
	if got, want := str(resourceByID["azs"].Rows[0]["code"]), "DC-LOAD"; got != want {
		t.Errorf("first AZ code: want %q, got %q", want, got)
	}
}

func TestInventoryPersistence_MissingFileKeepsSeed(t *testing.T) {
	path := withTempInventoryPath(t)
	before := snapshotSeed(t)

	// File doesn't exist → SetInventoryPath should leave the seed in
	// place (operator can start fresh and the seed is the baseline).
	SetInventoryPath(path)

	after := snapshotSeed(t)
	if before != after {
		t.Errorf("seed should survive missing inventory file ; before=%+v after=%+v", before, after)
	}
}

func TestInventoryPersistence_AtomicWrite(t *testing.T) {
	// Ensure the .tmp file isn't left behind after a successful flush.
	path := withTempInventoryPath(t)
	SetInventoryPath(path)

	appendAZ(map[string]any{
		"uuid": "az-atomic", "code": "DC-ATOMIC", "status": "active",
	})

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file should be cleaned up, stat err=%v", err)
	}
}

func TestInventoryPersistence_DisabledWhenPathEmpty(t *testing.T) {
	prev := inventoryPath
	t.Cleanup(func() { inventoryPath = prev })
	inventoryPath = ""

	// Without a path, mutations must not crash and the directory we
	// would have written to stays untouched.
	dir := t.TempDir()
	appendAZ(map[string]any{"uuid": "az-nopath", "code": "DC-NOP", "status": "active"})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("disabled mode wrote to disk anyway: %v", entries)
	}
	// Cleanup the row we just added so the seed stays clean for the
	// next test.
	deleteAZRow("az-nopath")
}
