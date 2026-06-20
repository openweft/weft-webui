package wclient

// wclient_batch.go — the v0.4.42+ webui-RPC-wiring wave. Each method
// follows the existing measured() + dial() + rpcCtx pattern from
// wclient.go.

import (
	"context"

	weftv1 "github.com/openweft/weft-proto"
)

// ---- Host management ----------------------------------------------

func (c *Client) DeleteHost(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteHost", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteHost(cctx, &weftv1.DeleteHostRequest{Uuid: uuid})
	return err
}

func (c *Client) SetHostState(ctx context.Context, uuid, state string) (retErr error) {
	defer c.measured("SetHostState", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetHostState(cctx, &weftv1.SetHostStateRequest{Uuid: uuid, State: state})
	return err
}

func (c *Client) SetHostProperties(ctx context.Context, uuid string, props map[string]string) (retErr error) {
	defer c.measured("SetHostProperties", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetHostProperties(cctx, &weftv1.SetHostPropertiesRequest{Uuid: uuid, Properties: props})
	return err
}

func (c *Client) GetHost(ctx context.Context, uuid, hostname string) (info map[string]any, retErr error) {
	defer c.measured("GetHost", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetHost(cctx, &weftv1.GetHostRequest{Uuid: uuid, Hostname: hostname})
	if err != nil {
		return nil, err
	}
	if resp.Host == nil {
		return map[string]any{}, nil
	}
	return map[string]any{
		"uuid":            resp.Host.Uuid,
		"hostname":        resp.Host.Hostname,
		"az":              resp.Host.Az,
		"rack":            resp.Host.Rack,
		"hypervisor":      resp.Host.Hypervisor,
		"architecture":    resp.Host.Architecture,
		"state":           resp.Host.State,
		"properties":      resp.Host.Properties,
		"created_at_ns":   resp.Host.CreatedAtUnixNs,
		"last_seen_at_ns": resp.Host.LastSeenAtUnixNs,
	}, nil
}

// ---- AZ / Rack -----------------------------------------------------

func (c *Client) GetAZ(ctx context.Context, uuid, code string) (row map[string]any, retErr error) {
	defer c.measured("GetAZ", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetAZ(cctx, &weftv1.GetAZRequest{Uuid: uuid, Code: code})
	if err != nil {
		return nil, err
	}
	if resp.Az == nil {
		return map[string]any{}, nil
	}
	return map[string]any{
		"uuid": resp.Az.Uuid,
		"code": resp.Az.Code,
		"name": resp.Az.Name,
	}, nil
}

func (c *Client) GetRack(ctx context.Context, uuid string) (row map[string]any, retErr error) {
	defer c.measured("GetRack", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetRack(cctx, &weftv1.GetRackRequest{Uuid: uuid})
	if err != nil {
		return nil, err
	}
	if resp.Rack == nil {
		return map[string]any{}, nil
	}
	return map[string]any{
		"uuid":    resp.Rack.Uuid,
		"code":    resp.Rack.Code,
		"az_uuid": resp.Rack.AzUuid,
	}, nil
}

// ---- User / Tenant -------------------------------------------------

func (c *Client) GetUser(ctx context.Context, uuid string) (row map[string]any, retErr error) {
	defer c.measured("GetUser", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetUser(cctx, &weftv1.GetUserRequest{Uuid: uuid})
	if err != nil {
		return nil, err
	}
	if resp.User == nil {
		return map[string]any{}, nil
	}
	return map[string]any{
		"uuid":         resp.User.Uuid,
		"email":        resp.User.Email,
		"display_name": resp.User.DisplayName,
		"oidc_issuer":  resp.User.OidcIssuer,
		"oidc_subject": resp.User.OidcSubject,
	}, nil
}

func (c *Client) DeleteUser(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteUser", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteUser(cctx, &weftv1.DeleteUserRequest{Uuid: uuid})
	return err
}

func (c *Client) DeleteTenant(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteTenant", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteTenant(cctx, &weftv1.DeleteTenantRequest{Uuid: uuid})
	return err
}

// ---- VM ------------------------------------------------------------

func (c *Client) SetVMProperties(ctx context.Context, project, name string, props map[string]string) (retErr error) {
	defer c.measured("SetVMProperties", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetVMProperties(cctx, &weftv1.SetVMPropertiesRequest{Project: project, Name: name, Properties: props})
	return err
}

// WaitVM blocks (up to timeoutSeconds) until the VM reaches a running
// state. Returns the VM's IP on success.
func (c *Client) WaitVM(ctx context.Context, project, name string, timeoutSeconds int) (ip string, retErr error) {
	defer c.measured("WaitVM", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.WaitVM(cctx, &weftv1.WaitVMRequest{Project: project, Name: name, TimeoutSeconds: int32(timeoutSeconds)})
	if err != nil {
		return "", err
	}
	return resp.Ip, nil
}

// ---- Network / SG rename ------------------------------------------

func (c *Client) RenameNetwork(ctx context.Context, uuid, newName string) (retErr error) {
	defer c.measured("RenameNetwork", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RenameNetwork(cctx, &weftv1.RenameNetworkRequest{Uuid: uuid, NewName: newName})
	return err
}

func (c *Client) RenameSecurityGroup(ctx context.Context, uuid, newName string) (retErr error) {
	defer c.measured("RenameSecurityGroup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RenameSecurityGroup(cctx, &weftv1.RenameSecurityGroupRequest{Uuid: uuid, NewName: newName})
	return err
}

// ---- Zombie reconciler --------------------------------------------

type ZombieReport struct {
	Zombies         []ZombieEntry
	DeletedTotal    uint64
	LastSweepUnixNs int64
	ZombiesByKind   map[string]int32
}

type ZombieEntry struct {
	UUID           string
	Name           string
	ProjectUUID    string
	HostUUID       string
	Kind           string
	Reason         string
	DeploymentType string
	DetectedUnixNs int64
	HostDownUnixNs int64
}

func (c *Client) GetZombieReport(ctx context.Context) (out *ZombieReport, retErr error) {
	defer c.measured("GetZombieReport", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetZombieReport(cctx, &weftv1.GetZombieReportRequest{})
	if err != nil {
		return nil, err
	}
	return &ZombieReport{
		Zombies:         zombieEntriesFromProto(resp.Zombies),
		DeletedTotal:    resp.DeletedTotal,
		LastSweepUnixNs: resp.LastSweepAtUnixNs,
		ZombiesByKind:   resp.ZombiesByKind,
	}, nil
}

func (c *Client) TriggerZombieSweep(ctx context.Context) (out *ZombieReport, retErr error) {
	defer c.measured("TriggerZombieSweep", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.TriggerZombieSweep(cctx, &weftv1.TriggerZombieSweepRequest{})
	if err != nil {
		return nil, err
	}
	return &ZombieReport{
		Zombies:         zombieEntriesFromProto(resp.Zombies),
		DeletedTotal:    resp.DeletedTotal,
		LastSweepUnixNs: resp.LastSweepAtUnixNs,
		ZombiesByKind:   resp.ZombiesByKind,
	}, nil
}

func zombieEntriesFromProto(in []*weftv1.ZombieEntry) []ZombieEntry {
	out := make([]ZombieEntry, len(in))
	for i, z := range in {
		out[i] = ZombieEntry{
			UUID: z.Uuid, Name: z.Name, ProjectUUID: z.ProjectUuid, HostUUID: z.HostUuid,
			Kind: z.Kind, Reason: z.Reason, DeploymentType: z.DeploymentType,
			DetectedUnixNs: z.DetectedAtUnixNs, HostDownUnixNs: z.HostDownSinceUnixNs,
		}
	}
	return out
}

// ---- Image management ---------------------------------------------

func (c *Client) ListImages(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListImages", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListImages(cctx, &weftv1.ListImagesRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.Images))
	for _, img := range resp.Images {
		out = append(out, map[string]any{
			"url":        img.Url,
			"name":       img.Name,
			"format":     img.Format,
			"size_bytes": img.SizeBytes,
		})
	}
	return out, nil
}

// PullImage triggers an OCI/HTTP pull of one image. url is the image
// reference ; checksum is optional (a URL to a checksum file weft
// verifies against the downloaded blob).
func (c *Client) PullImage(ctx context.Context, url, checksum string) (retErr error) {
	defer c.measured("PullImage", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.PullImage(cctx, &weftv1.PullImageRequest{Url: url, Checksum: checksum})
	return err
}

// PullImages reads images.hcl in configDir and pulls every image
// declared there ; parallel caps concurrent fetches (0 = sequential).
func (c *Client) PullImages(ctx context.Context, configDir string, parallel int) (retErr error) {
	defer c.measured("PullImages", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.PullImages(cctx, &weftv1.PullImagesRequest{ConfigDir: configDir, Parallel: int32(parallel)})
	return err
}

// CleanImages garbage-collects cache entries no live VM references.
// dryRun returns the list of would-be-deleted names without
// touching disk.
func (c *Client) CleanImages(ctx context.Context, configDir string, dryRun bool) (deleted []string, retErr error) {
	defer c.measured("CleanImages", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CleanImages(cctx, &weftv1.CleanImagesRequest{ConfigDir: configDir, DryRun: dryRun})
	if err != nil {
		return nil, err
	}
	return resp.Deleted, nil
}

// PatchImage applies in-place patches to a cached image (used by the
// platform team to stitch architecture-specific fixups onto base
// images so cloned VMs inherit them without per-instance rewrites).
// The full surface (file_ops, delete_ops, mod_ops) lives on the
// proto ; the dashboard exposes the most common subset — adding a
// single file via file_ops — through this helper.
func (c *Client) PatchImage(ctx context.Context, url string, fileOps []*weftv1.DiskFileOp) (retErr error) {
	defer c.measured("PatchImage", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.PatchImage(cctx, &weftv1.PatchImageRequest{Url: url, FileOps: fileOps})
	return err
}

// ---- Federation / sharing ------------------------------------------

func (c *Client) PublishShareToProject(ctx context.Context, projectUUID string, mount *weftv1.ShareMount) (retErr error) {
	defer c.measured("PublishShareToProject", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.PublishShareToProject(cctx, &weftv1.PublishShareToProjectRequest{
		ProjectUuid: projectUUID,
		Mount:       mount,
	})
	return err
}
