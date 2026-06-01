// inventory_mock.go — find / add / update / delete helpers for the
// AZ / Rack / Host rows that back the inventory tree + map. Same
// idea as dns_mock.go : a mutex guards in-place mutation of the
// rows owned by resourceByID, and uuids are stamped at init so
// the dashboard has stable handles to drive Edit + Delete.
//
// In live mode these resources will sync from etcd via weft-network
// (RegisterAZ, RegisterRack, RegisterHost RPCs). The mock layer
// stays in place as a fallback so superadmins can still adjust
// the catalogue even when the controller is partially rolled out.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var inventoryMu sync.Mutex

// inventoryPath, when non-empty, is the JSON file the AZ / Rack /
// Host rows are rehydrated from at boot and flushed back to after
// every mutation. Set by SetInventoryPath() from server.New().
var inventoryPath string

func init() {
	stampInventoryUUIDs()
}

// SetInventoryPath configures the on-disk persistence target. Called
// once during server construction from cfg.InventoryPath. Empty path
// disables persistence (the in-memory seed survives restart, operator
// changes don't — dev mode only).
//
// If the file exists at SetInventoryPath() time, its contents replace
// the seeded rows in resourceByID. Missing file = first run, the
// seed wins and gets flushed on the first mutation.
func SetInventoryPath(p string) {
	inventoryMu.Lock()
	inventoryPath = strings.TrimSpace(p)
	inventoryMu.Unlock()
	if inventoryPath == "" {
		return
	}
	if err := loadInventoryFromDiskLocked(); err != nil {
		// Don't fatal — a corrupt or partially-written file shouldn't
		// keep the dashboard from booting. The operator can roll back
		// the file by hand ; meanwhile the seed remains visible.
		slog.Warn("inventory: load failed, keeping in-memory seed",
			"path", inventoryPath, "err", err.Error())
	}
}

// inventorySnapshot is the JSON shape on disk. One top-level key per
// resource so the file is human-diffable. Version = forward-compat
// marker for the eventual etcd migration.
type inventorySnapshot struct {
	Version int              `json:"version"`
	AZs     []map[string]any `json:"azs"`
	Racks   []map[string]any `json:"racks"`
	Hosts   []map[string]any `json:"hosts"`
}

// loadInventoryFromDiskLocked is callable only with inventoryMu held
// (called from SetInventoryPath right after taking the lock).
func loadInventoryFromDiskLocked() error {
	b, err := os.ReadFile(inventoryPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var snap inventorySnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if a, ok := resourceByID["azs"]; ok {
		a.Rows = snap.AZs
	}
	if r, ok := resourceByID["racks"]; ok {
		r.Rows = snap.Racks
	}
	if h, ok := resourceByID["hosts"]; ok {
		h.Rows = snap.Hosts
	}
	return nil
}

// flushInventoryLocked writes the current rows back to inventoryPath.
// Atomic : write to <path>.tmp + rename, so a partial write never
// leaves the file unparseable. No-op when persistence is disabled.
//
// Must be called with inventoryMu held.
func flushInventoryLocked() {
	if inventoryPath == "" {
		return
	}
	snap := inventorySnapshot{Version: 1}
	if a, ok := resourceByID["azs"]; ok {
		snap.AZs = a.Rows
	}
	if r, ok := resourceByID["racks"]; ok {
		snap.Racks = r.Rows
	}
	if h, ok := resourceByID["hosts"]; ok {
		snap.Hosts = h.Rows
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		slog.Error("inventory: marshal failed", "err", err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(inventoryPath), 0o755); err != nil {
		slog.Error("inventory: mkdir failed", "path", inventoryPath, "err", err.Error())
		return
	}
	tmp := inventoryPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		slog.Error("inventory: write tmp failed", "path", tmp, "err", err.Error())
		return
	}
	if err := os.Rename(tmp, inventoryPath); err != nil {
		slog.Error("inventory: rename failed", "from", tmp, "to", inventoryPath, "err", err.Error())
		// Best-effort cleanup of the orphan tmp ; ignore the error.
		_ = os.Remove(tmp)
		return
	}
}

// stampInventoryUUIDs ensures every AZ / Rack / Host row has a uuid.
// Seed data already includes uuids for AZs + Racks ; hosts in the
// seed have only `name`. Stamp a content-derived uuid where missing
// so the API + UI can address every row by id.
func stampInventoryUUIDs() {
	if a, ok := resourceByID["azs"]; ok {
		for i, row := range a.Rows {
			if _, has := row["uuid"]; !has {
				a.Rows[i]["uuid"] = mockUUID("az", str(row["code"]))
			}
		}
	}
	if r, ok := resourceByID["racks"]; ok {
		for i, row := range r.Rows {
			if _, has := row["uuid"]; !has {
				r.Rows[i]["uuid"] = mockUUID("rack", str(row["az"]), str(row["code"]))
			}
		}
	}
	if h, ok := resourceByID["hosts"]; ok {
		for i, row := range h.Rows {
			if _, has := row["uuid"]; !has {
				h.Rows[i]["uuid"] = mockUUID("host", str(row["az"]), str(row["rack"]), str(row["name"]))
			}
		}
	}
}

// ---- AZ helpers ------------------------------------------------

func findAZByUUID(uuid string) (map[string]any, int, bool) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	a, ok := resourceByID["azs"]
	if !ok {
		return nil, -1, false
	}
	for i, row := range a.Rows {
		if str(row["uuid"]) == uuid {
			return row, i, true
		}
	}
	return nil, -1, false
}

func azCodeExists(code string) bool {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	a, ok := resourceByID["azs"]
	if !ok {
		return false
	}
	for _, row := range a.Rows {
		if str(row["code"]) == code {
			return true
		}
	}
	return false
}

func appendAZ(row map[string]any) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	if a, ok := resourceByID["azs"]; ok {
		a.Rows = append(a.Rows, row)
	}
	flushInventoryLocked()
}

func updateAZRow(uuid string, patch func(map[string]any)) bool {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	a, ok := resourceByID["azs"]
	if !ok {
		return false
	}
	for _, row := range a.Rows {
		if str(row["uuid"]) == uuid {
			patch(row)
			flushInventoryLocked()
			return true
		}
	}
	return false
}

// deleteAZRow drops the AZ + cascades to its racks + hosts. Returns
// the deleted-row counts so the caller can report what was removed.
func deleteAZRow(uuid string) (azDeleted, rackCount, hostCount int) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	a, ok := resourceByID["azs"]
	if !ok {
		return 0, 0, 0
	}
	var azCode string
	for i, row := range a.Rows {
		if str(row["uuid"]) == uuid {
			azCode = str(row["code"])
			a.Rows = append(a.Rows[:i], a.Rows[i+1:]...)
			azDeleted = 1
			break
		}
	}
	if azDeleted == 0 || azCode == "" {
		return
	}
	if r, ok := resourceByID["racks"]; ok {
		filtered := r.Rows[:0]
		for _, row := range r.Rows {
			if str(row["az"]) == azCode {
				rackCount++
				continue
			}
			filtered = append(filtered, row)
		}
		r.Rows = filtered
	}
	if h, ok := resourceByID["hosts"]; ok {
		filtered := h.Rows[:0]
		for _, row := range h.Rows {
			if str(row["az"]) == azCode {
				hostCount++
				continue
			}
			filtered = append(filtered, row)
		}
		h.Rows = filtered
	}
	flushInventoryLocked()
	return
}

// ---- Rack helpers ----------------------------------------------

func findRackByUUID(uuid string) (map[string]any, bool) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	r, ok := resourceByID["racks"]
	if !ok {
		return nil, false
	}
	for _, row := range r.Rows {
		if str(row["uuid"]) == uuid {
			return row, true
		}
	}
	return nil, false
}

func rackCodeExistsInAZ(az, code string) bool {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	r, ok := resourceByID["racks"]
	if !ok {
		return false
	}
	for _, row := range r.Rows {
		if str(row["az"]) == az && str(row["code"]) == code {
			return true
		}
	}
	return false
}

func appendRack(row map[string]any) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	if r, ok := resourceByID["racks"]; ok {
		r.Rows = append(r.Rows, row)
	}
	flushInventoryLocked()
}

func updateRackRow(uuid string, patch func(map[string]any)) bool {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	r, ok := resourceByID["racks"]
	if !ok {
		return false
	}
	for _, row := range r.Rows {
		if str(row["uuid"]) == uuid {
			patch(row)
			flushInventoryLocked()
			return true
		}
	}
	return false
}

// deleteRackRow drops the rack + cascades to its hosts.
func deleteRackRow(uuid string) (rackDeleted, hostCount int) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	r, ok := resourceByID["racks"]
	if !ok {
		return 0, 0
	}
	var az, code string
	for i, row := range r.Rows {
		if str(row["uuid"]) == uuid {
			az = str(row["az"])
			code = str(row["code"])
			r.Rows = append(r.Rows[:i], r.Rows[i+1:]...)
			rackDeleted = 1
			break
		}
	}
	if rackDeleted == 0 {
		return
	}
	if h, ok := resourceByID["hosts"]; ok {
		filtered := h.Rows[:0]
		for _, row := range h.Rows {
			if str(row["az"]) == az && str(row["rack"]) == code {
				hostCount++
				continue
			}
			filtered = append(filtered, row)
		}
		h.Rows = filtered
	}
	flushInventoryLocked()
	return
}

// ---- Host helpers ----------------------------------------------

func findHostByUUID(uuid string) (map[string]any, bool) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	h, ok := resourceByID["hosts"]
	if !ok {
		return nil, false
	}
	for _, row := range h.Rows {
		if str(row["uuid"]) == uuid {
			return row, true
		}
	}
	return nil, false
}

func hostNameExists(name string) bool {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	h, ok := resourceByID["hosts"]
	if !ok {
		return false
	}
	for _, row := range h.Rows {
		if strings.EqualFold(str(row["name"]), name) {
			return true
		}
	}
	return false
}

func appendHost(row map[string]any) {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	if h, ok := resourceByID["hosts"]; ok {
		h.Rows = append(h.Rows, row)
	}
	flushInventoryLocked()
}

func updateHostRow(uuid string, patch func(map[string]any)) bool {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	h, ok := resourceByID["hosts"]
	if !ok {
		return false
	}
	for _, row := range h.Rows {
		if str(row["uuid"]) == uuid {
			patch(row)
			flushInventoryLocked()
			return true
		}
	}
	return false
}

func deleteHostRow(uuid string) bool {
	inventoryMu.Lock()
	defer inventoryMu.Unlock()
	h, ok := resourceByID["hosts"]
	if !ok {
		return false
	}
	for i, row := range h.Rows {
		if str(row["uuid"]) == uuid {
			h.Rows = append(h.Rows[:i], h.Rows[i+1:]...)
			flushInventoryLocked()
			return true
		}
	}
	return false
}
