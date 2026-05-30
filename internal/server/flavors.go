// flavors.go — read-side catalogue of compute flavors.
//
// Today this is the source of truth, seeded in memory. It used to live
// inline in the resource registry ; pulling it out behind a
// flavorCatalogue interface stages the migration to weft-agent + etcd
// without rewriting the consumers when that lands.
//
// Target topology (cross-repo, not implemented here) :
//
//	etcd                /weft/catalogue/flavors/<name>     →  JSON
//	weft-agent          watch the prefix, cache, serve via ListFlavors
//	                    RPC ; HCL fallback for dev (single-binary).
//	weft-cli            `weft flavor {create,update,delete}` writes etcd
//	weft-webui          ← this binary, drops the in-memory seed and
//	                    becomes a plain consumer of ListFlavors.
//
// Why etcd : the rule `etcd-quorum` is already part of the platform's
// seed scheduling rules, so etcd is in the stack. Catalogue traffic
// is read-heavy with rare writes (operator edits) — etcd's watch +
// quorum semantics fit exactly. bbolt would have been single-writer
// local only, no HA, wrong tool.
//
// In the meantime : seedFlavors() below mirrors what used to be in
// resources.go. Both the user-facing /api/flavors endpoint and the
// admin "Flavors" sidebar page now flow through the catalogue, so
// the day weft-agent's ListFlavors RPC ships, swapping the
// implementation is a one-liner — the wire stays identical.
package server

import "context"

// Flavor is the wire shape both /api/flavors and /api/resources/flavors
// emit. RAM is a string like "4Gi" / "256Mi" so the catalogue can stay
// human-editable on the operator's side ; lifecycle.go parses it to
// MB at the point it threads into wclient.CreateVMOpts.
type Flavor struct {
	Name        string `json:"name"`
	VCPU        int    `json:"vcpu"`
	RAM         string `json:"ram"`
	EphemeralGB int    `json:"ephemeral_gb"`
	GPU         string `json:"gpu"`
}

// flavorCatalogue is the read-only contract every consumer goes
// through. Two impls anticipated :
//
//   - memFlavorCatalogue : in-memory seed (today, dev-mode forever)
//   - liveFlavorCatalogue : delegates to weft-agent's ListFlavors RPC,
//     fallback to mem on Unimplemented. Same pattern as the other
//     live-first handlers in this package.
type flavorCatalogue interface {
	List(ctx context.Context) ([]Flavor, error)
	Get(ctx context.Context, name string) (Flavor, bool)
}

// memFlavorCatalogue holds an immutable seed. Returning copies on
// List() so a caller can't mutate the canonical slice ; the API has
// no Add/Remove since the goal is to delete this impl entirely once
// the etcd-backed one lands.
type memFlavorCatalogue struct {
	flavors []Flavor
}

func newMemFlavorCatalogue() *memFlavorCatalogue {
	return &memFlavorCatalogue{flavors: seedFlavors()}
}

// seedFlavors — same envelope the resource registry used to carry
// inline. Kept in one place so the etcd seed (`weft flavor create
// --from-defaults`) and this dev fallback don't drift.
func seedFlavors() []Flavor {
	return []Flavor{
		{Name: "small", VCPU: 2, RAM: "4Gi", EphemeralGB: 8},
		{Name: "medium", VCPU: 4, RAM: "8Gi", EphemeralGB: 16},
		{Name: "large", VCPU: 8, RAM: "32Gi", EphemeralGB: 32},
		{Name: "xlarge", VCPU: 16, RAM: "64Gi", EphemeralGB: 64},
		{Name: "gpu-small", VCPU: 4, RAM: "16Gi", EphemeralGB: 32, GPU: "1×L4-24G"},
		{Name: "gpu-medium", VCPU: 8, RAM: "64Gi", EphemeralGB: 64, GPU: "1×A100-40G"},
		{Name: "gpu-large", VCPU: 32, RAM: "256Gi", EphemeralGB: 256, GPU: "4×H100-80G"},
	}
}

func (m *memFlavorCatalogue) List(ctx context.Context) ([]Flavor, error) {
	out := make([]Flavor, len(m.flavors))
	copy(out, m.flavors)
	return out, nil
}

func (m *memFlavorCatalogue) Get(ctx context.Context, name string) (Flavor, bool) {
	for _, f := range m.flavors {
		if f.Name == name {
			return f, true
		}
	}
	return Flavor{}, false
}

// flavorsCatalogue is the process-wide singleton. Today always the
// in-memory impl ; New() flips it to a live-backed wrapper once
// wclient gains ListFlavors. Exposed as a package var rather than
// pulled from Deps so handlers (which take a stdlib *http.Request
// only) can reach it without a closure dance.
var flavorsCatalogue flavorCatalogue = newMemFlavorCatalogue()

// flavorRows projects the catalogue to the map[string]any shape the
// generic /api/resources/{id} path expects. Lets the registry entry
// for "flavors" stay declarative without duplicating the seed data.
func flavorRows(ctx context.Context) []map[string]any {
	fl, err := flavorsCatalogue.List(ctx)
	if err != nil || len(fl) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(fl))
	for _, f := range fl {
		out = append(out, map[string]any{
			"name":         f.Name,
			"vcpu":         f.VCPU,
			"ram":          f.RAM,
			"ephemeral_gb": f.EphemeralGB,
			"gpu":          f.GPU,
		})
	}
	return out
}
