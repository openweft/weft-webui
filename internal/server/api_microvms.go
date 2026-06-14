// api_microvms.go — VM lifecycle + inspect endpoints. All require a
// live weft-agent connection (mock-mode returns 503) since they
// mutate or read real cluster state.

package server

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/wclient"
)

// requireLiveCtx is the huma-side analogue of requireLive. Returns
// nil when live is wired ; a 503 huma error otherwise.
func requireLiveCtx() error {
	if live == nil {
		return huma.Error503ServiceUnavailable("no live weft daemon configured ; start the webui with --weft-socket")
	}
	return nil
}

// resolveVMProjectCtx returns the project to use for a VM mutation :
// the session's selected project, falling back to the explicit
// ?project= query param. Errors out when neither is available — a
// VM mutation needs a project to disambiguate the name.
func resolveVMProjectCtx(ctx context.Context, queryProject string) (string, error) {
	if u := auth.UserFromContext(ctx); u != nil && u.Project != "" {
		if queryProject != "" {
			return queryProject, nil
		}
		return u.Project, nil
	}
	if queryProject != "" {
		return queryProject, nil
	}
	return "", huma.Error400BadRequest("project is required (set scope via /api/session/scope or pass ?project=...)")
}

// userActionCtx records a per-user action counter (admin telemetry).
// No-op when telemetry is off or the caller is anonymous.
func userActionCtx(ctx context.Context, action string) {
	if metrics == nil {
		return
	}
	if u := auth.UserFromContext(ctx); u != nil {
		metrics.UserAction(u.Subject, action)
	}
}

func mountMicroVMLifecycleAPI(api huma.API) {
	// --- Mutators (Start / Stop / Delete) ---------------------------

	huma.Register(api, huma.Operation{
		OperationID:   "start-vm",
		Method:        "POST",
		Path:          "/api/microvms/{name}/start",
		Summary:       "Start a stopped VM",
		Description:   "Acceptance-style endpoint — returns 202 with state=starting ; poll status for the actual transition.",
		Tags:          []string{"microvms", "lifecycle"},
		DefaultStatus: 202,
	}, func(ctx context.Context, in *vmProjectInput) (*vmStateOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		cerr := live.StartVM(ctx, in.Name, project)
		Audit(ctx, auditLogger, "microvm.start", "microvm", in.Name, "", cerr, map[string]string{"project": project})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "microvm.start")
		return &vmStateOutput{Body: vmStateBody{Name: in.Name, State: "starting"}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "stop-vm",
		Method:        "POST",
		Path:          "/api/microvms/{name}/stop",
		Summary:       "Stop a running VM",
		Tags:          []string{"microvms", "lifecycle"},
		DefaultStatus: 202,
	}, func(ctx context.Context, in *vmProjectInput) (*vmStateOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		cerr := live.StopVM(ctx, in.Name, project)
		Audit(ctx, auditLogger, "microvm.stop", "microvm", in.Name, "", cerr, map[string]string{"project": project})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "microvm.stop")
		return &vmStateOutput{Body: vmStateBody{Name: in.Name, State: "stopping"}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-vm",
		Method:        "DELETE",
		Path:          "/api/microvms/{name}",
		Summary:       "Delete a VM (irreversible)",
		Tags:          []string{"microvms", "lifecycle"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *vmProjectInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		cerr := live.DeleteVM(ctx, in.Name, project)
		Audit(ctx, auditLogger, "microvm.delete", "microvm", in.Name, "", cerr, map[string]string{"project": project})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "microvm.delete")
		return nil, nil
	})

	// --- Inspect (status / timings / logs) --------------------------

	huma.Register(api, huma.Operation{
		OperationID: "vm-status",
		Method:      "GET",
		Path:        "/api/microvms/{name}/status",
		Summary:     "Read a VM's current status",
		Tags:        []string{"microvms", "inspect"},
	}, func(ctx context.Context, in *vmProjectInput) (*vmStatusOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		info, cerr := live.VMStatus(ctx, in.Name, project)
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		return &vmStatusOutput{Body: *info}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "vm-timings",
		Method:      "GET",
		Path:        "/api/microvms/{name}/timings",
		Summary:     "Read a VM's boot/lifecycle event timings",
		Tags:        []string{"microvms", "inspect"},
	}, func(ctx context.Context, in *vmProjectInput) (*vmTimingsOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		events, cerr := live.VMTimings(ctx, in.Name, project)
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		return &vmTimingsOutput{Body: events}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "vm-logs",
		Method:      "GET",
		Path:        "/api/microvms/{name}/logs",
		Summary:     "Read the VM's serial console log",
		Description: "?tail caps the response to the last N bytes (default 65536). A VM with a giant console.log doesn't blow up the SPA on first open.",
		Tags:        []string{"microvms", "inspect"},
	}, func(ctx context.Context, in *vmLogsInput) (*vmLogsOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		tail := in.Tail
		if tail <= 0 {
			tail = 65536
		}
		out, cerr := live.VMLogs(ctx, in.Name, project, tail)
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		return &vmLogsOutput{Body: *out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "vm-network-diag",
		Method:      "GET",
		Path:        "/api/microvms/{name}/network-diag",
		Summary:     "Aggregate networks + floating IPs visible to a VM",
		Description: "Mirrors `weft network diag <vm-name>` : lists networks in scope plus every floating IP mapped to this VM. Read-only.",
		Tags:        []string{"microvms", "inspect"},
	}, func(ctx context.Context, in *vmProjectInput) (*vmNetworkDiagOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		nets, _, nerr := live.ListNetworks(ctx, project, wclient.ListOpts{Limit: 200})
		if nerr != nil {
			return nil, huma.Error502BadGateway("ListNetworks: " + nerr.Error())
		}
		fips, _, ferr := live.ListFloatingIPs(ctx, project, wclient.ListOpts{Limit: 500})
		if ferr != nil {
			return nil, huma.Error502BadGateway("ListFloatingIPs: " + ferr.Error())
		}
		mapped := make([]map[string]any, 0, 4)
		for _, f := range fips {
			if s, _ := f["mapped_to"].(string); s == in.Name {
				mapped = append(mapped, f)
			}
		}
		// Ports : best-effort — a fresh VM may not have any yet.
		ports, perr := live.ListPortsForVM(ctx, "", in.Name, project)
		if perr != nil {
			// Don't fail the whole diag if Ports aren't reachable
			// (e.g. older daemon without the RPC). Surface as a
			// warning slot instead — empty list + non-fatal.
			ports = nil
		}
		return &vmNetworkDiagOutput{Body: VMNetworkDiagBody{
			VMName:      in.Name,
			Networks:    nets,
			FloatingIPs: mapped,
			Ports:       ports,
		}}, nil
	})

	// --- Create ---------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID:   "create-vm",
		Method:        "POST",
		Path:          "/api/microvms",
		Summary:       "Create a VM (flavor + image + scheduling)",
		Description:   "A microVM is the combination of a flavor (sizing envelope), an image, and a scheduling policy ; cpu/ram/disk are not independent fields — they're resolved from the flavor catalogue server-side. Ingress (floating-ip / load-balancer) is best-effort post-create ; failures show up as `warnings` rather than a hard error. First-boot provisioning stamps weft.boot/* properties read by the in-guest weft-microvm-agent (pull model).",
		Tags:          []string{"microvms", "lifecycle"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createVMInput) (*createVMOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		if in.Body.Name == "" || in.Body.Image == "" {
			return nil, huma.Error400BadRequest("name and image are required")
		}
		if in.Body.Flavor == "" {
			return nil, huma.Error400BadRequest("flavor is required (microVM = flavor + image + scheduling)")
		}
		spec, ok := resolveFlavor(ctx, in.Body.Flavor)
		if !ok {
			return nil, huma.Error400BadRequest("unknown flavor: " + in.Body.Flavor)
		}
		if spec.CPU == 0 || spec.MemMB == 0 {
			return nil, huma.Error400BadRequest("flavor " + in.Body.Flavor + " has incomplete spec (cpu/ram)")
		}
		if cerr := live.CreateVM(ctx, wclient.CreateVMOpts{
			Name: in.Body.Name, Image: in.Body.Image, Project: project,
			CPU: spec.CPU, MemMB: spec.MemMB, DiskGB: spec.DiskGB,
			SchedulingRule: in.Body.SchedulingRule,
			Network:        in.Body.Network,
		}); cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "microvm.create")

		var warnings []string
		switch in.Body.IngressKind {
		case "", "none":
			// nothing to do
		case "floating_ip":
			if in.Body.IngressFloatingIP == "" {
				warnings = append(warnings, "ingress=floating_ip but no IngressFloatingIP UUID — allocation flow not wired here ; allocate then Map from the Floating IPs page")
				break
			}
			if err := live.MapFloatingIP(ctx, in.Body.IngressFloatingIP, "vm", in.Body.Name, 0); err != nil {
				warnings = append(warnings, "MapFloatingIP: "+err.Error())
			}
		case "loadbalancer":
			if in.Body.IngressLoadBalancer == "" {
				warnings = append(warnings, "ingress=loadbalancer but no IngressLoadBalancer UUID")
				break
			}
			if liveNet == nil {
				warnings = append(warnings, "ingress=loadbalancer requires --weft-network-socket")
				break
			}
			rows, _, lerr := liveNet.ListLoadBalancers(ctx, project, wclient.ListOpts{Limit: 1000})
			if lerr != nil {
				warnings = append(warnings, "list LBs: "+lerr.Error())
				break
			}
			backends := []string{in.Body.Name}
			for _, row := range rows {
				if u, _ := row["uuid"].(string); u != in.Body.IngressLoadBalancer {
					continue
				}
				if cur, _ := row["backends"].(string); cur != "" {
					for _, b := range strings.Split(cur, ",") {
						b = strings.TrimSpace(b)
						if b != "" && b != in.Body.Name {
							backends = append([]string{b}, backends...)
						}
					}
				}
				break
			}
			if err := liveNet.SetLoadBalancerBackends(ctx, in.Body.IngressLoadBalancer, backends); err != nil {
				warnings = append(warnings, "SetLoadBalancerBackends: "+err.Error())
			}
		default:
			warnings = append(warnings, "unknown ingress kind: "+in.Body.IngressKind)
		}

		if in.Body.Provisioning != nil {
			p := in.Body.Provisioning
			switch p.SourceKind {
			case "", "none":
				if strings.TrimSpace(p.Script) != "" {
					writeBootProperty(in.Body.Name, "weft.boot/script", p.Script)
				}
			case "git", "oci":
				writeBootProperty(in.Body.Name, "weft.boot/source.kind", p.SourceKind)
				writeBootProperty(in.Body.Name, "weft.boot/source.url", p.SourceURL)
				if p.SourceRef != "" {
					writeBootProperty(in.Body.Name, "weft.boot/source.ref", p.SourceRef)
				}
				if strings.TrimSpace(p.Script) != "" {
					writeBootProperty(in.Body.Name, "weft.boot/script", p.Script)
				}
			default:
				warnings = append(warnings, "unknown provisioning source kind: "+p.SourceKind)
			}
		}

		out := &createVMOutput{}
		out.Body.Name = in.Body.Name
		out.Body.Project = project
		out.Body.Warnings = warnings
		return out, nil
	})
}

// vmProjectInput is the common shape for per-VM mutators : VM name
// in path, optional ?project= override in query.
type vmProjectInput struct {
	Name    string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	Project string `query:"project" doc:"Override the session project"`
}

type vmStateBody struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type vmStateOutput struct {
	Body vmStateBody
}

type vmLogsInput struct {
	Name    string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	Project string `query:"project" doc:"Override the session project"`
	Tail    int64  `query:"tail" doc:"Cap the response to the last N bytes (0 = default 65536)" minimum:"0"`
}

type createVMInput struct {
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Name              string `json:"name" doc:"VM name (must be unique within the project)" minLength:"1" maxLength:"128"`
		Image             string `json:"image" doc:"OCI reference or registered image name"`
		Flavor            string `json:"flavor" doc:"Catalogue flavor name (resolves to cpu/ram/disk server-side)"`
		SchedulingRule    string `json:"scheduling_rule,omitempty" doc:"Nominal binding to a SchedulingRule (k8s PVC volumeName pattern)"`
		Network           string `json:"network,omitempty" doc:"Network to attach (label ; weft-network reconcile loop performs AttachVM)"`
		IngressKind       string `json:"ingress_kind,omitempty" doc:"Best-effort ingress setup" enum:"none,floating_ip,loadbalancer"`
		IngressFloatingIP string `json:"ingress_floating_ip,omitempty"`
		IngressLoadBalancer string `json:"ingress_load_balancer,omitempty"`
		Provisioning      *struct {
			SourceKind string `json:"source_kind" doc:"First-boot payload source" enum:"none,git,oci"`
			SourceURL  string `json:"source_url"`
			SourceRef  string `json:"source_ref,omitempty"`
			Script     string `json:"script,omitempty"`
		} `json:"provisioning,omitempty"`
	}
}

// CreateVMResp is what /api/microvms POST returns : the VM's name +
// resolved project, plus best-effort warnings from the post-create
// orchestration (ingress setup, provisioning property writes). The
// VM itself is created regardless ; warnings list the follow-ups
// the operator can retry.
type CreateVMResp struct {
	Name     string   `json:"name"`
	Project  string   `json:"project"`
	Warnings []string `json:"warnings,omitempty" doc:"Best-effort post-create steps that didn't complete cleanly. The VM itself is created ; these are follow-ups the operator can retry."`
}

type createVMOutput struct {
	Body CreateVMResp
}

// vmStatusOutput / vmTimingsOutput / vmLogsOutput surface the wclient
// types directly so the OpenAPI schema gains real VMInfo /
// VMTimingEvent / VMLogsResult shapes instead of `any`.
type vmStatusOutput  struct{ Body wclient.VMInfo }
type vmTimingsOutput struct{ Body []wclient.VMTimingEvent }
type vmLogsOutput    struct{ Body wclient.VMLogsResult }

// VMNetworkDiagBody mirrors the CLI `weft network diag` output : every
// network in scope, the floating IPs filtered to mapped_to==vm, plus
// every Port currently attached to the VM (MAC/IP/security-groups
// drive the host-side portsec anti-spoof reconciler).
type VMNetworkDiagBody struct {
	VMName      string           `json:"vm_name"`
	Networks    []map[string]any `json:"networks"`
	FloatingIPs []map[string]any `json:"floating_ips"`
	Ports       []map[string]any `json:"ports,omitempty"`
}
type vmNetworkDiagOutput struct{ Body VMNetworkDiagBody }

// passthroughOutput is the response shape for endpoints that
// forward whatever the live client returned without re-typing.
// Today both VMStatus and VMTimings return rich proto-derived
// structs ; rather than mirror them all (a lot of typing for
// handlers that are passthroughs to weft-agent), we declare an
// `any` body. The OpenAPI shows "any" for these ; when we shape
// them later the contract narrows without a wire break.
type passthroughOutput struct {
	Body any
}
