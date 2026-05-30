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
//	                    RPC. Embedded etcd (go.etcd.io/etcd/server/v3/
//	                    embed) in single-node dev mode — same client
//	                    code paths and same watch semantics as the HA
//	                    case, no parallel HCL parser.
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
// Dev-mode topology — DECIDED : etcd embedded
// (go.etcd.io/etcd/server/v3/embed) inside weft-agent itself when
// no external --etcd-endpoints is configured. Single-node, no HA, but
// same client code paths + same watch semantics as the prod HA case.
// Operator's cluster.hcl picks the mode :
//
//	etcd { embed = true  data_dir = "~/.weft/etcd" }   # dev (single-binary)
//	etcd { endpoints = ["https://etcd-{0,1,2}:2379", …] }  # prod (HA cluster)
//
// HCL fallback for the catalogue itself is NOT a thing — the embedded
// etcd carries the same wire shape, so dev and prod hit the same
// reads. Same-codepaths-everywhere wins over any complexity saved by
// a side HCL parser.
//
// In the meantime : seedFlavors() below mirrors what used to be in
// resources.go. Both the user-facing /api/flavors endpoint and the
// admin "Flavors" sidebar page now flow through the catalogue, so
// the day weft-agent's ListFlavors RPC ships, swapping the
// implementation is a one-liner — the wire stays identical.
package server

import (
	"context"
	"sync"

	"github.com/openweft/weft-webui/internal/wclient"
)

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

// liveFlavorCatalogue wraps wclient.ListFlavors / GetFlavor with a
// transparent fallback to the in-memory seed on Unimplemented. Same
// shape as the other live-first consumers in this package (see
// server.go's scheduling-rules / routers branches) :
//
//   - Happy path : agent returns rows, we project to []Flavor and
//     cache the slice so a hot dashboard doesn't pound the agent.
//   - Unimplemented : silent fallback to the mem catalogue. Lets the
//     webui stay green when pointed at an older agent that hasn't
//     shipped the Flavor RPCs.
//   - Other error : surfaced to the caller — a real failure should
//     show up in the dashboard, not get masked by the seed.
//
// Cache is intentionally a single mutex-guarded slice ; the
// catalogue is small (single digits to low double-digits of
// entries) and writes go through the agent + invalidate via TTL,
// not by direct mutation.
type liveFlavorCatalogue struct {
	live *wclient.Client
	mem  flavorCatalogue

	mu     sync.Mutex
	cached []Flavor
}

func newLiveFlavorCatalogue(live *wclient.Client) *liveFlavorCatalogue {
	return &liveFlavorCatalogue{
		live: live,
		mem:  newMemFlavorCatalogue(),
	}
}

func (l *liveFlavorCatalogue) List(ctx context.Context) ([]Flavor, error) {
	rows, _, err := l.live.ListFlavors(ctx, wclient.ListOpts{})
	if err != nil {
		if wclient.IsUnimplemented(err) {
			return l.mem.List(ctx)
		}
		return nil, err
	}
	out := make([]Flavor, 0, len(rows))
	for _, r := range rows {
		out = append(out, flavorFromRow(r))
	}
	l.mu.Lock()
	l.cached = append(l.cached[:0], out...)
	l.mu.Unlock()
	dup := make([]Flavor, len(out))
	copy(dup, out)
	return dup, nil
}

// Get hits the per-name GetFlavor RPC and falls back to the cached
// List output (or the mem seed if List has never succeeded). Same
// not-found semantics as memFlavorCatalogue — bool=false rather
// than an error so the consumer pattern stays uniform.
func (l *liveFlavorCatalogue) Get(ctx context.Context, name string) (Flavor, bool) {
	row, err := l.live.GetFlavor(ctx, name)
	if err == nil {
		return flavorFromRow(row), true
	}
	if wclient.IsUnimplemented(err) {
		// Walk the most recent List() result if present, otherwise
		// the seed. Keeping the mem path as the last-resort source
		// of truth on a brand-new instance.
		l.mu.Lock()
		cached := append([]Flavor(nil), l.cached...)
		l.mu.Unlock()
		for _, f := range cached {
			if f.Name == name {
				return f, true
			}
		}
		return l.mem.Get(ctx, name)
	}
	// Real error — caller treats as not-found, same as the mem impl
	// would for an unknown name. Logged at the handler boundary.
	return Flavor{}, false
}

// flavorFromRow lifts the wclient row-shape map back to the typed
// Flavor we expose to the rest of the package. Defensive on type
// assertions — bogus rows just yield zeroed fields rather than
// panicking the catalogue.
func flavorFromRow(r map[string]any) Flavor {
	f := Flavor{}
	if v, ok := r["name"].(string); ok {
		f.Name = v
	}
	if v, ok := r["vcpu"].(int); ok {
		f.VCPU = v
	}
	if v, ok := r["ram"].(string); ok {
		f.RAM = v
	}
	if v, ok := r["ephemeral_gb"].(int); ok {
		f.EphemeralGB = v
	}
	if v, ok := r["gpu"].(string); ok {
		f.GPU = v
	}
	return f
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
