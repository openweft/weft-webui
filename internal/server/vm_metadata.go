// vm_metadata.go — per-VM property bag + UEFI NVRAM variables stores.
//
// The HTTP handlers moved to api_microvm_metadata.go (huma) ; this
// file owns the in-memory stores + the seed data.
//
// Two related-but-distinct stores share this file :
//
//   Properties (host-set application-level annotations) :
//     - free-form key→value (operator-defined)
//     - each carries a GuestReadable flag — when true, the in-guest
//       weft-microvm-agent is allowed to read the value via its NATS API
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
// matching RPCs and the in-guest weft-microvm-agent learns the property
// subject, this file becomes a thin live-first wrapper.
package server

import "sync"

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
