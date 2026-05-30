// quotas.go — types + seed values + dimension list for the quota
// views. The /api/quotas handler moved to api_tenants.go (huma) ;
// this file keeps just the data the typed op consumes.
package server

// Quota — per-project resource limit shown on the overview. Mock
// values are chosen to exercise all four consumption bands (green /
// yellow / orange / red). Wiring to the real quota system swaps the
// slice for a lookup.
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
