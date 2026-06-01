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
	"strings"
	"sync"
)

var inventoryMu sync.Mutex

func init() {
	stampInventoryUUIDs()
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
			return true
		}
	}
	return false
}
