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
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
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
	if scope.Has(ScopeAdmin) {
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
			ExternalMode:    in.Body.ExternalMode,
			VLAN:            in.Body.VLAN,
			ParentInterface: in.Body.ParentInterface,
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

	// ---- Editable metadata layer (mock-friendly) -----------------
	//
	// The webui needs an "Edit" affordance even when no daemon is
	// wired ; live wiring routes these to SetNetworkDNS / a future
	// SetNetworkDescription / RenameNetwork. Mock store mirrors
	// the patterns used for volumes.

	huma.Register(api, huma.Operation{
		OperationID: "get-network-metadata",
		Method:      "GET",
		Path:        "/api/networks/{key}/metadata",
		Summary:     "Get the editable metadata layer for one network",
		Tags:        []string{"networks"},
	}, func(_ context.Context, in *networkKeyInput) (*getNetworkMetadataOutput, error) {
		return &getNetworkMetadataOutput{Body: getNetworkMetadata(in.Key)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "set-network-metadata",
		Method:        "PUT",
		Path:          "/api/networks/{key}/metadata",
		Summary:       "Replace the editable metadata for one network (admin)",
		Description:   "Description + DNS servers. UpdatedAt / UpdatedBy stamped server-side. Live wiring forwards DNS to SetNetworkDNS.",
		Tags:          []string{"networks"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *setNetworkMetadataInput) (*setNetworkMetadataOutput, error) {
		m := in.Body
		m.Description = strings.TrimSpace(m.Description)
		if m.DNSServers == nil {
			m.DNSServers = []string{}
		}
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if u := auth.UserFromContext(ctx); u != nil {
			m.UpdatedBy = u.Email
			if m.UpdatedBy == "" {
				m.UpdatedBy = u.Subject
			}
		}
		setNetworkMetadataStore(in.Key, m)
		return &setNetworkMetadataOutput{Body: m}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "rename-network",
		Method:        "PUT",
		Path:          "/api/networks/{key}",
		Summary:       "Rename a network (admin)",
		Description:   "Updates the human-readable name. Attached VMs keep referencing by uuid. Calls the live RenameNetwork RPC ; the key path arg must be the network UUID.",
		Tags:          []string{"networks"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *renameNetworkInput) (*renameNetworkOutput, error) {
		newName := strings.TrimSpace(in.Body.NewName)
		if newName == "" {
			return nil, huma.Error400BadRequest("new_name is required")
		}
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if err := live.RenameNetwork(ctx, in.Key, newName); err != nil {
			Audit(ctx, auditLogger, "network.rename", "network", in.Key, "", err, map[string]string{"new_name": newName})
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		Audit(ctx, auditLogger, "network.rename", "network", in.Key, "", nil, map[string]string{"new_name": newName})
		return &renameNetworkOutput{Body: renameNetworkResp{Name: newName}}, nil
	})
}

type networkKeyInput struct {
	Key string `path:"key" doc:"Network identifier (name today ; uuid once live wiring lands)" minLength:"1" maxLength:"128"`
}

type getNetworkMetadataOutput struct {
	Body NetworkMetadata
}

type setNetworkMetadataInput struct {
	Key  string `path:"key" doc:"Network identifier" minLength:"1" maxLength:"128"`
	Body NetworkMetadata
}

type setNetworkMetadataOutput struct {
	Body NetworkMetadata
}

type renameNetworkInput struct {
	Key  string `path:"key" doc:"Current network identifier" minLength:"1" maxLength:"128"`
	Body struct {
		NewName string `json:"new_name" doc:"New human-readable name" minLength:"1" maxLength:"128"`
	}
}

type renameNetworkResp struct {
	Name string `json:"name"`
}

type renameNetworkOutput struct {
	Body renameNetworkResp
}

type updateSGInput struct {
	UUID string `path:"uuid" doc:"Security-group uuid" minLength:"1" maxLength:"64"`
	Body struct {
		Name        string `json:"name,omitempty"        doc:"New name. Empty = keep current."`
		Description string `json:"description"           doc:"Free-form description. Empty string clears it."`
		// Enabled is a tri-state from the client's view : present-and-true
		// = enable, present-and-false = disable, absent = leave alone.
		// Modeled as a pointer so the JSON marshaller distinguishes the
		// three states correctly.
		Enabled *bool `json:"enabled,omitempty"     doc:"Enable / disable the group. Omit to leave unchanged ; disabled groups stay in the catalogue but their rules don't apply."`
	}
}

type UpdateSGResp struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type updateSGOutput struct {
	Body UpdateSGResp
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
		Summary:       "Delete a security group",
		Tags:          []string{"security-groups"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if live == nil {
			if !deleteMockSecurityGroup(in.UUID) {
				return nil, huma.Error404NotFound("group not found")
			}
			return nil, nil
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
		// Mock path : read from the in-memory store (seeded lazily
		// from the static security-rules table on first hit). Writes
		// flow through setMockSGRules so subsequent gets reflect them.
		return &sgRulesOutput{Body: getMockSGRules(in.UUID)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "update-security-group",
		Method:        "PUT",
		Path:          "/api/security-groups/{uuid}",
		Summary:       "Rename / re-describe a security group (mock-friendly)",
		Description:   "Updates name and/or description in the seed catalogue. Live wiring will route to RenameSecurityGroup + SetSecurityGroupDescription once exposed via huma.",
		Tags:          []string{"security-groups"},
		DefaultStatus: 200,
	}, func(_ context.Context, in *updateSGInput) (*updateSGOutput, error) {
		row := updateMockSecurityGroupRow(in.UUID, in.Body.Name, in.Body.Description, in.Body.Enabled)
		if row == nil {
			return nil, huma.Error404NotFound("group not found")
		}
		return &updateSGOutput{Body: UpdateSGResp{
			UUID:        in.UUID,
			Name:        str(row["name"]),
			Description: str(row["description"]),
			Enabled:     boolField(row["enabled"]),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-security-group-rules",
		Method:      "PUT",
		Path:        "/api/security-groups/{uuid}/rules",
		Summary:     "Atomically replace a security group's rules",
		Description: "Live-first ; falls back to an in-memory mock store (seeded from the static security-rules table on first read) when no live agent is wired, so the SecurityPage edit affordance works through staged rollouts.",
		Tags:        []string{"security-groups"},
	}, func(ctx context.Context, in *setSGRulesInput) (*setSGRulesOutput, error) {
		if live == nil {
			setMockSGRules(in.UUID, in.Body)
			Audit(ctx, auditLogger, "security-group.set-rules", "security-group", in.UUID, "ok", nil, map[string]string{
				"rules": strconv.Itoa(len(in.Body)),
				"mode":  "mock",
			})
			return &setSGRulesOutput{Body: SetSGRulesResp{UUID: in.UUID, Rules: len(in.Body)}}, nil
		}
		err := live.SetSecurityGroupRules(ctx, in.UUID, in.Body)
		Audit(ctx, auditLogger, "security-group.set-rules", "security-group", in.UUID, "", err, map[string]string{
			"rules": strconv.Itoa(len(in.Body)),
		})
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "security-group.set-rules")
		return &setSGRulesOutput{Body: SetSGRulesResp{UUID: in.UUID, Rules: len(in.Body)}}, nil
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
		Audit(ctx, auditLogger, "floating-ip.allocate", "floating-ip", uuid, "", cerr, map[string]string{
			"project": project,
			"network": in.Body.Network,
			"address": addr,
		})
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
	}, func(ctx context.Context, in *mapFloatingIPInput) (*mapFIPOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Body.TargetKind != "vm" && in.Body.TargetKind != "lb" {
			return nil, huma.Error400BadRequest("target_kind must be 'vm' or 'lb'")
		}
		if in.Body.TargetName == "" {
			return nil, huma.Error400BadRequest("target_name is required")
		}
		err := live.MapFloatingIP(ctx, in.UUID, in.Body.TargetKind, in.Body.TargetName, in.Body.RateLimitPPS)
		Audit(ctx, auditLogger, "floating-ip.map", "floating-ip", in.UUID, "", err, map[string]string{
			"target_kind":    in.Body.TargetKind,
			"target_name":    in.Body.TargetName,
			"rate_limit_pps": strconv.Itoa(int(in.Body.RateLimitPPS)),
		})
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "floating-ip.map")
		return &mapFIPOutput{Body: MapFIPResp{UUID: in.UUID, Target: in.Body.TargetName}}, nil
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
			Prefixes: in.Body.Prefixes, Replicas: in.Body.Replicas,
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
		Description:   "Live-first against weft-agent's CreateLoadBalancer (proto v0.8.0) ; the local catalogue row gets mirrored on success so the dashboard tree + map see the new LB without a re-list. On Unimplemented (or no live wiring) the existing weft-network controller path takes over.",
		Tags:          []string{"loadbalancers"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createLBInput) (*createNameUUIDOutput, error) {
		if in.Body.Name == "" || in.Body.Port == 0 {
			return nil, huma.Error400BadRequest("name and port are required")
		}
		project, perr := resolveVMProjectCtx(ctx, in.Project)
		if perr != nil {
			return nil, perr
		}
		listenAddr := ""
		if in.Body.Port > 0 {
			listenAddr = ":" + strconv.FormatUint(uint64(in.Body.Port), 10)
		}
		protocol := strings.ToLower(strings.TrimSpace(in.Body.Mode))
		backends := make([]map[string]any, 0, len(in.Body.Backends))
		for _, addr := range in.Body.Backends {
			backends = append(backends, map[string]any{"address": addr, "weight": int32(1)})
		}
		if live != nil {
			uuid, _, err := live.CreateLoadBalancer(ctx, project, in.Body.Name, listenAddr, protocol, backends)
			if err == nil {
				appendLoadBalancerRow(map[string]any{
					"uuid":       uuid,
					"name":       in.Body.Name,
					"mode":       in.Body.Mode,
					"port":       in.Body.Port,
					"backends":   strings.Join(in.Body.Backends, ", "),
					"az":         in.Body.AZ,
					"controller": "weft-agent",
					"project":    project,
					"status":     "active",
					"created":    time.Now().UTC().Format("2006-01-02"),
				})
				Audit(ctx, auditLogger, "lb.create", "lb", uuid, "", nil,
					map[string]string{"name": in.Body.Name, "project": project})
				userActionCtx(ctx, "lb.create")
				return &createNameUUIDOutput{Body: CreateNameUUID{Name: in.Body.Name, UUID: uuid}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
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
		OperationID:   "update-loadbalancer",
		Method:        "PUT",
		Path:          "/api/loadbalancers/{uuid}",
		Summary:       "Update a load balancer's mutable listener fields (live-first)",
		Description:   "Patches name / mode / port — empty strings keep the current value. Live-first against weft-agent's UpdateLoadBalancer (proto v0.8.0). Mock store mirrored on success ; on Unimplemented falls back to mutating the local row directly.",
		Tags:          []string{"loadbalancers"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *updateLBInput) (*updateLBOutput, error) {
		listenAddr := ""
		if in.Body.Port > 0 {
			listenAddr = ":" + strconv.FormatUint(uint64(in.Body.Port), 10)
		}
		protocol := strings.ToLower(strings.TrimSpace(in.Body.Mode))
		mirror := func(row map[string]any) {
			if in.Body.Name != "" {
				row["name"] = in.Body.Name
			}
			if in.Body.Mode != "" {
				row["mode"] = in.Body.Mode
			}
			if in.Body.Port > 0 {
				row["port"] = in.Body.Port
			}
		}
		if live != nil {
			err := live.UpdateLoadBalancer(ctx, in.UUID, in.Body.Name, listenAddr, protocol)
			if err == nil {
				_ = updateLoadBalancerRow(in.UUID, mirror)
				Audit(ctx, auditLogger, "lb.update", "lb", in.UUID, "", nil, nil)
				userActionCtx(ctx, "lb.update")
				return &updateLBOutput{Body: UpdateLBResp{UUID: in.UUID, Name: in.Body.Name}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if !updateLoadBalancerRow(in.UUID, mirror) {
			return nil, huma.Error404NotFound("load balancer not found")
		}
		Audit(ctx, auditLogger, "lb.update", "lb", in.UUID, "", nil, nil)
		userActionCtx(ctx, "lb.update")
		row, _ := findLoadBalancerRow(in.UUID)
		name := in.Body.Name
		if name == "" && row != nil {
			name = str(row["name"])
		}
		return &updateLBOutput{Body: UpdateLBResp{UUID: in.UUID, Name: name}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-loadbalancer",
		Method:        "DELETE",
		Path:          "/api/loadbalancers/{uuid}",
		Summary:       "Delete a load balancer",
		Description:   "Live-first against weft-agent's DeleteLoadBalancer (proto v0.8.0). Cascade refusal (FloatingIPs mapped to the LB) surfaces as 409 Conflict carrying the blocking-fips count. On Unimplemented falls back to the weft-network controller path then the local catalogue.",
		Tags:          []string{"loadbalancers"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if live != nil {
			blockedFips, err := live.DeleteLoadBalancer(ctx, in.UUID)
			if err == nil {
				deleteLoadBalancerRow(in.UUID)
				Audit(ctx, auditLogger, "lb.delete", "lb", in.UUID, "", nil, nil)
				userActionCtx(ctx, "lb.delete")
				return nil, nil
			}
			if !wclient.IsUnimplemented(err) {
				if blockedFips > 0 {
					Audit(ctx, auditLogger, "lb.delete", "lb", in.UUID, "blocked", nil,
						map[string]string{"blocked_by_fips": strconv.Itoa(int(blockedFips))})
					return nil, huma.NewError(409, fmt.Sprintf("lb delete blocked by %d floating IP(s) ; unmap them first", blockedFips))
				}
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if liveNet == nil {
			if !deleteLoadBalancerRow(in.UUID) {
				return nil, huma.Error404NotFound("load balancer not found")
			}
			Audit(ctx, auditLogger, "lb.delete", "lb", in.UUID, "", nil, nil)
			userActionCtx(ctx, "lb.delete")
			return nil, nil
		}
		if err := liveNet.DeleteLoadBalancer(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		deleteLoadBalancerRow(in.UUID)
		userActionCtx(ctx, "lb.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-loadbalancer-backends",
		Method:      "PUT",
		Path:        "/api/loadbalancers/{uuid}/backends",
		Summary:     "Atomically replace a load balancer's backend list",
		Tags:        []string{"loadbalancers"},
	}, func(ctx context.Context, in *setLBBackendsInput) (*setLBBackendsOutput, error) {
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
		}
		if err := liveNet.SetLoadBalancerBackends(ctx, in.UUID, in.Body); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		userActionCtx(ctx, "lb.set-backends")
		return &setLBBackendsOutput{Body: SetLBBackendsResp{Backends: len(in.Body)}}, nil
	})
}

type updateLBInput struct {
	UUID string `path:"uuid" doc:"Load-balancer uuid" minLength:"1" maxLength:"64"`
	Body struct {
		Name string `json:"name,omitempty" doc:"New name. Empty = keep current." maxLength:"128"`
		Mode string `json:"mode,omitempty" doc:"L4 / L7. Empty = keep current." maxLength:"16"`
		Port uint32 `json:"port,omitempty" doc:"New listener port (>0 to change)" minimum:"0" maximum:"65535"`
	}
}

// UpdateLBResp echoes uuid + post-write name.
type UpdateLBResp struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type updateLBOutput struct{ Body UpdateLBResp }

// ---- DNS ---------------------------------------------------------

func mountDNSAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-dns-zone",
		Method:        "POST",
		Path:          "/api/dns-zones",
		Summary:       "Create a DNS zone",
		Description:   "Live-first against weft-agent's CreateDNSZone (proto v0.8.0) ; the local catalogue row is mirrored on success. On Unimplemented falls back to the weft-network controller path.",
		Tags:          []string{"dns"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createDNSZoneInput) (*createNameUUIDOutput, error) {
		if in.Body.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		project := resolveProjectOrPlatform(ctx, in.Project)
		if live != nil {
			uuid, _, err := live.CreateDNSZone(ctx, project, in.Body.Name, "", in.Body.TTLDefault)
			if err == nil {
				appendDNSZoneRow(map[string]any{
					"uuid":        uuid,
					"name":        in.Body.Name,
					"role":        in.Body.Role,
					"ttl_default": int(in.Body.TTLDefault),
					"push_target": in.Body.PushTarget,
					"project":     project,
					"enabled":     true,
					"created":     time.Now().UTC().Format("2006-01-02"),
				})
				Audit(ctx, auditLogger, "dns-zone.create", "dns-zone", uuid, "", nil,
					map[string]string{"name": in.Body.Name, "project": project})
				userActionCtx(ctx, "dns-zone.create")
				return &createNameUUIDOutput{Body: CreateNameUUID{Name: in.Body.Name, UUID: uuid}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
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
		Description:   "Live-first against weft-agent's DeleteDNSZone (proto v0.8.0). Cascade refusal (records remain) surfaces as 409 Conflict carrying the blocking-records count so the SPA can render a 'drain X records first' hint.",
		Tags:          []string{"dns"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if live != nil {
			blockedRecs, err := live.DeleteDNSZone(ctx, in.UUID)
			if err == nil {
				deleteDNSZoneRow(in.UUID)
				Audit(ctx, auditLogger, "dns-zone.delete", "dns-zone", in.UUID, "", nil, nil)
				userActionCtx(ctx, "dns-zone.delete")
				return nil, nil
			}
			if !wclient.IsUnimplemented(err) {
				if blockedRecs > 0 {
					Audit(ctx, auditLogger, "dns-zone.delete", "dns-zone", in.UUID, "blocked", nil,
						map[string]string{"blocked_by_records": strconv.Itoa(int(blockedRecs))})
					return nil, huma.NewError(409, fmt.Sprintf("zone delete blocked by %d record(s) ; drain them first", blockedRecs))
				}
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if liveNet == nil {
			// Mock fallback : drop the row from the seed + flush
			// the on-disk snapshot when persistence is wired up.
			if !deleteDNSZoneRow(in.UUID) {
				return nil, huma.Error404NotFound("zone not found")
			}
			Audit(ctx, auditLogger, "dns-zone.delete", "dns-zone", in.UUID, "", nil, nil)
			return nil, nil
		}
		if err := liveNet.DeleteDNSZone(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		deleteDNSZoneRow(in.UUID)
		Audit(ctx, auditLogger, "dns-zone.delete", "dns-zone", in.UUID, "", nil, nil)
		userActionCtx(ctx, "dns-zone.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "update-dns-zone",
		Method:        "PUT",
		Path:          "/api/dns-zones/{uuid}",
		Summary:       "Update a DNS zone (live-first ; mock fallback)",
		Description:   "Live-first against weft-agent's UpdateDNSZone (proto v0.8.0 — patches soa_email + ttl). The mock-layer fields (role / backend / push_target / enabled) keep flowing through the local store so the dashboard's editor remains rich while staged rollouts catch up.",
		Tags:          []string{"dns"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *updateDNSZoneInput) (*updateDNSZoneOutput, error) {
		mirror := func(row map[string]any) {
			if in.Body.Name != "" {
				row["name"] = in.Body.Name
			}
			if in.Body.Role != "" {
				row["role"] = in.Body.Role
			}
			if in.Body.TTLDefault > 0 {
				row["ttl_default"] = in.Body.TTLDefault
			}
			if in.Body.Backend != "" {
				row["backend"] = in.Body.Backend
			}
			// PushTarget may legitimately be cleared.
			row["push_target"] = in.Body.PushTarget
			if in.Body.Enabled != nil {
				row["enabled"] = *in.Body.Enabled
			}
		}
		if live != nil {
			// Proto carries soa_email + ttl ; pass -1 to "keep current"
			// when the dashboard hasn't supplied a new TTL.
			ttl := int32(-1)
			if in.Body.TTLDefault > 0 {
				ttl = int32(in.Body.TTLDefault)
			}
			err := live.UpdateDNSZone(ctx, in.UUID, "", ttl)
			if err == nil {
				_ = updateDNSZoneRow(in.UUID, mirror)
				Audit(ctx, auditLogger, "dns-zone.update", "dns-zone", in.UUID, "", nil, nil)
				userActionCtx(ctx, "dns-zone.update")
				row, _, _ := findDNSZoneByUUID(in.UUID)
				if row == nil {
					row = map[string]any{}
				}
				return &updateDNSZoneOutput{Body: UpdateDNSZoneResp{
					UUID:       in.UUID,
					Name:       str(row["name"]),
					Role:       str(row["role"]),
					TTLDefault: toInt(row["ttl_default"]),
					Backend:    str(row["backend"]),
					PushTarget: str(row["push_target"]),
					Enabled:    boolField(row["enabled"]),
				}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if !updateDNSZoneRow(in.UUID, mirror) {
			return nil, huma.Error404NotFound("zone not found")
		}
		Audit(ctx, auditLogger, "dns-zone.update", "dns-zone", in.UUID, "", nil, nil)
		row, _, _ := findDNSZoneByUUID(in.UUID)
		return &updateDNSZoneOutput{Body: UpdateDNSZoneResp{
			UUID:       in.UUID,
			Name:       str(row["name"]),
			Role:       str(row["role"]),
			TTLDefault: toInt(row["ttl_default"]),
			Backend:    str(row["backend"]),
			PushTarget: str(row["push_target"]),
			Enabled:    boolField(row["enabled"]),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-dns-record",
		Method:        "POST",
		Path:          "/api/dns-records",
		Summary:       "Create a DNS record",
		Description:   "Live-first against weft-agent's CreateDNSRecord (proto v0.8.0) ; the local catalogue row is mirrored on success. On Unimplemented falls back to the weft-network controller path.",
		Tags:          []string{"dns"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createDNSRecordInput) (*createDNSRecordOutput, error) {
		if in.Body.ZoneUUID == "" || in.Body.Type == "" || in.Body.Value == "" {
			return nil, huma.Error400BadRequest("zone_uuid, type, value are required")
		}
		if live != nil {
			uuid, _, err := live.CreateDNSRecord(ctx, in.Body.ZoneUUID, in.Body.Name, in.Body.Type, in.Body.Value, in.Body.TTL, 0)
			if err == nil {
				// Resolve the zone name for the mirror row so the
				// records table groups correctly without an extra
				// round-trip.
				zoneName := ""
				if zrow, _, ok := findDNSZoneByUUID(in.Body.ZoneUUID); ok {
					zoneName = str(zrow["name"])
				}
				appendDNSRecordRow(map[string]any{
					"uuid":      uuid,
					"zone_uuid": in.Body.ZoneUUID,
					"zone":      zoneName,
					"name":      in.Body.Name,
					"type":      in.Body.Type,
					"value":     in.Body.Value,
					"ttl":       int(in.Body.TTL),
					"enabled":   true,
				})
				Audit(ctx, auditLogger, "dns-record.create", "dns-record", uuid, "", nil,
					map[string]string{"zone_uuid": in.Body.ZoneUUID, "type": in.Body.Type, "name": in.Body.Name})
				userActionCtx(ctx, "dns-record.create")
				return &createDNSRecordOutput{Body: CreateDNSRecordResp{UUID: uuid, Name: in.Body.Name, Type: in.Body.Type}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if err := requireLiveNetCtx(); err != nil {
			return nil, err
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
		Description:   "Live-first against weft-agent's DeleteDNSRecord (proto v0.8.0). On Unimplemented falls back to weft-network controller, then the local catalogue.",
		Tags:          []string{"dns"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if live != nil {
			err := live.DeleteDNSRecord(ctx, in.UUID)
			if err == nil {
				deleteDNSRecordRow(in.UUID)
				Audit(ctx, auditLogger, "dns-record.delete", "dns-record", in.UUID, "", nil, nil)
				userActionCtx(ctx, "dns-record.delete")
				return nil, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if liveNet == nil {
			if !deleteDNSRecordRow(in.UUID) {
				return nil, huma.Error404NotFound("record not found")
			}
			Audit(ctx, auditLogger, "dns-record.delete", "dns-record", in.UUID, "", nil, nil)
			return nil, nil
		}
		if err := liveNet.DeleteDNSRecord(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("net: " + err.Error())
		}
		deleteDNSRecordRow(in.UUID)
		Audit(ctx, auditLogger, "dns-record.delete", "dns-record", in.UUID, "", nil, nil)
		userActionCtx(ctx, "dns-record.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "update-dns-record",
		Method:        "PUT",
		Path:          "/api/dns-records/{uuid}",
		Summary:       "Update a DNS record (live-first ; mock fallback)",
		Description:   "Live-first against weft-agent's UpdateDNSRecord (proto v0.8.0 — patches value + ttl + priority ; name/type immutable). The mock-layer `enabled` flag keeps flowing through the local store so the dashboard toggle remains responsive.",
		Tags:          []string{"dns"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *updateDNSRecordInput) (*updateDNSRecordOutput, error) {
		mirror := func(row map[string]any) {
			if in.Body.Name != "" {
				row["name"] = in.Body.Name
			}
			if in.Body.Type != "" {
				row["type"] = in.Body.Type
			}
			if in.Body.Value != "" {
				row["value"] = in.Body.Value
			}
			if in.Body.TTL > 0 {
				row["ttl"] = in.Body.TTL
			}
			if in.Body.Enabled != nil {
				row["enabled"] = *in.Body.Enabled
			}
		}
		if live != nil {
			ttl := int32(-1)
			if in.Body.TTL > 0 {
				ttl = int32(in.Body.TTL)
			}
			err := live.UpdateDNSRecord(ctx, in.UUID, in.Body.Name, in.Body.Type, in.Body.Value, ttl, -1)
			if err == nil {
				_ = updateDNSRecordRow(in.UUID, mirror)
				Audit(ctx, auditLogger, "dns-record.update", "dns-record", in.UUID, "", nil, nil)
				userActionCtx(ctx, "dns-record.update")
				row, _ := findDNSRecordByUUID(in.UUID)
				if row == nil {
					row = map[string]any{}
				}
				return &updateDNSRecordOutput{Body: UpdateDNSRecordResp{
					UUID:    in.UUID,
					Name:    str(row["name"]),
					Zone:    str(row["zone"]),
					Type:    str(row["type"]),
					Value:   str(row["value"]),
					TTL:     toInt(row["ttl"]),
					Enabled: boolField(row["enabled"]),
				}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if !updateDNSRecordRow(in.UUID, mirror) {
			return nil, huma.Error404NotFound("record not found")
		}
		Audit(ctx, auditLogger, "dns-record.update", "dns-record", in.UUID, "", nil, nil)
		row, _ := findDNSRecordByUUID(in.UUID)
		return &updateDNSRecordOutput{Body: UpdateDNSRecordResp{
			UUID:    in.UUID,
			Name:    str(row["name"]),
			Zone:    str(row["zone"]),
			Type:    str(row["type"]),
			Value:   str(row["value"]),
			TTL:     toInt(row["ttl"]),
			Enabled: boolField(row["enabled"]),
		}}, nil
	})
}

// ---- Scheduling rules --------------------------------------------
//
// Per the openweft pull-model (see memory : openweft-pull-model),
// weft-agent owns the SchedulingRule state ; weft-network observes
// changes through its reconcile loop. The mutation handlers therefore
// prefer `live` (weft-agent) when wired, fall back to `liveNet`
// (weft-network) on Unimplemented for staged rollouts, then to the
// in-memory mock store. GET endpoints keep reading from liveNet for
// the enriched compliance view ; a follow-up will merge them with
// `live.ListSchedulingRules` + `scheduling-rule.compliant` SSE events.

func mountSchedulingRulesAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-scheduling-rule",
		Method:        "POST",
		Path:          "/api/scheduling-rules",
		Summary:       "Create a scheduling rule (live-first via weft-agent ; liveNet/mem fallback on Unimplemented)",
		Description:   "Routes through `live.CreateSchedulingRule` (weft-agent owns the state per the pull-model) ; on Unimplemented falls back to `liveNet.CreateSchedulingRule` (weft-network) then to the in-memory mock so the affordance works during staged rollouts. Mock store is mirrored on success so /api/resources/scheduling-rules sees the new rule immediately.",
		Tags:          []string{"scheduling-rules"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createSchedulingRuleInput) (*createSchedRuleOutput, error) {
		project := in.Body.Project
		if project == "" {
			project = resolveProjectOrPlatform(ctx, "")
		}
		mirror := func(uuid string) {
			rule := &SchedulingRule{
				UUID: uuid,
				Name: in.Body.Name, Count: in.Body.Count, Selector: in.Body.Selector,
				AZ: in.Body.AZ, Rack: in.Body.Rack, Host: in.Body.Host,
				Project: project,
			}
			// Best-effort : if the name already exists in the mock the
			// create returns conflict ; the live write was authoritative
			// regardless, so swallow it.
			_ = schedulingDB.create(rule)
		}
		// Live-first : weft-agent is the source of truth for the rule
		// catalogue. The proto's CreateSchedulingRule has no project
		// field (rules are cluster-wide) ; antiAffinity carries the
		// AZ/Rack/Host compact form.
		if live != nil {
			uuid, _, err := live.CreateSchedulingRule(ctx, project, in.Body.Name, in.Body.Selector,
				int32(in.Body.Count), composeAntiAffinity(in.Body.AZ, in.Body.Rack, in.Body.Host))
			if err == nil {
				mirror(uuid)
				Audit(ctx, auditLogger, "scheduling-rule.create", "scheduling-rule", uuid, "", nil,
					map[string]string{"name": in.Body.Name, "project": project})
				userActionCtx(ctx, "scheduling-rule.create")
				return &createSchedRuleOutput{Body: CreateSchedRuleResp{
					Name: in.Body.Name, Project: project,
				}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if liveNet != nil {
			uuid, err := liveNet.CreateSchedulingRule(ctx, wclient.CreateSchedulingRuleNetOpts{
				Project: project, Name: in.Body.Name, Count: int32(in.Body.Count),
				Selector: in.Body.Selector, AZ: in.Body.AZ, Rack: in.Body.Rack, Host: in.Body.Host,
			})
			if err == nil {
				mirror(uuid)
				Audit(ctx, auditLogger, "scheduling-rule.create", "scheduling-rule", uuid, "", nil,
					map[string]string{"name": in.Body.Name, "project": project, "mode": "net"})
				userActionCtx(ctx, "scheduling-rule.create")
				return &createSchedRuleOutput{Body: CreateSchedRuleResp{
					Name: in.Body.Name, Project: project,
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
		Audit(ctx, auditLogger, "scheduling-rule.create", "scheduling-rule", rule.UUID, "", nil,
			map[string]string{"name": rule.Name, "project": rule.Project, "mode": "mock"})
		userActionCtx(ctx, "scheduling-rule.create")
		return &createSchedRuleOutput{Body: CreateSchedRuleResp{
			Name: rule.Name, Project: rule.Project,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-scheduling-rule",
		Method:        "DELETE",
		Path:          "/api/scheduling-rules/{name}",
		Summary:       "Delete a scheduling rule (live-first via weft-agent ; liveNet/mem fallback)",
		Description:   "Resolves name → uuid via the mock catalogue, then routes through `live.DeleteSchedulingRule` (weft-agent). On Unimplemented falls back to `liveNet.DeleteSchedulingRule` (weft-network) then to the in-memory mock so the affordance keeps working during staged rollouts.",
		Tags:          []string{"scheduling-rules"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *struct {
		Name string `path:"name" minLength:"1" maxLength:"128"`
	}) (*struct{}, error) {
		uuid, _ := schedulingDB.ruleUUID(in.Name)
		if live != nil && uuid != "" {
			err := live.DeleteSchedulingRule(ctx, uuid)
			if err == nil {
				_ = schedulingDB.delete(in.Name)
				Audit(ctx, auditLogger, "scheduling-rule.delete", "scheduling-rule", uuid, "", nil,
					map[string]string{"name": in.Name})
				userActionCtx(ctx, "scheduling-rule.delete")
				return nil, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if liveNet != nil && uuid != "" {
			err := liveNet.DeleteSchedulingRule(ctx, uuid)
			if err == nil {
				_ = schedulingDB.delete(in.Name)
				Audit(ctx, auditLogger, "scheduling-rule.delete", "scheduling-rule", uuid, "", nil,
					map[string]string{"name": in.Name, "mode": "net"})
				userActionCtx(ctx, "scheduling-rule.delete")
				return nil, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("net: " + err.Error())
			}
		}
		if err := schedulingDB.delete(in.Name); err != nil {
			return nil, hideHTTPErr(err)
		}
		Audit(ctx, auditLogger, "scheduling-rule.delete", "scheduling-rule", uuid, "", nil,
			map[string]string{"name": in.Name, "mode": "mock"})
		userActionCtx(ctx, "scheduling-rule.delete")
		return nil, nil
	})

	// PATCH /api/scheduling-rules/{name} — partial update of placement
	// constraints, replica count, selector, project. Live-first via
	// `live.UpdateSchedulingRule` (weft-agent owns the state) ; on
	// Unimplemented or absence falls through to the in-memory store.
	// liveNet (weft-network) has no Update RPC of its own, so it's not
	// part of the fallback chain here. Each field is optional ; ""
	// explicitly clears an axis back to `any`.
	huma.Register(api, huma.Operation{
		OperationID:   "update-scheduling-rule",
		Method:        "PATCH",
		Path:          "/api/scheduling-rules/{name}",
		Summary:       "Update placement / count / selector of a scheduling rule (live-first via weft-agent)",
		Description:   "Partial-update : every field is optional. Empty-string on AZ/Rack/Host clears the axis. Live-first against `live.UpdateSchedulingRule` (weft-agent owns the state) ; the mock store is mirrored on success so the row projection stays in sync. On Unimplemented falls back to the mock store directly.",
		Tags:          []string{"scheduling-rules"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *updateSchedulingRuleInput) (*createSchedRuleOutput, error) {
		uuid, _ := schedulingDB.ruleUUID(in.Name)
		if live != nil && uuid != "" {
			// Resolve "keep current" semantics : the proto uses
			// targetCount == -1 to mean "keep" ; "" selectors / anti-
			// affinity mean "keep" too. The mock store's patch shape
			// uses nil pointers for "not provided" — translate.
			count := int32(-1)
			if in.Body.Count != nil {
				count = int32(*in.Body.Count)
			}
			selector := ""
			if in.Body.Selector != nil {
				selector = *in.Body.Selector
			}
			anti := ""
			// Build a fresh compact form only if any axis was patched ;
			// otherwise leave empty to mean "keep". Existing axes that
			// the caller didn't touch are read from the mock so the
			// new wire form represents the post-patch desired state.
			if in.Body.AZ != nil || in.Body.Rack != nil || in.Body.Host != nil {
				az, rack, host := "", "", ""
				if rule, ok := snapshotRule(in.Name); ok {
					az, rack, host = rule.AZ, rule.Rack, rule.Host
				}
				if in.Body.AZ != nil {
					az = *in.Body.AZ
				}
				if in.Body.Rack != nil {
					rack = *in.Body.Rack
				}
				if in.Body.Host != nil {
					host = *in.Body.Host
				}
				anti = composeAntiAffinity(az, rack, host)
			}
			err := live.UpdateSchedulingRule(ctx, uuid, in.Name, selector, count, anti)
			if err == nil {
				r, perr := schedulingDB.update(in.Name, in.Body)
				if perr != nil {
					return nil, hideHTTPErr(perr)
				}
				Audit(ctx, auditLogger, "scheduling-rule.update", "scheduling-rule", uuid, "", nil,
					map[string]string{"name": in.Name})
				userActionCtx(ctx, "scheduling-rule.update")
				return &createSchedRuleOutput{Body: CreateSchedRuleResp{
					Name: r.Name, Project: r.Project,
				}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		r, err := schedulingDB.update(in.Name, in.Body)
		if err != nil {
			return nil, hideHTTPErr(err)
		}
		Audit(ctx, auditLogger, "scheduling-rule.update", "scheduling-rule", uuid, "", nil,
			map[string]string{"name": in.Name, "mode": "mock"})
		userActionCtx(ctx, "scheduling-rule.update")
		return &createSchedRuleOutput{Body: CreateSchedRuleResp{
			Name: r.Name, Project: r.Project,
		}}, nil
	})
}

// composeAntiAffinity builds the compact "az=…, rack=…, host=…" wire
// form the weft-agent proto expects for the antiAffinity field.
// Absent axes drop out so a fully-unconstrained rule serialises as "".
func composeAntiAffinity(az, rack, host string) string {
	parts := make([]string, 0, 3)
	if az != "" {
		parts = append(parts, "az="+az)
	}
	if rack != "" {
		parts = append(parts, "rack="+rack)
	}
	if host != "" {
		parts = append(parts, "host="+host)
	}
	return strings.Join(parts, ", ")
}

// snapshotRule returns a copy of the mock-store rule under read-lock,
// so callers can read AZ/Rack/Host without racing against subsequent
// mutations. ok=false when the name isn't in the store.
func snapshotRule(name string) (SchedulingRule, bool) {
	schedulingDB.mu.RLock()
	defer schedulingDB.mu.RUnlock()
	r, ok := schedulingDB.rules[name]
	if !ok {
		return SchedulingRule{}, false
	}
	return *r, true
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
			"weft-network",
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

// Tiny ack-style shapes for set/map endpoints.

// MapFIPResp confirms which target a floating IP got mapped to.
type MapFIPResp struct {
	UUID   string `json:"uuid"`
	Target string `json:"target"`
}
type mapFIPOutput struct{ Body MapFIPResp }

// SetSGRulesResp echoes the SG UUID + the count of rules applied.
type SetSGRulesResp struct {
	UUID  string `json:"uuid"`
	Rules int    `json:"rules"`
}
type setSGRulesOutput struct{ Body SetSGRulesResp }

// SetLBBackendsResp surfaces the post-write backend count.
type SetLBBackendsResp struct {
	Backends int `json:"backends"`
}
type setLBBackendsOutput struct{ Body SetLBBackendsResp }

// CreateSchedRuleResp is the intersection of the live-mode and
// mock-mode return shapes — both paths set name + project, so the
// SPA can refresh the rule listing keyed on those.
type CreateSchedRuleResp struct {
	Name    string `json:"name"`
	Project string `json:"project"`
}
type createSchedRuleOutput struct{ Body CreateSchedRuleResp }

type createNetworkInput struct {
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Name       string   `json:"name" minLength:"1" maxLength:"128"`
		CIDR       string   `json:"cidr" example:"10.0.0.0/24"`
		Gateway    string   `json:"gateway,omitempty"`
		Type       string   `json:"type,omitempty" doc:"e.g. 'wireguard' / 'flat' / 'mesh' / 'bridged'"`
		DNSServers []string `json:"dns_servers,omitempty"`
		// Edge-attachment for floating IPs.
		ExternalMode    string `json:"external_mode,omitempty" enum:"bgp,vlan" doc:"'bgp' (default) or 'vlan' for L2-attached subnets"`
		VLAN            int32  `json:"vlan,omitempty" minimum:"0" maximum:"4094" doc:"802.1Q tag when external_mode == 'vlan' ; 0 = untagged trunk"`
		ParentInterface string `json:"parent_interface,omitempty" doc:"Host NIC name for the macvlan when external_mode == 'vlan'"`
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
		TargetKind   string `json:"target_kind" enum:"vm,lb"`
		TargetName   string `json:"target_name" minLength:"1"`
		RateLimitPPS int32  `json:"rate_limit_pps,omitempty" doc:"Inbound packets/sec cap (0=unlimited, capped at 100000)" minimum:"0" maximum:"100000"`
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
		Prefixes []string `json:"prefixes,omitempty" doc:"CIDRs the router advertises (kind=egress + backend=gobgp)"`
		Replicas int32    `json:"replicas,omitempty" minimum:"0" maximum:"10" doc:"Number of weft-router microVMs (HA). 0 = server default (1)."`
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

type updateDNSZoneInput struct {
	UUID string `path:"uuid" doc:"Zone uuid" minLength:"1" maxLength:"64"`
	Body struct {
		Name       string `json:"name,omitempty"        doc:"New zone name (FQDN). Empty = keep current."`
		Role       string `json:"role,omitempty"        doc:"primary / secondary / forward" enum:",primary,secondary,forward"`
		TTLDefault int    `json:"ttl_default,omitempty" doc:"Default TTL in seconds (>0 to change)" minimum:"0" maximum:"86400"`
		Backend    string `json:"backend,omitempty"     doc:"Backend (coredns / bind9 / route53 …). Empty = keep current."`
		PushTarget string `json:"push_target"           doc:"External NS to fan updates to (RFC-2136). Empty string clears the target."`
		Enabled    *bool  `json:"enabled,omitempty"     doc:"Enable / disable serving this zone. Omit to leave unchanged."`
	}
}

type UpdateDNSZoneResp struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	TTLDefault int    `json:"ttl_default"`
	Backend    string `json:"backend"`
	PushTarget string `json:"push_target"`
	Enabled    bool   `json:"enabled"`
}

type updateDNSZoneOutput struct {
	Body UpdateDNSZoneResp
}

type updateDNSRecordInput struct {
	UUID string `path:"uuid" doc:"Record uuid" minLength:"1" maxLength:"64"`
	Body struct {
		Name    string `json:"name,omitempty"    doc:"Leaf name (or '@' for apex)"`
		Type    string `json:"type,omitempty"    doc:"A / AAAA / CNAME / TXT / SRV / NS / MX"`
		Value   string `json:"value,omitempty"   doc:"Record value (IP, target, TXT data, …)"`
		TTL     int    `json:"ttl,omitempty"     doc:"TTL in seconds" minimum:"0" maximum:"86400"`
		Enabled *bool  `json:"enabled,omitempty" doc:"Enable / disable this record. Omit to leave unchanged."`
	}
}

type UpdateDNSRecordResp struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Zone    string `json:"zone"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	TTL     int    `json:"ttl"`
	Enabled bool   `json:"enabled"`
}

type updateDNSRecordOutput struct {
	Body UpdateDNSRecordResp
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

type updateSchedulingRuleInput struct {
	Name string `path:"name" doc:"Scheduling-rule name" minLength:"1" maxLength:"128"`
	Body schedulingRulePatch
}
