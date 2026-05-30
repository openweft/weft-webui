package server

import "net/http"

// handleNetworkTopology builds the mesh graph the dashboard draws : the
// overlay networks (hubs, peered WireGuard) and every workload microVM / VM
// attached to one of them, plus the platform infra microVMs on the mgmt
// network. Derived from the registry so it stays in sync with the tables.
func handleNetworkTopology(w http.ResponseWriter, r *http.Request) {
	nets := make([]map[string]any, 0)
	for _, m := range resourceByID["networks"].Rows {
		nets = append(nets, map[string]any{
			"id": sval(m, "name"), "name": sval(m, "name"),
			"cidr": sval(m, "cidr"), "az": sval(m, "az"), "type": sval(m, "type"),
		})
	}

	nodes := make([]map[string]any, 0)
	addRows := func(resID, kind string) {
		for _, m := range resourceByID[resID].Rows {
			nodes = append(nodes, map[string]any{
				"id": sval(m, "name"), "name": sval(m, "name"), "kind": kind,
				"network": sval(m, "network"), "status": sval(m, "status"),
				"project": sval(m, "project"), "host": sval(m, "host"),
			})
		}
	}
	addRows("microvms", "microvm")
	addRows("instances", "instance")

	// Platform infra microVMs live on the mgmt network (one shown per
	// service).
	//
	// Networking control plane :
	//   weft-network  — bespoke controller, watches weft-agent events, reconciles
	//                   LoadBalancer/Router/Network into Envoy xDS +
	//                   WireGuard configs. One per DC (HA via etcd leader).
	//   envoy-dc{a,b,c} — data plane LBs, programmed by weft-network.
	//
	// Observability :
	//   otel-collector scrapes /metrics from each weft-webui admin port
	//   (over WG), victoriametrics stores long-term, perses dashboards
	//   (SSO via dex). All CNCF, like the rest of the platform.
	for _, name := range []string{
		"etcd", "nats", "dex", "weft", "cubefs",
		"weft-network", "envoy-dca", "envoy-dcb", "envoy-dcc",
		"otel-collector", "victoriametrics", "perses",
	} {
		nodes = append(nodes, map[string]any{
			"id": name, "name": name, "kind": "infra",
			"network": "mgmt", "status": "running", "project": "platform", "host": "—",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"networks": nets, "nodes": nodes})
}

func sval(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
