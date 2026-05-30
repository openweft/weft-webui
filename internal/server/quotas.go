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

var quotas = []Quota{
	{ID: "vcpus", Label: "vCPUs", Icon: "cpu", Used: 28, Limit: 64, Unit: ""},
	{ID: "memory", Label: "Memory", Icon: "ram", Used: 200, Limit: 256, Unit: "GB"},
	// GPUs counted in physical cards (not vGPUs) ; matches the
	// notation on Flavors + Hosts ("1×A100-40G" → consumes 1 card).
	{ID: "gpus", Label: "GPUs", Icon: "gpu", Used: 3, Limit: 6, Unit: ""},
	{ID: "microvms", Label: "microVMs", Icon: "microvm", Used: 51, Limit: 80, Unit: ""},
	{ID: "instances", Label: "Instances", Icon: "vm", Used: 2, Limit: 10, Unit: ""},
	{ID: "volumes", Label: "Volumes", Icon: "volume", Used: 7, Limit: 20, Unit: ""},
	{ID: "block_gb", Label: "Block storage", Icon: "storage", Used: 1480, Limit: 2000, Unit: "GB"},
	{ID: "object_gb", Label: "Object storage", Icon: "bucket", Used: 1660, Limit: 4096, Unit: "GB"},
	{ID: "floating_ips", Label: "Floating IPs", Icon: "ip", Used: 4, Limit: 4, Unit: ""},
}

func handleQuotas(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, quotas)
}
