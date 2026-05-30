package server

import "net/http"

// Quotas — per-project resource limits shown on the overview. Mock values
// chosen to exercise all four consumption bands (green / yellow / orange /
// red). Wiring to the real quota system swaps the slice for a lookup.
type Quota struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Icon  string `json:"icon"` // frontend icon key
	Used  int    `json:"used"`
	Limit int    `json:"limit"`
	Unit  string `json:"unit"`
}

// Cluster-wide quotas — the fallback when no scope is selected.
var quotas = []Quota{
	{ID: "vcpus", Label: "vCPUs", Icon: "cpu", Used: 28, Limit: 64, Unit: ""},
	{ID: "memory", Label: "Memory", Icon: "ram", Used: 200, Limit: 256, Unit: "GB"},
	// GPUs counted in physical cards (not vGPUs) ; matches the
	// notation on Flavors + Hosts ("1×A100-40G" → consumes 1 card).
	{ID: "gpus", Label: "GPUs", Icon: "gpu", Used: 3, Limit: 6, Unit: ""},
	{ID: "microvms", Label: "microVMs", Icon: "microvm", Used: 51, Limit: 80, Unit: ""},
	{ID: "instances", Label: "Instances", Icon: "vm", Used: 2, Limit: 10, Unit: ""},
	{ID: "volumes", Label: "Volumes", Icon: "volume", Used: 7, Limit: 20, Unit: ""},
	{ID: "shares", Label: "Shares", Icon: "share", Used: 3, Limit: 12, Unit: ""},
	{ID: "block_gb", Label: "Block storage", Icon: "storage", Used: 1480, Limit: 2000, Unit: "GB"},
	{ID: "share_gb", Label: "Share storage", Icon: "share", Used: 6660, Limit: 16384, Unit: "GB"},
	{ID: "object_gb", Label: "Object storage", Icon: "bucket", Used: 1660, Limit: 4096, Unit: "GB"},
	{ID: "floating_ips", Label: "Floating IPs", Icon: "ip", Used: 4, Limit: 4, Unit: ""},
}

// tenantQuotaDims describes the 12 Quotas-struct dimensions in the
// order they should appear on the Overview when the session is
// scoped to a tenant. Pairs each typed field to the matching icon +
// human label. Listed once here so the renderer stays declarative.
var tenantQuotaDims = []struct {
	ID, Label, Icon, Unit string
	Get                   func(q Quotas) int
}{
	{"vcpu", "vCPUs", "cpu", "", func(q Quotas) int { return q.VCPU }},
	{"ram_gib", "Memory", "ram", "GiB", func(q Quotas) int { return q.RAMGiB }},
	{"gpus", "GPUs", "gpu", "", func(q Quotas) int { return q.GPUs }},
	{"projects", "Projects", "microvm", "", func(q Quotas) int { return q.Projects }},
	{"volumes", "Volumes", "volume", "", func(q Quotas) int { return q.Volumes }},
	{"volumes_gib", "Block storage", "storage", "GiB", func(q Quotas) int { return q.VolumesGiB }},
	{"shares", "Shares", "share", "", func(q Quotas) int { return q.Shares }},
	{"shares_gib", "Share storage", "share", "GiB", func(q Quotas) int { return q.SharesGiB }},
	{"buckets", "Buckets", "bucket", "", func(q Quotas) int { return q.Buckets }},
	{"buckets_gib", "Object storage", "bucket", "GiB", func(q Quotas) int { return q.BucketsGiB }},
	{"registry_gib", "Registry", "image", "GiB", func(q Quotas) int { return q.RegistryGiB }},
	{"floating_ips", "Floating IPs", "ip", "", func(q Quotas) int { return q.FloatingIPs }},
}

// handleQuotas — scope-aware.
//
// When the session carries a tenant scope, return THAT tenant's
// quotas (cap + allocated) mapped to the same Quota[] shape the
// global path uses. The Overview already knows how to render the
// progress bars ; it doesn't need to know which path served them.
//
// When no scope is set (cluster admin "all tenants" view), serve the
// static global quotas as before. A project-only scope is treated the
// same way as tenant-only at this layer — the per-project quotas
// editor in TenantsPage already handles that view.
func handleQuotas(w http.ResponseWriter, r *http.Request) {
	tenant, _ := scopeFromRequest(r)
	if tenant != "" {
		if view, ok := tenantsDB.tenantQuotaView(tenant); ok {
			cap, _ := view["cap"].(Quotas)
			alloc, _ := view["allocated"].(Quotas)
			out := make([]Quota, 0, len(tenantQuotaDims))
			for _, d := range tenantQuotaDims {
				out = append(out, Quota{
					ID: d.ID, Label: d.Label, Icon: d.Icon, Unit: d.Unit,
					Used:  d.Get(alloc),
					Limit: d.Get(cap),
				})
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	writeJSON(w, http.StatusOK, quotas)
}
