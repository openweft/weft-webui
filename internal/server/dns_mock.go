// dns_mock.go — mock-friendly editing layer for DNS zones + records.
//
// The static seed rows in resources.go ship without uuids ; live
// wiring stamps them through weft-network's CreateDNSZone /
// CreateDNSRecord. In mock mode the dashboard needs stable handles
// to drive Edit + Delete, so init() stamps synthetic uuids on each
// row and this file exposes find / update helpers keyed by them.
//
// Once weft-network is live, the dashboard's PUT calls will route
// through liveNet ; the mock layer falls back to the row map by uuid
// so the affordance survives a partial controller rollout.

package server

import (
	"crypto/sha1"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
)

var dnsMockMu sync.Mutex

// dnsPath, when non-empty, is the JSON file dns-zones + dns-records
// rows are rehydrated from at boot + flushed back to after every
// mutation. Set by SetDNSPath() from server.New().
var dnsPath string

func init() {
	stampDNSUUIDs()
}

// SetDNSPath configures the on-disk persistence for the mock-layer
// DNS rows. Same shape + opt-in semantics as SetInventoryPath.
func SetDNSPath(p string) {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	dnsPath = strings.TrimSpace(p)
	if dnsPath == "" {
		return
	}
	if err := loadDNSFromDiskLocked(); err != nil {
		slog.Warn("dns: load failed, keeping in-memory seed",
			"path", dnsPath, "err", err.Error())
	}
}

type dnsSnapshot struct {
	Version int              `json:"version"`
	Zones   []map[string]any `json:"zones"`
	Records []map[string]any `json:"records"`
}

func loadDNSFromDiskLocked() error {
	var snap dnsSnapshot
	loaded, err := readJSON(dnsPath, &snap)
	if err != nil || !loaded {
		return err
	}
	if z, ok := resourceByID["dns-zones"]; ok {
		z.Rows = snap.Zones
	}
	if r, ok := resourceByID["dns-records"]; ok {
		r.Rows = snap.Records
	}
	return nil
}

func flushDNSLocked() {
	if dnsPath == "" {
		return
	}
	snap := dnsSnapshot{Version: 1}
	if z, ok := resourceByID["dns-zones"]; ok {
		snap.Zones = z.Rows
	}
	if r, ok := resourceByID["dns-records"]; ok {
		snap.Records = r.Rows
	}
	atomicWriteJSON(dnsPath, snap)
}

// deleteDNSZoneRow removes the zone with the given uuid from the
// seed. Idempotent (missing = false). Called from the mock fallback
// path in api_networking.go's delete-dns-zone handler.
func deleteDNSZoneRow(uuid string) bool {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	z, ok := resourceByID["dns-zones"]
	if !ok {
		return false
	}
	for i, row := range z.Rows {
		if str(row["uuid"]) == uuid {
			z.Rows = append(z.Rows[:i], z.Rows[i+1:]...)
			flushDNSLocked()
			return true
		}
	}
	return false
}

func deleteDNSRecordRow(uuid string) bool {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	r, ok := resourceByID["dns-records"]
	if !ok {
		return false
	}
	for i, row := range r.Rows {
		if str(row["uuid"]) == uuid {
			r.Rows = append(r.Rows[:i], r.Rows[i+1:]...)
			flushDNSLocked()
			return true
		}
	}
	return false
}

// stampDNSUUIDs walks every row in dns-zones + dns-records and
// inserts a uuid field if missing. The uuid is content-derived
// (sha1 over name + zone + type + value) so tests + restarts see
// the same id across runs.
func stampDNSUUIDs() {
	if z, ok := resourceByID["dns-zones"]; ok {
		for i, row := range z.Rows {
			if _, has := row["uuid"]; has {
				continue
			}
			z.Rows[i]["uuid"] = mockUUID("dns-zone",
				str(row["name"]))
		}
	}
	if r, ok := resourceByID["dns-records"]; ok {
		for i, row := range r.Rows {
			if _, has := row["uuid"]; has {
				continue
			}
			r.Rows[i]["uuid"] = mockUUID("dns-record",
				str(row["zone"]), str(row["name"]),
				str(row["type"]), str(row["value"]))
		}
	}
}

// mockUUID returns a 16-char hex slug from sha1(kind, parts...).
// Stable across runs ; collision-resistant enough for the seed scale.
func mockUUID(kind string, parts ...string) string {
	h := sha1.New()
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// toInt coerces row values to int regardless of whether they were
// stored as int (literal seed) or float64 (JSON unmarshal).
func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

// boolField returns true if the row value is true, with "missing or
// unrecognised" defaulting to true. The enabled-state convention
// across mock rows is "enabled unless explicitly set false", so the
// helper treats absent fields as a permissive default.
func boolField(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case nil:
		return true
	}
	return true
}

// ---- find / update helpers --------------------------------------

func findDNSZoneByUUID(uuid string) (map[string]any, int, bool) {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	z, ok := resourceByID["dns-zones"]
	if !ok {
		return nil, -1, false
	}
	for i, row := range z.Rows {
		if str(row["uuid"]) == uuid {
			return row, i, true
		}
	}
	return nil, -1, false
}

func updateDNSZoneRow(uuid string, patch func(map[string]any)) bool {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	z, ok := resourceByID["dns-zones"]
	if !ok {
		return false
	}
	for _, row := range z.Rows {
		if str(row["uuid"]) == uuid {
			patch(row)
			flushDNSLocked()
			return true
		}
	}
	return false
}

func findDNSRecordByUUID(uuid string) (map[string]any, bool) {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	r, ok := resourceByID["dns-records"]
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

func updateDNSRecordRow(uuid string, patch func(map[string]any)) bool {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	r, ok := resourceByID["dns-records"]
	if !ok {
		return false
	}
	for _, row := range r.Rows {
		if str(row["uuid"]) == uuid {
			patch(row)
			flushDNSLocked()
			return true
		}
	}
	return false
}

// appendDNSZoneRow inserts a new zone row + flushes the on-disk
// snapshot when persistence is wired up. Used by the live-first
// path in api_networking.go's create-dns-zone handler to mirror a
// successful daemon write into the dashboard's cached view.
func appendDNSZoneRow(row map[string]any) {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	if z, ok := resourceByID["dns-zones"]; ok {
		z.Rows = append(z.Rows, row)
	}
	flushDNSLocked()
}

// appendDNSRecordRow inserts a new record row + flushes the on-disk
// snapshot when persistence is wired up. Used by the live-first
// path in api_networking.go's create-dns-record handler.
func appendDNSRecordRow(row map[string]any) {
	dnsMockMu.Lock()
	defer dnsMockMu.Unlock()
	if r, ok := resourceByID["dns-records"]; ok {
		r.Rows = append(r.Rows, row)
	}
	flushDNSLocked()
}
