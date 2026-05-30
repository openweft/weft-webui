// api_networking.go — typed endpoints for the weft-network controller :
// networks, routers, load balancers, DNS zones / records, floating IPs,
// security groups, scheduling rules, and the topology view.
//
// Live-first across the board ; mock-mode mutations 503 (no fake
// state) ; scheduling-rules retain the in-memory fallback so the
// affordance survives a partial weft-network rollout.

package server

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/wclient"
)

// requireLiveNetCtx returns nil when the controller is wired, a 503
// huma error otherwise. Used for resources where there's no sane
// mock-mode mutation path.
func requireLiveNetCtx() error {
	if liveNet == nil {
		return huma.Error503ServiceUnavailable("no live weft-network controller configured ; start the webui with --weft-network-socket")
	}
	return nil
}

// resolveProjectOrPlatform falls back to "platform" for resources that
// live in the cluster-wide bucket (routers, DNS zones) when the
// session has no project scope.
func resolveProjectOrPlatform(ctx context.Context, queryProject string) string {
	if p, err := resolveVMProjectCtx(ctx, queryProject); err == nil {
		return p
	}
	return "platform"
}

func mountNetworkingAPI(api huma.API, scope Scope) {
	mountNetworksAPI(api)
	mountSecurityGroupsAPI(api)
	mountFloatingIPsAPI(api)
	mountRoutersAPI(api)
	mountLoadBalancersAPI(api)
	mountDNSAPI(api)
	mountSchedulingRulesAPI(api)
	if scope == ScopeAdmin {
		mountNetworkTopologyAPI(api)
	}
}

// ---- Networks ----------------------------------------------------

func mountNetworksAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-network",
		Method:        "POST",
		Path:          "/api/networks",
		Summary:       "Create a network (live-only)",
		Tags:          []string{"networks"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createNetworkInput) (*createNetworkOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		if in.Body.Name == "" || in.Body.CIDR == "" {
			return nil, huma.Error400BadRequest("name and cidr are required")
		}
		if cerr := live.CreateNetwork(ctx, wclient.CreateNetworkOpts{
			Project: project, Name: in.Body.Name, CIDR: in.Body.CIDR,
			Gateway: in.Body.Gateway, Type: in.Body.Type, DNSServers: in.Body.DNSServers,
		}); cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "network.create")
		return &createNetworkOutput{Body: CreateNetworkResp{Name: in.Body.Name, Project: project, CIDR: in.Body.CIDR}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-network",
		Method:        "DELETE",
		Path:          "/api/networks/{uuid}",
		Summary:       "Delete a network (live-only)",
		Tags:          []string{"networks"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if err := live.DeleteNetwork(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "network.delete")
		return nil, nil
	})
}

// ---- Security groups ---------------------------------------------

func mountSecurityGroupsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-security-group",
		Method:        "POST",
		Path:          "/api/security-groups",
		Summary:       "Create a security group (live-only)",
		Tags:          []string{"security-groups"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createSGInput) (*createSGOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		if in.Body.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		uuid, cerr := live.CreateSecurityGroup(ctx, wclient.CreateSecurityGroupOpts{
			Project: project, Name: in.Body.Name, Description: in.Body.Description, Rules: in.Body.Rules,
		})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "security-group.create")
		return &createSGOutput{Body: CreateSecurityGroupResp{
			Name: in.Body.Name, Project: project, UUID: uuid, Rules: len(in.Body.Rules),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-security-group",
		Method:        "DELETE",
		Path:          "/api/security-groups/{uuid}",
		Summary:       "Delete a security group (live-only)",
		Tags:          []string{"security-groups"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if err := live.DeleteSecurityGroup(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "security-group.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-security-group-rules",
		Method:      "GET",
		Path:        "/api/security-groups/{uuid}/rules",
		Summary:     "Get a security group's rule list",
		Description: "Live-first ; on Unimplemented OR no daemon, falls back to the mock 'security-rules' resource (matched by group name) so the dashboard stays explorable before weft-agent's SG-rule embedding lands.",
		Tags:        []string{"security-groups"},
	}, func(ctx context.Context, in *uuidInput) (*sgRulesOutput, error) {
		if live != nil {
			rules, err := live.GetSecurityGroup(ctx, in.UUID)
			if err == nil {
				return &sgRulesOutput{Body: rules}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		var groupName string
		for _, row := range resourceByID["security-groups"].Rows {
			if row["uuid"] == in.UUID {
				groupName, _ = row["name"].(string)
				break
			}
		}
		out := make([]wclient.SecurityRule, 0)
		if groupName != "" {
			for _, row := range resourceByID["security-rules"].Rows {
				if row["group"] == groupName {
					portMin, portMax := parsePortRange(row["port_range"])
					dir, _ := row["direction"].(string)
					proto, _ := row["protocol"].(string)
					cidr, _ := row["remote"].(string)
					out = append(out, wclient.SecurityRule{
						Direction: dir, Protocol: proto,
						PortMin: int32(portMin), PortMax: int32(portMax),
						RemoteCIDR: cidr,
					})
				}
			}
		}
		return &sgRulesOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-security-group-rules",
		Method:      "PUT",
		Path:        "/api/security-groups/{uuid}/rules",
		Summary:     "Atomically replace a security group's rules",
		Tags:        []string{"security-groups"},
	}, func(ctx context.Context, in *setSGRulesInput) (*passthroughOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if err := live.SetSecurityGroupRules(ctx, in.UUID, in.Body); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "security-group.set-rules")
		return &passthroughOutput{Body: map[string]any{"uuid": in.UUID, "rules": len(in.Body)}}, nil
	})
}

// ---- Floating IPs ------------------------------------------------

func mountFloatingIPsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "allocate-floating-ip",
		Method:        "POST",
		Path:          "/api/floating-ips",
		Summary:       "Allocate a floating IP (live-only)",
		Tags:          []string{"floating-ips"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *allocateFloatingIPInput) (*allocateFloatingIPOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		if in.Body.Network == "" {
			return nil, huma.Error400BadRequest("network is required")
		}
		uuid, addr, cerr := live.AllocateFloatingIP(ctx, project, in.Body.Network)
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "floating-ip.allocate")
		return &allocateFloatingIPOutput{Body: AllocateFloatingIPResp{
			UUID: uuid, Address: addr, Network: in.Body.Network, Project: project,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "release-floating-ip",
		Method:        "DELETE",
		Path:          "/api/floating-ips/{uuid}",
		Summary:       "Release a floating IP (live-only)",
		Tags:          []string{"floating-ips"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if err := live.ReleaseFloatingIP(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "floating-ip.release")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "map-floating-ip",
		Method:      "POST",
		Path:        "/api/floating-ips/{uuid}/map",
		Summary:     "Map a floating IP to a target",
		Tags:        []string{"floating-ips"},
	}, func(ctx context.Context, in *mapFloatingIPInput) (*passthroughOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Body.TargetKind != "vm" && in.Body.TargetKind != "lb" {
			return nil, huma.Error400BadRequest("target_kind must be 'vm' or 'lb'")
		}
		if in.Body.TargetName == "" {
			return nil, huma.Error400BadRequest("target_name is required")
		}
		if err := live.MapFloatingIP(ctx, in.UUID, in.Body.TargetKind, in.Body.TargetName); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "floating-ip.map")
		return &passthroughOutput{Body: map[string]string{"uuid": in.UUID, "target": in.Body.TargetName}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "unmap-floating-ip",
		Method:        "POST",
		Path:          "/api/floating-ips/{uuid}/unmap",
		Summary:       "Unmap a floating IP",
		Tags:          []string{"floating-ips"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if err := live.UnmapFloatingIP(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "floating-ip.unmap")
		return nil, nil
	})
}

// ---- Routers -----------------------------------------------------

func mountRoutersAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-router",
		Method:        "POST",
		Path:          "/api/routers",
		Summary:       "Create a router (weft-network controller)",
		Tags:          []string{"routers"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createRouterInput) (*createNameUUIDOutput, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		project := resolveProjectOrPlatform(ctx, in.Project)
		if in.Body.Name == "" || in.Body.Kind == "" {
			return nil, huma.Error400BadRequest("name and kind are required")
		}
		uuid, err := liveNet.CreateRouter(ctx, wclient.CreateRouterOpts{
			Project: project, Name: in.Body.Name, Kind: in.Body.Kind,
			Backend: in.Body.Backend, Networks: in.Body.Networks, External: in.Body.External,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "router.create")
		return &createNameUUIDOutput{Body: CreateNameUUID{Name: in.Body.Name, UUID: uuid}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-router",
		Method:        "DELETE",
		Path:          "/api/routers/{uuid}",
		Summary:       "Delete a router",
		Tags:          []string{"routers"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		if err := liveNet.DeleteRouter(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "router.delete")
		return nil, nil
	})
}

// ---- Load Balancers ----------------------------------------------

func mountLoadBalancersAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-loadbalancer",
		Method:        "POST",
		Path:          "/api/loadbalancers",
		Summary:       "Create a load balancer",
		Tags:          []string{"loadbalancers"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createLBInput) (*createNameUUIDOutput, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		if in.Body.Name == "" || in.Body.Port == 0 {
			return nil, huma.Error400BadRequest("name and port are required")
		}
		uuid, cerr := liveNet.CreateLoadBalancer(ctx, wclient.CreateLoadBalancerOpts{
			Project: project, Name: in.Body.Name, Mode: in.Body.Mode,
			Port: in.Body.Port, Backends: in.Body.Backends, AZ: in.Body.AZ,
		})
		if cerr != nil {
			return nil, huma.Error502BadGateway("net: " + cerr.Error())
		}
		userActionCtx(ctx, "lb.create")
		return &createNameUUIDOutput{Body: CreateNameUUID{Name: in.Body.Name, UUID: uuid}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-loadbalancer",
		Method:        "DELETE",
		Path:          "/api/loadbalancers/{uuid}",
		Summary:       "Delete a load balancer",
		Tags:          []string{"loadbalancers"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		if err := liveNet.DeleteLoadBalancer(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "lb.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-loadbalancer-backends",
		Method:      "PUT",
		Path:        "/api/loadbalancers/{uuid}/backends",
		Summary:     "Atomically replace a load balancer's backend list",
		Tags:        []string{"loadbalancers"},
	}, func(ctx context.Context, in *setLBBackendsInput) (*passthroughOutput, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		if err := liveNet.SetLoadBalancerBackends(ctx, in.UUID, in.Body); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "lb.set-backends")
		return &passthroughOutput{Body: map[string]int{"backends": len(in.Body)}}, nil
	})
}

// ---- DNS ---------------------------------------------------------

func mountDNSAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-dns-zone",
		Method:        "POST",
		Path:          "/api/dns-zones",
		Summary:       "Create a DNS zone",
		Tags:          []string{"dns"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createDNSZoneInput) (*createNameUUIDOutput, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		project := resolveProjectOrPlatform(ctx, in.Project)
		if in.Body.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		uuid, err := liveNet.CreateDNSZone(ctx, wclient.CreateDNSZoneOpts{
			Project: project, Name: in.Body.Name, Role: in.Body.Role,
			TTLDefault: in.Body.TTLDefault, PushTarget: in.Body.PushTarget,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "dns-zone.create")
		return &createNameUUIDOutput{Body: CreateNameUUID{Name: in.Body.Name, UUID: uuid}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-dns-zone",
		Method:        "DELETE",
		Path:          "/api/dns-zones/{uuid}",
		Summary:       "Delete a DNS zone",
		Tags:          []string{"dns"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		if err := liveNet.DeleteDNSZone(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "dns-zone.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-dns-record",
		Method:        "POST",
		Path:          "/api/dns-records",
		Summary:       "Create a DNS record",
		Tags:          []string{"dns"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createDNSRecordInput) (*createDNSRecordOutput, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		if in.Body.ZoneUUID == "" || in.Body.Type == "" || in.Body.Value == "" {
			return nil, huma.Error400BadRequest("zone_uuid, type, value are required")
		}
		uuid, err := liveNet.CreateDNSRecord(ctx, wclient.CreateDNSRecordOpts{
			ZoneUUID: in.Body.ZoneUUID, Name: in.Body.Name, Type: in.Body.Type,
			Value: in.Body.Value, TTL: in.Body.TTL,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "dns-record.create")
		return &createDNSRecordOutput{Body: CreateDNSRecordResp{UUID: uuid, Name: in.Body.Name, Type: in.Body.Type}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-dns-record",
		Method:        "DELETE",
		Path:          "/api/dns-records/{uuid}",
		Summary:       "Delete a DNS record",
		Tags:          []string{"dns"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		if err := liveNet.DeleteDNSRecord(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "dns-record.delete")
		return nil, nil
	})
}

// ---- Scheduling rules --------------------------------------------

func mountSchedulingRulesAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-scheduling-rule",
		Method:        "POST",
		Path:          "/api/scheduling-rules",
		Summary:       "Create a scheduling rule (live-first ; mem fallback on Unimplemented)",
		Tags:          []string{"scheduling-rules"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createSchedulingRuleInput) (*passthroughOutput, error) {
		project := in.Body.Project
		if project == "" {
			project = resolveProjectOrPlatform(ctx, "")
		}
		if liveNet != nil {
			_, err := liveNet.CreateSchedulingRule(ctx, wclient.CreateSchedulingRuleNetOpts{
				Project: project, Name: in.Body.Name, Count: int32(in.Body.Count),
				Selector: in.Body.Selector, AZ: in.Body.AZ, Rack: in.Body.Rack, Host: in.Body.Host,
			})
			if err == nil {
				userActionCtx(ctx, "scheduling-rule.create")
				return &passthroughOutput{Body: map[string]any{
					"name": in.Body.Name, "project": project,
				}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("net: " + err.Error())
			}
		}
		rule := &SchedulingRule{
			Name: in.Body.Name, Count: in.Body.Count, Selector: in.Body.Selector,
			AZ: in.Body.AZ, Rack: in.Body.Rack, Host: in.Body.Host,
			Project: project,
		}
		if err := schedulingDB.create(rule); err != nil {
			return nil, hideHTTPErr(err)
		}
		userActionCtx(ctx, "scheduling-rule.create")
		return &passthroughOutput{Body: ruleToRow(rule)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-scheduling-rule",
		Method:        "DELETE",
		Path:          "/api/scheduling-rules/{name}",
		Summary:       "Delete a scheduling rule (mem store)",
		Tags:          []string{"scheduling-rules"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *struct {
		Name string `path:"name" minLength:"1" maxLength:"128"`
	}) (*struct{}, error) {
		if err := schedulingDB.delete(in.Name); err != nil {
			return nil, hideHTTPErr(err)
		}
		userActionCtx(ctx, "scheduling-rule.delete")
		return nil, nil
	})
}

// ---- Network topology --------------------------------------------

func mountNetworkTopologyAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "network-topology",
		Method:      "GET",
		Path:        "/api/network-topology",
		Summary:     "Mesh graph of overlay networks + attached workloads (admin)",
		Description: "Admin-only — exposes host placement for every node. Mounted on the admin listener only ; the user listener returns 404 so a stale SPA build never accidentally reveals which host runs a VM.",
		Tags:        []string{"network-topology"},
	}, func(_ context.Context, _ *struct{}) (*topologyOutput, error) {
		nets := make([]TopoNetwork, 0)
		for _, m := range resourceByID["networks"].Rows {
			nets = append(nets, TopoNetwork{
				ID: sval(m, "name"), Name: sval(m, "name"),
				CIDR: sval(m, "cidr"), AZ: sval(m, "az"), Type: sval(m, "type"),
			})
		}
		nodes := make([]TopoNode, 0)
		addRows := func(resID, kind string) {
			for _, m := range resourceByID[resID].Rows {
				nodes = append(nodes, TopoNode{
					ID: sval(m, "name"), Name: sval(m, "name"), Kind: kind,
					Network: sval(m, "network"), Status: sval(m, "status"),
					Project: sval(m, "project"), Host: sval(m, "host"),
				})
			}
		}
		addRows("microvms", "microvm")
		addRows("instances", "instance")
		for _, name := range []string{
			"etcd", "nats", "dex", "weft", "cubefs",
			"weft-network", "envoy-dca", "envoy-dcb", "envoy-dcc",
			"otel-collector", "victoriametrics", "perses",
		} {
			nodes = append(nodes, TopoNode{
				ID: name, Name: name, Kind: "infra",
				Network: "mgmt", Status: "running", Project: "platform", Host: "—",
			})
		}
		return &topologyOutput{Body: TopologyBody{Networks: nets, Nodes: nodes}}, nil
	})
}

// TopoNetwork is one overlay network in the mesh map.
type TopoNetwork struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	CIDR string `json:"cidr"`
	AZ   string `json:"az"`
	Type string `json:"type"`
}

// TopoNode is one workload or infra microVM in the mesh map.
type TopoNode struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind" enum:"microvm,instance,infra"`
	Network string `json:"network"`
	Status  string `json:"status"`
	Project string `json:"project"`
	Host    string `json:"host"`
}

// TopologyBody is what /api/network-topology returns.
type TopologyBody struct {
	Networks []TopoNetwork `json:"networks"`
	Nodes    []TopoNode    `json:"nodes"`
}

type topologyOutput struct {
	Body TopologyBody
}

// hideHTTPErr converts the legacy *httpErr type from tenants.go into
// a huma error so the wire shape stays consistent across migrated +
// legacy code paths. The legacy paths still throw *httpErr until
// every batch lands.
func hideHTTPErr(err error) error {
	if he, ok := err.(*httpErr); ok {
		switch he.code {
		case 400:
			return huma.Error400BadRequest(he.msg)
		case 403:
			return huma.Error403Forbidden(he.msg)
		case 404:
			return huma.Error404NotFound(he.msg)
		case 409:
			return huma.Error409Conflict(he.msg)
		case 502:
			return huma.Error502BadGateway(he.msg)
		case 503:
			return huma.Error503ServiceUnavailable(he.msg)
		}
		return huma.NewError(he.code, he.msg)
	}
	return huma.Error500InternalServerError(fmt.Sprintf("%v", err))
}

// ---- inputs / outputs --------------------------------------------

// uuidInput is the shared shape for {uuid}-path endpoints. UUIDs
// are RFC4122 hex-with-dashes ; the regex isn't validated here
// because some weft-network UUIDs may be opaque tokens.
type uuidInput struct {
	UUID string `path:"uuid" doc:"Resource UUID" minLength:"1" maxLength:"64"`
}

// Typed 201-envelopes for the create endpoints. Each shape stays
// minimal — the SPA only consumes the names + uuids ; richer reads
// come from /api/resources/<kind> right after.

// CreateNameUUID is what routers, load balancers, and DNS zones
// return : {name, uuid}.
type CreateNameUUID struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

// CreateNetworkResp echoes name + project + cidr.
type CreateNetworkResp struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	CIDR    string `json:"cidr"`
}

// CreateSecurityGroupResp adds project + uuid + rule-count to the
// name. SecurityRules themselves live behind GET .../rules.
type CreateSecurityGroupResp struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	UUID    string `json:"uuid"`
	Rules   int    `json:"rules"`
}

// AllocateFloatingIPResp carries everything the FIP table needs to
// render the new row without a refresh.
type AllocateFloatingIPResp struct {
	UUID    string `json:"uuid"`
	Address string `json:"address"`
	Network string `json:"network"`
	Project string `json:"project"`
}

// CreateDNSRecordResp surfaces the operator-friendly fields ; type
// is kept because the records-table groups by it.
type CreateDNSRecordResp struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type createNameUUIDOutput        struct{ Body CreateNameUUID }
type createNetworkOutput         struct{ Body CreateNetworkResp }
type createSGOutput              struct{ Body CreateSecurityGroupResp }
type allocateFloatingIPOutput    struct{ Body AllocateFloatingIPResp }
type createDNSRecordOutput       struct{ Body CreateDNSRecordResp }
type sgRulesOutput               struct{ Body []wclient.SecurityRule }

type createNetworkInput struct {
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Name       string   `json:"name" minLength:"1" maxLength:"128"`
		CIDR       string   `json:"cidr" example:"10.0.0.0/24"`
		Gateway    string   `json:"gateway,omitempty"`
		Type       string   `json:"type,omitempty" doc:"e.g. 'wireguard' / 'flat'"`
		DNSServers []string `json:"dns_servers,omitempty"`
	}
}

type createSGInput struct {
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Name        string                 `json:"name" minLength:"1" maxLength:"128"`
		Description string                 `json:"description,omitempty"`
		Rules       []wclient.SecurityRule `json:"rules,omitempty"`
	}
}

type setSGRulesInput struct {
	UUID string `path:"uuid" minLength:"1"`
	Body []wclient.SecurityRule
}

type allocateFloatingIPInput struct {
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Network string `json:"network" doc:"Network name to allocate from" minLength:"1"`
	}
}

type mapFloatingIPInput struct {
	UUID string `path:"uuid" minLength:"1"`
	Body struct {
		TargetKind string `json:"target_kind" enum:"vm,lb"`
		TargetName string `json:"target_name" minLength:"1"`
	}
}

type createRouterInput struct {
	Project string `query:"project" doc:"Override the session project (defaults to platform)"`
	Body    struct {
		Name     string   `json:"name" minLength:"1" maxLength:"128"`
		Kind     string   `json:"kind" doc:"Router kind (controller-defined)"`
		Backend  string   `json:"backend,omitempty"`
		External string   `json:"external,omitempty"`
		Networks []string `json:"networks,omitempty"`
	}
}

type createLBInput struct {
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Name     string   `json:"name" minLength:"1" maxLength:"128"`
		Mode     string   `json:"mode,omitempty"`
		AZ       string   `json:"az,omitempty"`
		Port     uint32   `json:"port" minimum:"1" maximum:"65535"`
		Backends []string `json:"backends,omitempty"`
	}
}

type setLBBackendsInput struct {
	UUID string `path:"uuid" minLength:"1"`
	Body []string
}

type createDNSZoneInput struct {
	Project string `query:"project" doc:"Override the session project (defaults to platform)"`
	Body    struct {
		Name       string `json:"name" minLength:"1" maxLength:"253"`
		Role       string `json:"role,omitempty"`
		PushTarget string `json:"push_target,omitempty"`
		TTLDefault int32  `json:"ttl_default,omitempty" minimum:"0"`
	}
}

type createDNSRecordInput struct {
	Body struct {
		ZoneUUID string `json:"zone_uuid" minLength:"1"`
		Name     string `json:"name,omitempty"`
		Type     string `json:"type" minLength:"1" doc:"A / AAAA / CNAME / TXT / SRV / …"`
		Value    string `json:"value" minLength:"1"`
		TTL      int32  `json:"ttl,omitempty" minimum:"0"`
	}
}

type createSchedulingRuleInput struct {
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"128"`
		Project  string `json:"project,omitempty"`
		Count    int    `json:"count,omitempty" minimum:"0"`
		Selector string `json:"selector,omitempty"`
		AZ       string `json:"az,omitempty"`
		Rack     string `json:"rack,omitempty"`
		Host     string `json:"host,omitempty"`
	}
}
