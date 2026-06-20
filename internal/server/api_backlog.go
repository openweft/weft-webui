package server

// api_backlog.go — webui-RPC-wiring wave : the operator actions that
// were previously documented as backlog (docs/operations/webui-rpc-
// backlog.md). Each endpoint wraps a wclient method.
//
// Auth model :
//   * Cluster-admin gated : DeleteHost, DeleteTenant, DeleteUser,
//     SetHostState, SetHostProperties, RenameNetwork,
//     RenameSecurityGroup, TriggerZombieSweep, GetZombieReport,
//     PatchImage, CleanImages.
//   * Project-scoped : SetVMProperties, WaitVM, PullImage,
//     PullImages.
//   * Read-only, auth-aware : GetHost / GetAZ / GetRack / GetUser,
//     ListImages.

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/wclient"
	weftv1 "github.com/openweft/weft-proto"
)

func mountBacklogAPI(api huma.API, scope Scope) {
	// ---- Host : delete / set-state / set-properties ----------------

	if scope.Has(ScopeAdmin) {
		huma.Register(api, huma.Operation{
			OperationID:   "delete-host-live",
			Method:        "DELETE",
			Path:          "/api/hosts/{uuid}/live",
			Summary:       "Delete a host via the live RPC (cluster-admin)",
			Description:   "Replaces the local-only DELETE /api/hosts/{uuid} stub. Operator should drain + stop VMs first ; the server doesn't migrate them automatically.",
			Tags:          []string{"inventory", "lifecycle"},
			DefaultStatus: 202,
		}, func(ctx context.Context, in *uuidPathInput) (*deleteOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			if err := live.DeleteHost(ctx, in.UUID); err != nil {
				Audit(ctx, auditLogger, "host.delete.live", "host", in.UUID, "", err, nil)
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			Audit(ctx, auditLogger, "host.delete.live", "host", in.UUID, "", nil, nil)
			out := &deleteOutput{}
			out.Body.Deleted = in.UUID
			return out, nil
		})

		huma.Register(api, huma.Operation{
			OperationID:   "set-host-state",
			Method:        "POST",
			Path:          "/api/hosts/{uuid}/state",
			Summary:       "Set a host's lifecycle state (cluster-admin)",
			Description:   "States : active | draining | down. Drain via state=draining stops accepting placements + lets existing VMs finish ; down marks the host as permanently out (operator should DeleteHost after).",
			Tags:          []string{"inventory", "lifecycle"},
			DefaultStatus: 202,
		}, func(ctx context.Context, in *setHostStateInput) (*setHostStateOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			if err := live.SetHostState(ctx, in.UUID, in.Body.State); err != nil {
				Audit(ctx, auditLogger, "host.set-state", "host", in.UUID, "", err, map[string]string{"state": in.Body.State})
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			Audit(ctx, auditLogger, "host.set-state", "host", in.UUID, "", nil, map[string]string{"state": in.Body.State})
			return &setHostStateOutput{Body: setHostStateResp{UUID: in.UUID, State: in.Body.State}}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID:   "set-host-properties",
			Method:        "PUT",
			Path:          "/api/hosts/{uuid}/properties",
			Summary:       "Replace a host's properties map (cluster-admin)",
			Description:   "Atomic replace. Empty map clears every property.",
			Tags:          []string{"inventory"},
			DefaultStatus: 200,
		}, func(ctx context.Context, in *setHostPropertiesInput) (*setHostPropertiesOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			if err := live.SetHostProperties(ctx, in.UUID, in.Body.Properties); err != nil {
				Audit(ctx, auditLogger, "host.set-properties", "host", in.UUID, "", err, nil)
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			Audit(ctx, auditLogger, "host.set-properties", "host", in.UUID, "", nil, nil)
			return &setHostPropertiesOutput{Body: setHostPropertiesResp{UUID: in.UUID, Properties: in.Body.Properties}}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID: "get-host",
			Method:      "GET",
			Path:        "/api/hosts/{uuid}/detail",
			Summary:     "Get a single host's full detail",
			Tags:        []string{"inventory"},
		}, func(ctx context.Context, in *uuidPathInput) (*hostDetailOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			row, err := live.GetHost(ctx, in.UUID, "")
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			return &hostDetailOutput{Body: row}, nil
		})
	}

	// ---- AZ / Rack / User detail (read-only) -----------------------

	if scope.Has(ScopeAdmin) {
		huma.Register(api, huma.Operation{
			OperationID: "get-az-detail",
			Method:      "GET",
			Path:        "/api/azs/{uuid}/detail",
			Summary:     "Get a single AZ's full detail (cluster-admin)",
			Tags:        []string{"inventory"},
		}, func(ctx context.Context, in *uuidPathInput) (*azDetailOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			row, err := live.GetAZ(ctx, in.UUID, "")
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			return &azDetailOutput{Body: row}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID: "get-rack-detail",
			Method:      "GET",
			Path:        "/api/racks/{uuid}/detail",
			Summary:     "Get a single rack's full detail (cluster-admin)",
			Tags:        []string{"inventory"},
		}, func(ctx context.Context, in *uuidPathInput) (*rackDetailOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			row, err := live.GetRack(ctx, in.UUID)
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			return &rackDetailOutput{Body: row}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID: "get-user-detail",
			Method:      "GET",
			Path:        "/api/users/{uuid}/detail",
			Summary:     "Get a single user's full detail (cluster-admin)",
			Tags:        []string{"identity"},
		}, func(ctx context.Context, in *uuidPathInput) (*userDetailOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			row, err := live.GetUser(ctx, in.UUID)
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			return &userDetailOutput{Body: row}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID:   "delete-user",
			Method:        "DELETE",
			Path:          "/api/users/{uuid}",
			Summary:       "Delete a user (cluster-admin)",
			Description:   "Idempotent. Removes the user from every project + tenant they were a member of as a side-effect.",
			Tags:          []string{"identity"},
			DefaultStatus: 202,
		}, func(ctx context.Context, in *uuidPathInput) (*deleteOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			if err := live.DeleteUser(ctx, in.UUID); err != nil {
				Audit(ctx, auditLogger, "user.delete", "user", in.UUID, "", err, nil)
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			Audit(ctx, auditLogger, "user.delete", "user", in.UUID, "", nil, nil)
			out := &deleteOutput{}
			out.Body.Deleted = in.UUID
			return out, nil
		})

		huma.Register(api, huma.Operation{
			OperationID:   "delete-tenant-live",
			Method:        "DELETE",
			Path:          "/api/tenants/{uuid}/live",
			Summary:       "Delete a tenant via the live RPC (cluster-admin)",
			Description:   "Cluster-wide delete. Refused server-side when projects still reference the tenant (cascade safety).",
			Tags:          []string{"tenants"},
			DefaultStatus: 202,
		}, func(ctx context.Context, in *uuidPathInput) (*deleteOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			if err := live.DeleteTenant(ctx, in.UUID); err != nil {
				Audit(ctx, auditLogger, "tenant.delete.live", "tenant", in.UUID, "", err, nil)
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			Audit(ctx, auditLogger, "tenant.delete.live", "tenant", in.UUID, "", nil, nil)
			out := &deleteOutput{}
			out.Body.Deleted = in.UUID
			return out, nil
		})
	}

	// ---- VM : SetProperties + WaitVM ------------------------------

	huma.Register(api, huma.Operation{
		OperationID:   "set-vm-properties",
		Method:        "PUT",
		Path:          "/api/microvms/{name}/properties",
		Summary:       "Replace a VM's properties map (project-scoped)",
		Tags:          []string{"microvms"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *setVMPropertiesInput) (*setVMPropertiesOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		if err := live.SetVMProperties(ctx, project, in.Name, in.Body.Properties); err != nil {
			Audit(ctx, auditLogger, "microvm.set-properties", "microvm", in.Name, "", err, map[string]string{"project": project})
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		Audit(ctx, auditLogger, "microvm.set-properties", "microvm", in.Name, "", nil, map[string]string{"project": project})
		return &setVMPropertiesOutput{Body: setVMPropertiesResp{Name: in.Name, Properties: in.Body.Properties}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "wait-vm",
		Method:      "GET",
		Path:        "/api/microvms/{name}/wait",
		Summary:     "Block until a VM reaches running (project-scoped)",
		Description: "Long-poll. Returns the VM's IP once it transitions to running, or 504 on timeout.",
		Tags:        []string{"microvms", "lifecycle"},
	}, func(ctx context.Context, in *waitVMInput) (*waitVMOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		timeout := in.TimeoutSeconds
		if timeout <= 0 {
			timeout = 60
		}
		ip, werr := live.WaitVM(ctx, project, in.Name, timeout)
		if werr != nil {
			return nil, huma.Error504GatewayTimeout("wait: " + werr.Error())
		}
		return &waitVMOutput{Body: waitVMResp{Name: in.Name, IP: ip}}, nil
	})

	// Rename network is already wired in api_networking.go (now
	// calls the live RPC). Rename security-group : add it under a
	// distinct route since the existing UI endpoint at /api/security-
	// groups/{key} hits the local store. The new live path uses
	// /api/security-groups/{uuid}/rename.

	if scope.Has(ScopeAdmin) {
		huma.Register(api, huma.Operation{
			OperationID: "rename-security-group-live",
			Method:      "PUT",
			Path:        "/api/security-groups/{uuid}/rename",
			Summary:     "Rename a security group via the live RPC (admin)",
			Tags:        []string{"security"},
		}, func(ctx context.Context, in *renameUUIDInput) (*renameUUIDOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			if err := live.RenameSecurityGroup(ctx, in.UUID, in.Body.NewName); err != nil {
				Audit(ctx, auditLogger, "sg.rename", "security-group", in.UUID, "", err, map[string]string{"new_name": in.Body.NewName})
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			Audit(ctx, auditLogger, "sg.rename", "security-group", in.UUID, "", nil, map[string]string{"new_name": in.Body.NewName})
			return &renameUUIDOutput{Body: renameUUIDResp{UUID: in.UUID, NewName: in.Body.NewName}}, nil
		})

		// ---- Zombie reconciler --------------------------------------

		huma.Register(api, huma.Operation{
			OperationID: "get-zombie-report",
			Method:      "GET",
			Path:        "/api/admin/zombies",
			Summary:     "Get the zombie GC report (cluster-admin)",
			Tags:        []string{"admin"},
		}, func(ctx context.Context, _ *struct{}) (*zombieReportOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			rep, err := live.GetZombieReport(ctx)
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			return &zombieReportOutput{Body: zombieReportFromWClient(rep)}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID:   "trigger-zombie-sweep",
			Method:        "POST",
			Path:          "/api/admin/zombies/sweep",
			Summary:       "Trigger an immediate zombie GC sweep (cluster-admin)",
			Tags:          []string{"admin"},
			DefaultStatus: 200,
		}, func(ctx context.Context, _ *struct{}) (*zombieReportOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			u := auth.UserFromContext(ctx)
			rep, err := live.TriggerZombieSweep(ctx)
			Audit(ctx, auditLogger, "zombie.sweep", "cluster", "", "", err, nil)
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			_ = u
			return &zombieReportOutput{Body: zombieReportFromWClient(rep)}, nil
		})

		// ---- Image management ---------------------------------------

		huma.Register(api, huma.Operation{
			OperationID: "list-images",
			Method:      "GET",
			Path:        "/api/admin/images",
			Summary:     "List images in the agent cache (cluster-admin)",
			Tags:        []string{"admin", "images"},
		}, func(ctx context.Context, _ *struct{}) (*imageListOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			rows, err := live.ListImages(ctx)
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			return &imageListOutput{Body: imageListBody{Images: rows}}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID:   "pull-image",
			Method:        "POST",
			Path:          "/api/admin/images/pull",
			Summary:       "Pull one image into the agent cache (cluster-admin)",
			Tags:          []string{"admin", "images"},
			DefaultStatus: 202,
		}, func(ctx context.Context, in *pullImageInput) (*okOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			if err := live.PullImage(ctx, in.Body.URL, in.Body.Checksum); err != nil {
				Audit(ctx, auditLogger, "image.pull", "image", in.Body.URL, "", err, nil)
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			Audit(ctx, auditLogger, "image.pull", "image", in.Body.URL, "", nil, nil)
			return &okOutput{Body: okBody{OK: true}}, nil
		})

		huma.Register(api, huma.Operation{
			OperationID:   "clean-images",
			Method:        "POST",
			Path:          "/api/admin/images/clean",
			Summary:       "GC unused images from the agent cache (cluster-admin)",
			Description:   "dry_run=true returns the list of candidates without deleting.",
			Tags:          []string{"admin", "images"},
			DefaultStatus: 200,
		}, func(ctx context.Context, in *cleanImagesInput) (*cleanImagesOutput, error) {
			if err := requireLiveCtx(); err != nil {
				return nil, err
			}
			deleted, err := live.CleanImages(ctx, in.Body.ConfigDir, in.Body.DryRun)
			Audit(ctx, auditLogger, "image.clean", "cluster", "", "", err, map[string]string{"dry_run": boolStr(in.Body.DryRun)})
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			return &cleanImagesOutput{Body: cleanImagesResp{Deleted: deleted, DryRun: in.Body.DryRun}}, nil
		})
	}
}

// ---- Types ---------------------------------------------------------

type setHostStateInput struct {
	UUID string `path:"uuid"`
	Body struct {
		State string `json:"state" enum:"active,draining,down"`
	}
}

type setHostStateResp struct {
	UUID  string `json:"uuid"`
	State string `json:"state"`
}

type setHostStateOutput struct {
	Body setHostStateResp
}

type setHostPropertiesInput struct {
	UUID string `path:"uuid"`
	Body struct {
		Properties map[string]string `json:"properties"`
	}
}

type setHostPropertiesResp struct {
	UUID       string            `json:"uuid"`
	Properties map[string]string `json:"properties"`
}

type setHostPropertiesOutput struct {
	Body setHostPropertiesResp
}

type hostDetailOutput struct {
	Body map[string]any
}

type azDetailOutput struct {
	Body map[string]any
}

type rackDetailOutput struct {
	Body map[string]any
}

type userDetailOutput struct {
	Body map[string]any
}

type setVMPropertiesInput struct {
	Name    string `path:"name"`
	Project string `query:"project"`
	Body    struct {
		Properties map[string]string `json:"properties"`
	}
}

type setVMPropertiesResp struct {
	Name       string            `json:"name"`
	Properties map[string]string `json:"properties"`
}

type setVMPropertiesOutput struct {
	Body setVMPropertiesResp
}

type waitVMInput struct {
	Name           string `path:"name"`
	Project        string `query:"project"`
	TimeoutSeconds int    `query:"timeout_seconds" doc:"Long-poll deadline ; 0 = server default (60)"`
}

type waitVMResp struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type waitVMOutput struct {
	Body waitVMResp
}

type renameUUIDInput struct {
	UUID string `path:"uuid"`
	Body struct {
		NewName string `json:"new_name" minLength:"1" maxLength:"128"`
	}
}

type renameUUIDResp struct {
	UUID    string `json:"uuid"`
	NewName string `json:"new_name"`
}

type renameUUIDOutput struct {
	Body renameUUIDResp
}

type zombieReportBody struct {
	Zombies         []map[string]any `json:"zombies"`
	DeletedTotal    uint64           `json:"deleted_total"`
	LastSweepUnixNs int64            `json:"last_sweep_unix_ns"`
	ByKind          map[string]int32 `json:"by_kind"`
}

type zombieReportOutput struct {
	Body zombieReportBody
}

func zombieReportFromWClient(r *wclient.ZombieReport) zombieReportBody {
	zs := make([]map[string]any, 0, len(r.Zombies))
	for _, z := range r.Zombies {
		zs = append(zs, map[string]any{
			"uuid":               z.UUID,
			"name":               z.Name,
			"project_uuid":       z.ProjectUUID,
			"host_uuid":          z.HostUUID,
			"kind":               z.Kind,
			"reason":             z.Reason,
			"deployment_type":    z.DeploymentType,
			"detected_at_ns":     z.DetectedUnixNs,
			"host_down_since_ns": z.HostDownUnixNs,
		})
	}
	return zombieReportBody{
		Zombies:         zs,
		DeletedTotal:    r.DeletedTotal,
		LastSweepUnixNs: r.LastSweepUnixNs,
		ByKind:          r.ZombiesByKind,
	}
}

type imageListBody struct {
	Images []map[string]any `json:"images"`
}

type imageListOutput struct {
	Body imageListBody
}

type pullImageInput struct {
	Body struct {
		URL      string `json:"url" minLength:"1"`
		Checksum string `json:"checksum,omitempty"`
	}
}

type cleanImagesInput struct {
	Body struct {
		ConfigDir string `json:"config_dir,omitempty"`
		DryRun    bool   `json:"dry_run"`
	}
}

type cleanImagesResp struct {
	Deleted []string `json:"deleted"`
	DryRun  bool     `json:"dry_run"`
}

type cleanImagesOutput struct {
	Body cleanImagesResp
}

// okBody / okOutput live in api_misc.go ; reuse those.

// Keep weftv1 referenced even if no path uses it directly so build
// stays stable when handlers are toggled behind scope flags.
var _ = weftv1.ListVMsRequest{}
