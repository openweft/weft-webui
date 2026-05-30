// vm_metadata.go — per-VM property bag + UEFI NVRAM variables editor.
//
// Two related-but-distinct stores share this file :
//
//   Properties (host-set application-level annotations) :
//     - free-form key→value (operator-defined)
//     - each carries a GuestReadable flag — when true, the in-guest
//       weft-vm-agent is allowed to read the value via its NATS API
//       (subject /weft/vm/<uuid>/property/<key>). False = host-only
//       metadata (billing tags, security labels, …) the guest never
//       sees.
//   UEFI variables (firmware NVRAM) :
//     - keyed by (namespace GUID, name) — the OVMF/EDK2 wire shape
//     - value carried as hex (operator-friendly representation of
//       what's an arbitrary byte blob)
//     - attributes are the standard UEFI flag set : NonVolatile,
//       BootServiceAccess, RuntimeAccess, …
//
// Both stores are in-memory mocks today. Once weft-agent grows the
// matching RPCs (`SetVMProperty` / `ListVMProperties` / `SetUEFIVar`
// / `ListUEFIVars`) and the in-guest weft-vm-agent learns the
// property subject, this binary becomes a thin proxy — the wire shape
// stays.
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---- Properties --------------------------------------------------

// VMProperty is one application-level annotation on a microVM. Value
// is always a string ; structured data is the caller's serialisation
// choice (JSON / YAML / plain text — opaque to this layer).
type VMProperty struct {
	Key           string `json:"key"`
	Value         string `json:"value"`
	GuestReadable bool   `json:"guest_readable"`
	UpdatedAt     string `json:"updated_at"`
}

var (
	vmPropsMu sync.Mutex
	vmProps   = seedVMProperties()
)

func seedVMProperties() map[string][]VMProperty {
	now := "2026-05-20T14:00:00Z"
	return map[string][]VMProperty{
		"web-1": {
			{Key: "owner", Value: "team-alpha", GuestReadable: true, UpdatedAt: now},
			{Key: "cost-center", Value: "AB-1234", GuestReadable: false, UpdatedAt: now},
			{Key: "tier", Value: "production", GuestReadable: true, UpdatedAt: now},
		},
	}
}

// handleListVMProperties — GET /api/microvms/{name}/properties
func handleListVMProperties(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmPropsMu.Lock()
	defer vmPropsMu.Unlock()
	props := vmProps[name]
	if props == nil {
		props = []VMProperty{}
	}
	writeJSON(w, http.StatusOK, props)
}

// handleSetVMProperty — POST /api/microvms/{name}/properties
// Body : {key, value, guest_readable}. Setting an existing key
// replaces the value + bumps UpdatedAt ; the operation is idempotent
// at the (key) granularity. Returns the stored row.
func handleSetVMProperty(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body VMProperty
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	body.Key = strings.TrimSpace(body.Key)
	if body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key is required"})
		return
	}
	body.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	vmPropsMu.Lock()
	defer vmPropsMu.Unlock()
	props := vmProps[name]
	for i, p := range props {
		if p.Key == body.Key {
			props[i] = body
			vmProps[name] = props
			writeJSON(w, http.StatusOK, body)
			return
		}
	}
	vmProps[name] = append(props, body)
	writeJSON(w, http.StatusCreated, body)
}

// handleDeleteVMProperty — DELETE /api/microvms/{name}/properties/{key}
func handleDeleteVMProperty(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key := r.PathValue("key")
	vmPropsMu.Lock()
	defer vmPropsMu.Unlock()
	props := vmProps[name]
	for i, p := range props {
		if p.Key == key {
			vmProps[name] = append(props[:i], props[i+1:]...)
			writeJSON(w, http.StatusOK, map[string]any{"removed": key})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such property"})
}

// ---- UEFI variables ----------------------------------------------

// UEFIVar is one entry in the VM's UEFI NVRAM. Namespace is the EFI
// vendor GUID (e.g. "8be4df61-93ca-11d2-aa0d-00e098032b8c" for the
// EFI Global Variable namespace). Value is hex-encoded so the editor
// can show + edit arbitrary byte blobs without a binary-text dance.
type UEFIVar struct {
	Namespace  string   `json:"namespace"`  // GUID
	Name       string   `json:"name"`       // e.g. "BootOrder", "Boot0000"
	ValueHex   string   `json:"value_hex"`  // hex of raw bytes ; "" = empty value (still valid)
	Attributes []string `json:"attributes"` // ["NonVolatile", "BootServiceAccess", "RuntimeAccess"]
	UpdatedAt  string   `json:"updated_at"`
}

const efiGlobalNS = "8be4df61-93ca-11d2-aa0d-00e098032b8c"

var (
	uefiVarsMu sync.Mutex
	uefiVars   = seedUEFIVars()
)

func seedUEFIVars() map[string][]UEFIVar {
	now := "2026-05-20T14:00:00Z"
	nvRtBs := []string{"NonVolatile", "BootServiceAccess", "RuntimeAccess"}
	return map[string][]UEFIVar{
		"web-1": {
			// BootOrder : uint16 LE list ; "0000" means "try Boot0000 first".
			{Namespace: efiGlobalNS, Name: "BootOrder", ValueHex: "0000",
				Attributes: nvRtBs, UpdatedAt: now},
			// Boot0000 : a load option entry — opaque blob to operators
			// in practice, but real OVMF will parse it.
			{Namespace: efiGlobalNS, Name: "Boot0000",
				ValueHex: "010000005800570065006600740000000400110000000000",
				Attributes: nvRtBs, UpdatedAt: now},
			// SecureBoot is a 1-byte enable/disable flag (0x01 = enabled).
			{Namespace: efiGlobalNS, Name: "SecureBoot", ValueHex: "01",
				Attributes: []string{"BootServiceAccess", "RuntimeAccess"}, UpdatedAt: now},
		},
	}
}

func handleListUEFIVars(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	uefiVarsMu.Lock()
	defer uefiVarsMu.Unlock()
	v := uefiVars[name]
	if v == nil {
		v = []UEFIVar{}
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSetUEFIVar — POST /api/microvms/{name}/uefi-vars
//
// Body : {namespace, name, value_hex, attributes}. (namespace, name)
// is the natural key — an existing pair is replaced. Empty namespace
// defaults to the EFI Global Variable GUID so the common case
// (BootOrder, SecureBoot, …) doesn't require typing the GUID.
func handleSetUEFIVar(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body UEFIVar
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if strings.TrimSpace(body.Namespace) == "" {
		body.Namespace = efiGlobalNS
	}
	body.ValueHex = strings.ReplaceAll(strings.TrimSpace(body.ValueHex), " ", "")
	if !validHex(body.ValueHex) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "value_hex must be a (possibly empty) sequence of hex pairs",
		})
		return
	}
	body.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	uefiVarsMu.Lock()
	defer uefiVarsMu.Unlock()
	vars := uefiVars[name]
	for i, v := range vars {
		if v.Namespace == body.Namespace && v.Name == body.Name {
			vars[i] = body
			uefiVars[name] = vars
			writeJSON(w, http.StatusOK, body)
			return
		}
	}
	uefiVars[name] = append(vars, body)
	writeJSON(w, http.StatusCreated, body)
}

// handleDeleteUEFIVar — DELETE /api/microvms/{name}/uefi-vars/{ns}/{varname}
func handleDeleteUEFIVar(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ns := r.PathValue("ns")
	varname := r.PathValue("varname")
	uefiVarsMu.Lock()
	defer uefiVarsMu.Unlock()
	vars := uefiVars[name]
	for i, v := range vars {
		if v.Namespace == ns && v.Name == varname {
			uefiVars[name] = append(vars[:i], vars[i+1:]...)
			writeJSON(w, http.StatusOK, map[string]any{"removed": varname})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such variable"})
}

// validHex returns true when s is an even-length string of [0-9a-fA-F].
// Empty is valid (UEFI variables can carry an empty value).
func validHex(s string) bool {
	if len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
