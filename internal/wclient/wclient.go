// Package wclient is the thin adapter between the weft-webui HTTP handlers
// and the real weft control-plane gRPC API (weft-agent / weft-client). It hides
// dialing + connection caching, and translates proto messages into the same
// map[string]any row shape the dashboard's frontend already consumes.
//
// When the server is started without --weft-socket, the live client is nil
// and every handler falls back to its mock implementation. When a socket
// is provided, the resources we have wired call into the daemon ; the
// others stay mock until they're wired one at a time.
package wclient

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/telemetry"
	weftclient "github.com/openweft/weft-client"
	weftv1 "github.com/openweft/weft-proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func tsDate(unixNs int64) string {
	if unixNs <= 0 {
		return ""
	}
	return time.Unix(0, unixNs).UTC().Format("2006-01-02")
}

// Client is a lazily-dialed gRPC client to a local (or SSH-tunneled) weft
// daemon. Safe for concurrent use — every method acquires the connection on
// first call and reuses it for the lifetime of the process.
//
// Metrics is optional ; when set, every call is timed and counted
// (weft_webui_grpc_*). nil means "no telemetry" — same code path,
// zero allocations on the hot loop.
type Client struct {
	socket  string
	mu      sync.Mutex
	conn    *grpc.ClientConn
	rpc     weftv1.WeftAgentClient
	Metrics *telemetry.Recorder
}

// New builds a client that will dial socket on the first RPC. socket follows
// the weft-client convention : a unix path (e.g. ~/.weft/weft.sock —
// some older deployments still see the legacy ~/.weft/weft.sock) or an
// ssh:// URL routed through the SSH transport.
func New(socket string) *Client { return &Client{socket: socket} }

// measured returns a deferable closure that records the call's
// duration + canonical status when the method returns. Use as :
//
//	defer c.measured("ListProjects", &retErr)()
//
// The pointer is the only way to read the final named-return value
// from a defer ; passing it directly captures the zero value.
func (c *Client) measured(method string, errPtr *error) func() {
	if c.Metrics == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		st := "ok"
		if errPtr != nil && *errPtr != nil {
			st = status.Code(*errPtr).String()
		}
		c.Metrics.ObserveGRPC(method, st, time.Since(start))
	}
}

func (c *Client) dial() (weftv1.WeftAgentClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpc != nil {
		return c.rpc, nil
	}
	// We install our own interceptors here so the bearer comes from the
	// request context (one access token per signed-in user) rather than
	// the on-disk cache weftclient.CachedTokenSource() reads. Weftclient's
	// Client() already installs its own bearer interceptor on top — both
	// stamp `authorization` metadata, and the per-request one wins when
	// it sets a value (metadata.AppendToOutgoingContext concatenates,
	// weft-agent's validator accepts the first valid bearer).
	rpc, conn, err := weftclient.Client(c.socket)
	if err != nil {
		return nil, err
	}
	c.rpc, c.conn = rpc, conn
	return c.rpc, nil
}

// withBearer derives a new context that carries the signed-in user's
// access token as gRPC outgoing metadata. No user / no token = the
// context is returned unchanged ; weft-agent then sees an unauthenticated
// call and decides per its auth-mode whether to reject it.
//
// Bypassing this when a token is already present (e.g. a daemon
// running in dev-mode that ignores auth) keeps the webui usable
// against a no-auth weft-agent without crashing on every list call.
func withBearer(ctx context.Context) context.Context {
	u := auth.UserFromContext(ctx)
	if u == nil || u.AccessToken == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+u.AccessToken)
}

// Close releases the cached connection (best-effort).
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.rpc = nil, nil
	return err
}

// ctx returns a context derived from the caller with a short RPC deadline.
func rpcCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}

// ListOpts is the universal page knob threaded through every List*
// call. Limit==0 means "server default" (the daemon currently maps that
// to 50 ; the proto pins the upper bound at 1000). PageToken=="" =
// first page. Both fields are pass-through to the proto request ; the
// daemon owns the cursor semantics.
type ListOpts struct {
	Limit     int32
	PageToken string
}

// Each List* below returns dashboard rows whose keys match the columns the
// frontend already declares for that resource, plus the next-page token
// the daemon hands back. Mock rows share the same shape (see
// internal/server/resources.go), so the table renders either.

func (c *Client) ListProjects(ctx context.Context, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListProjects", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListProjects(cctx, &weftv1.ListProjectsRequest{
		Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	if resp == nil {
		return nil, "", errors.New("nil ListProjects response")
	}
	out := make([]map[string]any, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		out = append(out, map[string]any{
			"name":    p.Name,
			"uuid":    p.Uuid,
			"created": tsDate(p.CreatedAtUnixNs),
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) ListVMs(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListVMs", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListVMs(cctx, &weftv1.ListVMsRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetVms()))
	for _, v := range resp.GetVms() {
		out = append(out, map[string]any{
			"name":    v.Name,
			"uuid":    v.Uuid,
			"image":   v.Image,
			"status":  weftclient.StateString(v.State),
			"cpu":     v.Cpu,
			"mem_mb":  v.MemMb,
			"disk_gb": v.DiskGb,
			"ip":      v.Ip,
			"project": v.Project,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) ListNetworks(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListNetworks", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListNetworks(cctx, &weftv1.ListNetworksRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetNetworks()))
	for _, n := range resp.GetNetworks() {
		out = append(out, map[string]any{
			"name":    n.Name,
			"cidr":    n.Cidr,
			"type":    n.Type,
			"gateway": n.Gateway,
			"created": tsDate(n.CreatedAtUnixNs),
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) ListHosts(ctx context.Context, az string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListHosts", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListHosts(cctx, &weftv1.ListHostsRequest{
		Az: az, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetHosts()))
	for _, h := range resp.GetHosts() {
		out = append(out, map[string]any{
			"name":       h.Hostname,
			"az":         h.Az,
			"rack":       h.Rack,
			"arch":       h.Architecture,
			"hypervisor": h.Hypervisor,
			"status":     h.State,
			"last_seen":  tsDate(h.LastSeenAtUnixNs),
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) ListVolumes(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListVolumes", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListVolumes(cctx, &weftv1.ListVolumesRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetVolumes()))
	for _, v := range resp.GetVolumes() {
		out = append(out, map[string]any{
			"uuid":        v.Uuid,
			"name":        v.Name,
			"size_gib":    v.SizeGib,
			"format":      v.Format,
			"backend":     v.Backend,
			"attached_to": v.AttachedToUuid,
			"project":     v.ProjectUuid,
			"created":     tsDate(v.CreatedAtUnixNs),
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) ListUsers(ctx context.Context, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListUsers", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListUsers(cctx, &weftv1.ListUsersRequest{
		Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		out = append(out, map[string]any{
			"name":      u.DisplayName,
			"email":     u.Email,
			"issuer":    u.OidcIssuer,
			"groups":    strings.Join(u.Groups, ", "),
			"last_seen": tsDate(u.LastSeenAtUnixNs),
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) ListSecurityGroups(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListSecurityGroups", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSecurityGroups(cctx, &weftv1.ListSecurityGroupsRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetGroups()))
	for _, g := range resp.GetGroups() {
		out = append(out, map[string]any{
			"name":        g.Name,
			"description": g.Description,
			"rules":       len(g.Rules),
			"project":     g.ProjectUuid,
			"created":     tsDate(g.CreatedAtUnixNs),
		})
	}
	return out, resp.GetNextPageToken(), nil
}

// --- Mutators -------------------------------------------------------
//
// Every mutator threads the bearer token through outgoing metadata
// (so weft-agent applies the caller's RBAC) and is wrapped in c.measured for
// the gRPC histograms. Return shapes are deliberately thin — handlers
// surface the action's success/failure ; the SPA refreshes the row set
// afterwards.

// CreateProject creates a new project in weft-agent and returns its UUID.
// The webui's tenant model wraps this : the handler updates its
// tenant↔project mapping after the call succeeds.
func (c *Client) CreateProject(ctx context.Context, name string) (uuid string, retErr error) {
	defer c.measured("CreateProject", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateProject(cctx, &weftv1.CreateProjectRequest{Name: name})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Project == nil {
		return "", errors.New("nil CreateProject response")
	}
	return resp.Project.Uuid, nil
}

// DeleteProject removes a project. The caller must already own / have
// admin on it ; weft-agent refuses otherwise.
func (c *Client) DeleteProject(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteProject", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteProject(cctx, &weftv1.DeleteProjectRequest{Uuid: uuid})
	return err
}

// ListProjectMembers returns the user UUIDs that have a role on the
// project (any role).
func (c *Client) ListProjectMembers(ctx context.Context, projectUUID string) (uuids []string, retErr error) {
	defer c.measured("ListProjectMembers", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListProjectMembers(cctx, &weftv1.ListProjectMembersRequest{ProjectUuid: projectUUID})
	if err != nil {
		return nil, err
	}
	return resp.GetUserUuids(), nil
}

// AddProjectMember grants a user access to a project. Both sides are
// UUID-keyed in weft-agent ; the handler resolves email→UUID upstream.
func (c *Client) AddProjectMember(ctx context.Context, projectUUID, userUUID string) (retErr error) {
	defer c.measured("AddProjectMember", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.AddProjectMember(cctx, &weftv1.AddProjectMemberRequest{
		ProjectUuid: projectUUID, UserUuid: userUUID,
	})
	return err
}

// RemoveProjectMember revokes a user's access.
func (c *Client) RemoveProjectMember(ctx context.Context, projectUUID, userUUID string) (retErr error) {
	defer c.measured("RemoveProjectMember", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RemoveProjectMember(cctx, &weftv1.RemoveProjectMemberRequest{
		ProjectUuid: projectUUID, UserUuid: userUUID,
	})
	return err
}

// CreateVM creates a new microVM. The proto carries name/image/cpu/
// mem/disk/project + the two pull-model labels scheduling_rule/network ;
// flavor mapping (tenant view) is the webui's responsibility. SSH keys
// are NOT a create-time field anymore — they're pushed at runtime via
// the per-VM Properties surface ; the CreateVMRequest.ssh_pub tag was
// retired upstream (see weft-proto's reserved comment on tag 6).
type CreateVMOpts struct {
	Name, Image, Project string
	CPU                  uint32
	MemMB, DiskGB        uint64
	// SchedulingRule : nominal binding ; the rule's selector is the
	// discovery fallback for VMs without an explicit binding. Empty =
	// selector-only (legacy / loose binding). See [[openweft_nominal_binding]].
	SchedulingRule string
	// Network : private network name to attach the primary NIC to.
	// Empty = project default. Pull/reconcile : weft-agent persists,
	// weft-network's reconcile loop applies AttachVM. See [[openweft_pull_model]].
	Network string
}

func (c *Client) CreateVM(ctx context.Context, o CreateVMOpts) (retErr error) {
	defer c.measured("CreateVM", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.CreateVM(cctx, &weftv1.CreateVMRequest{
		Name: o.Name, Image: o.Image, Project: o.Project,
		Cpu: o.CPU, MemMb: o.MemMB, DiskGb: o.DiskGB,
		SchedulingRule: o.SchedulingRule,
		Network:        o.Network,
	})
	return err
}

// StartVM / StopVM / DeleteVM share the same three-field request
// shape (name + project + optional host UUID). The webui doesn't pin
// a host today — weft-agent's scheduler picks one.
func (c *Client) StartVM(ctx context.Context, name, project string) (retErr error) {
	defer c.measured("StartVM", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.StartVM(cctx, &weftv1.StartVMRequest{Name: name, Project: project})
	return err
}

func (c *Client) StopVM(ctx context.Context, name, project string) (retErr error) {
	defer c.measured("StopVM", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.StopVM(cctx, &weftv1.StopVMRequest{Name: name, Project: project})
	return err
}

func (c *Client) DeleteVM(ctx context.Context, name, project string) (retErr error) {
	defer c.measured("DeleteVM", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteVM(cctx, &weftv1.DeleteVMRequest{Name: name, Project: project})
	return err
}

// VMInfo is the typed shape every VM-level read returns — VMStatus
// today, future per-VM watchers tomorrow. Mirror of weft-proto's
// weftv1.VMInfo with State pre-rendered to a human string ; the
// fields and JSON tags match what handleResourceRows emits for the
// /api/microvms listing so the SPA renders both via the same code.
type VMInfo struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Image   string `json:"image"`
	Status  string `json:"status"`
	OS      string `json:"os"`
	CPU     uint32 `json:"cpu"`
	MemMB   uint64 `json:"mem_mb"`
	DiskGB  uint64 `json:"disk_gb"`
	IP      string `json:"ip"`
	Project string `json:"project"`
}

// VMStatus returns the live VMInfo for a single VM.
func (c *Client) VMStatus(ctx context.Context, name, project string) (info *VMInfo, retErr error) {
	defer c.measured("VMStatus", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.VMStatus(cctx, &weftv1.VMStatusRequest{Name: name, Project: project})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Vm == nil {
		return nil, errors.New("nil VMStatus response")
	}
	v := resp.Vm
	return &VMInfo{
		Name: v.Name, UUID: v.Uuid, Image: v.Image,
		Status: weftclient.StateString(v.State),
		OS:     v.Os,
		CPU:    v.Cpu, MemMB: v.MemMb, DiskGB: v.DiskGb,
		IP: v.Ip, Project: v.Project,
	}, nil
}

// AttachVolume wires a Volume to a VM by their UUIDs. Detach takes
// only the volume UUID — the daemon resolves the current attachment.
func (c *Client) AttachVolume(ctx context.Context, volumeUUID, vmUUID string) (retErr error) {
	defer c.measured("AttachVolume", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.AttachVolume(cctx, &weftv1.AttachVolumeRequest{Uuid: volumeUUID, VmUuid: vmUUID})
	return err
}

func (c *Client) DetachVolume(ctx context.Context, volumeUUID string) (retErr error) {
	defer c.measured("DetachVolume", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DetachVolume(cctx, &weftv1.DetachVolumeRequest{Uuid: volumeUUID})
	return err
}

// VMTimingEvent is one entry in the lifecycle-events list. ts is the
// RFC-3339 rendering of the proto's ts_unix_ns so the frontend can
// emit it verbatim without re-encoding.
type VMTimingEvent struct {
	Name string            `json:"name"`
	TS   string            `json:"ts"`
	Meta map[string]string `json:"meta"`
}

// VMTimings returns the recorded lifecycle events for a VM (state
// transitions, network up, exec ready, …).
func (c *Client) VMTimings(ctx context.Context, name, project string) (events []VMTimingEvent, retErr error) {
	defer c.measured("VMTimings", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.VMTimings(cctx, &weftv1.VMTimingsRequest{Name: name, Project: project})
	if err != nil {
		return nil, err
	}
	out := make([]VMTimingEvent, 0, len(resp.GetEvents()))
	for _, e := range resp.GetEvents() {
		out = append(out, VMTimingEvent{
			Name: e.Name,
			TS:   time.Unix(0, e.TsUnixNs).UTC().Format(time.RFC3339Nano),
			Meta: e.Meta,
		})
	}
	return out, nil
}

// VMLogsResult is the console-log tail returned by /api/microvms/
// {name}/logs. Contents is the raw text ; TotalBytes is the size on
// disk so the SPA can show "showing N of M bytes".
type VMLogsResult struct {
	Contents   string `json:"contents"`
	TotalBytes int64  `json:"total_bytes"`
}

// VMLogs returns the tail of the console log. tailBytes=0 reads
// everything ; the frontend defaults to a sensible cap (~64 KiB).
func (c *Client) VMLogs(ctx context.Context, name, project string, tailBytes int64) (out *VMLogsResult, retErr error) {
	defer c.measured("VMLogs", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.VMLogs(cctx, &weftv1.VMLogsRequest{Name: name, Project: project, TailBytes: tailBytes})
	if err != nil {
		return nil, err
	}
	return &VMLogsResult{
		Contents:   string(resp.GetContents()),
		TotalBytes: resp.GetTotalBytes(),
	}, nil
}

// CreateNetwork / DeleteNetwork.
type CreateNetworkOpts struct {
	Project, Name, CIDR, Gateway, Type string
	DNSServers                         []string
	// Edge-attachment knobs for floating IPs ; see weft-proto
	// NetworkInfo.external_mode for the semantics. Empty
	// ExternalMode defaults to "bgp" on the server side.
	ExternalMode    string
	VLAN            int32
	ParentInterface string
}

func (c *Client) CreateNetwork(ctx context.Context, o CreateNetworkOpts) (retErr error) {
	defer c.measured("CreateNetwork", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.CreateNetwork(cctx, &weftv1.CreateNetworkRequest{
		Project: o.Project, Name: o.Name, Cidr: o.CIDR, Gateway: o.Gateway,
		DnsServers:      o.DNSServers,
		Type:            o.Type,
		ExternalMode:    o.ExternalMode,
		Vlan:            o.VLAN,
		ParentInterface: o.ParentInterface,
	})
	return err
}

func (c *Client) DeleteNetwork(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteNetwork", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteNetwork(cctx, &weftv1.DeleteNetworkRequest{Uuid: uuid})
	return err
}

// CreateVolume / DeleteVolume.
func (c *Client) CreateVolume(ctx context.Context, project, name string, sizeGiB int64, format string) (retErr error) {
	defer c.measured("CreateVolume", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.CreateVolume(cctx, &weftv1.CreateVolumeRequest{
		Project: project, Name: name, SizeGib: sizeGiB, Format: format,
	})
	return err
}

func (c *Client) DeleteVolume(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteVolume", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteVolume(cctx, &weftv1.DeleteVolumeRequest{Uuid: uuid})
	return err
}

// SecurityRule mirrors the proto's per-rule shape but exposed as a
// public type so handlers can decode the SPA's payload without
// touching the weftv1 alias.
type SecurityRule struct {
	Direction       string `json:"direction" enum:"ingress,egress"`
	Protocol        string `json:"protocol" enum:"tcp,udp,icmp,any"`
	PortMin         int32  `json:"port_min"`
	PortMax         int32  `json:"port_max"`
	RemoteCIDR      string `json:"remote_cidr"`
	RemoteGroupUUID string `json:"remote_group_uuid"`
	// Enabled = whether the rule applies. Disabled rules stay in the
	// list (auditable + easy to re-enable) but the data plane skips
	// them. Optional on the wire — missing = enabled — so older
	// rule literals don't need to be updated atomically with this
	// field's introduction.
	Enabled bool `json:"enabled,omitempty"`
}

// CreateSecurityGroupOpts groups the proto's create fields with a
// JSON-friendly rules slice. The handler accepts the same shape from
// the SPA's POST body.
type CreateSecurityGroupOpts struct {
	Project, Name, Description string
	Rules                      []SecurityRule
}

func (c *Client) CreateSecurityGroup(ctx context.Context, o CreateSecurityGroupOpts) (uuid string, retErr error) {
	defer c.measured("CreateSecurityGroup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	rules := make([]*weftv1.SecurityRule, 0, len(o.Rules))
	for _, r := range o.Rules {
		rules = append(rules, &weftv1.SecurityRule{
			Direction: r.Direction, Protocol: r.Protocol,
			PortMin: r.PortMin, PortMax: r.PortMax,
			RemoteCidr: r.RemoteCIDR, RemoteGroupUuid: r.RemoteGroupUUID,
		})
	}
	resp, err := rpc.CreateSecurityGroup(cctx, &weftv1.CreateSecurityGroupRequest{
		Project: o.Project, Name: o.Name, Description: o.Description, Rules: rules,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Group == nil {
		return "", errors.New("nil CreateSecurityGroup response")
	}
	return resp.Group.Uuid, nil
}

func (c *Client) DeleteSecurityGroup(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteSecurityGroup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteSecurityGroup(cctx, &weftv1.DeleteSecurityGroupRequest{Uuid: uuid})
	return err
}

// SetSecurityGroupRules atomically replaces the SG's rule list. The
// proto's `repeated rules` semantic is "this is the new state" —
// any pre-existing rule not in the slice is dropped.
func (c *Client) SetSecurityGroupRules(ctx context.Context, uuid string, rules []SecurityRule) (retErr error) {
	defer c.measured("SetSecurityGroupRules", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	protoRules := make([]*weftv1.SecurityRule, 0, len(rules))
	for _, r := range rules {
		protoRules = append(protoRules, &weftv1.SecurityRule{
			Direction: r.Direction, Protocol: r.Protocol,
			PortMin: r.PortMin, PortMax: r.PortMax,
			RemoteCidr: r.RemoteCIDR, RemoteGroupUuid: r.RemoteGroupUUID,
		})
	}
	_, err = rpc.SetSecurityGroupRules(cctx, &weftv1.SetSecurityGroupRulesRequest{
		Uuid: uuid, Rules: protoRules,
	})
	return err
}

// GetSecurityGroup returns one SG by UUID. There's no dedicated
// GetSecurityGroup RPC ; we list (paginating to be safe) and filter.
// Good enough at SG-list scale (typically dozens, not thousands per
// project).
func (c *Client) GetSecurityGroup(ctx context.Context, uuid string) (rules []SecurityRule, retErr error) {
	defer c.measured("GetSecurityGroup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	token := ""
	for {
		resp, err := rpc.ListSecurityGroups(cctx, &weftv1.ListSecurityGroupsRequest{
			PageToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, g := range resp.GetGroups() {
			if g.Uuid != uuid {
				continue
			}
			out := make([]SecurityRule, 0, len(g.Rules))
			for _, r := range g.Rules {
				out = append(out, SecurityRule{
					Direction: r.Direction, Protocol: r.Protocol,
					PortMin: r.PortMin, PortMax: r.PortMax,
					RemoteCIDR: r.RemoteCidr, RemoteGroupUUID: r.RemoteGroupUuid,
				})
			}
			return out, nil
		}
		if resp.GetNextPageToken() == "" {
			return nil, errors.New("security group not found")
		}
		token = resp.GetNextPageToken()
	}
}

// --- Lookup helpers -------------------------------------------------
//
// weft-agent's mutation RPCs key by UUID (project, user, network, volume).
// The SPA works in human names ; these helpers walk the matching list
// once per request to resolve.
//
// They're intentionally not cached at this layer : the daemon is
// authoritative, and a stale lookup that referenced a renamed entity
// would 400 down the line anyway.

// EventStream is the channel-based view of WatchEvents. Each emit is
// a flat map ready for SSE / JSON ; the underlying gRPC stream is
// cancelled when the caller's context is done OR Close is invoked.
type EventStream struct {
	Events <-chan map[string]any
	Errors <-chan error
	cancel context.CancelFunc
}

// Close cancels the stream. Safe to call multiple times.
func (s *EventStream) Close() { s.cancel() }

// WatchEvents opens a server-stream of PlatformEvents filtered by
// kindPrefixes (any-match) and optionally narrowed to a single
// project / subject. The returned channels are closed when the stream
// ends (server side or context cancel). Errors carry the canonical
// gRPC status so the proxy can surface a clean message.
func (c *Client) WatchEvents(ctx context.Context, kindPrefixes []string, project, subject string) (*EventStream, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(withBearer(ctx))
	stream, err := rpc.WatchEvents(streamCtx, &weftv1.WatchEventsRequest{
		KindPrefix: kindPrefixes, Project: project, Subject: subject,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	events := make(chan map[string]any, 16)
	errors := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errors)
		for {
			ev, err := stream.Recv()
			if err != nil {
				errors <- err
				return
			}
			events <- map[string]any{
				"ts":      time.Unix(0, ev.TsUnixNs).UTC().Format(time.RFC3339Nano),
				"kind":    ev.Kind,
				"subject": ev.Subject,
				"project": ev.ProjectUuid,
				"meta":    ev.Meta,
			}
		}
	}()
	return &EventStream{Events: events, Errors: errors, cancel: cancel}, nil
}

// UserUUIDByEmail returns the UUID for the given email, or "" if no
// user matches. Walks ListUsers across pages — the per-request count
// is small but the dataset may not fit a single page once weft-agent
// honours its own pagination cap.
func (c *Client) UserUUIDByEmail(ctx context.Context, email string) (string, error) {
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	want := strings.ToLower(strings.TrimSpace(email))
	token := ""
	for {
		resp, err := rpc.ListUsers(cctx, &weftv1.ListUsersRequest{PageToken: token})
		if err != nil {
			return "", err
		}
		for _, u := range resp.GetUsers() {
			if strings.EqualFold(u.Email, want) {
				return u.Uuid, nil
			}
		}
		if resp.GetNextPageToken() == "" {
			return "", nil
		}
		token = resp.GetNextPageToken()
	}
}

// ProjectUUIDByName resolves a project name to its UUID. Walks the
// ListProjects pages until a match or exhaustion.
func (c *Client) ProjectUUIDByName(ctx context.Context, name string) (string, error) {
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	token := ""
	for {
		resp, err := rpc.ListProjects(cctx, &weftv1.ListProjectsRequest{PageToken: token})
		if err != nil {
			return "", err
		}
		for _, p := range resp.Projects {
			if p.Name == name {
				return p.Uuid, nil
			}
		}
		if resp.GetNextPageToken() == "" {
			return "", nil
		}
		token = resp.GetNextPageToken()
	}
}

// --- New RPCs (post-proto-extension) -------------------------------
//
// Tenants / Quotas / Shares / Floating IPs. Each returns
// codes.Unimplemented while the daemon catches up ; handlers call
// IsUnimplemented(err) to decide whether to fall back to the webui's
// in-memory mock store or surface the error.

// IsUnimplemented reports whether err is a gRPC Unimplemented status.
// Used by handlers to fall back gracefully to the mock store while
// the daemon catches up with the proto.
func IsUnimplemented(err error) bool {
	return err != nil && status.Code(err) == codes.Unimplemented
}

// ---- Tenants ----

// ListTenants is unique among the List* family : the proto request is
// empty (cluster cardinality is bounded by the operator's onboarding
// flow, not user activity). Signature stays (ctx)→rows, no opts.
func (c *Client) ListTenants(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListTenants", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListTenants(cctx, &weftv1.ListTenantsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetTenants()))
	for _, t := range resp.GetTenants() {
		out = append(out, map[string]any{
			"name":     t.Name,
			"uuid":     t.Uuid,
			"domain":   t.Domain,
			"status":   t.Status,
			"projects": t.Projects,
			"members":  t.Members,
			"admins":   t.Admins,
		})
	}
	return out, nil
}

func (c *Client) CreateTenant(ctx context.Context, name, domain string) (uuid string, retErr error) {
	defer c.measured("CreateTenant", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateTenant(cctx, &weftv1.CreateTenantRequest{Name: name, Domain: domain})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Tenant == nil {
		return "", errors.New("nil CreateTenant response")
	}
	return resp.Tenant.Uuid, nil
}

func (c *Client) AddTenantAdmin(ctx context.Context, tenantUUID, email string) (retErr error) {
	defer c.measured("AddTenantAdmin", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.AddTenantAdmin(cctx, &weftv1.AddTenantAdminRequest{
		TenantUuid: tenantUUID, Email: email,
	})
	return err
}

func (c *Client) AddTenantMember(ctx context.Context, tenantUUID, email string, groups []string) (retErr error) {
	defer c.measured("AddTenantMember", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.AddTenantMember(cctx, &weftv1.AddTenantMemberRequest{
		TenantUuid: tenantUUID, Email: email, Groups: groups,
	})
	return err
}

// ---- Quotas ----
//
// The webui's Quotas struct doesn't depend on weftv1 ; the
// translation table here keeps the two shapes from drifting.

func quotasToProto(in map[string]int) *weftv1.Quotas {
	return &weftv1.Quotas{
		Vcpu: int32(in["vcpu"]), RamGib: int32(in["ram_gib"]),
		Volumes: int32(in["volumes"]), VolumesGib: int32(in["volumes_gib"]),
		Shares: int32(in["shares"]), SharesGib: int32(in["shares_gib"]),
		Buckets: int32(in["buckets"]), BucketsGib: int32(in["buckets_gib"]),
		RegistryGib: int32(in["registry_gib"]),
		FloatingIps: int32(in["floating_ips"]),
		Projects:    int32(in["projects"]),
	}
}

func quotasFromProto(q *weftv1.Quotas) map[string]int {
	if q == nil {
		return nil
	}
	return map[string]int{
		"vcpu": int(q.Vcpu), "ram_gib": int(q.RamGib),
		"volumes": int(q.Volumes), "volumes_gib": int(q.VolumesGib),
		"shares": int(q.Shares), "shares_gib": int(q.SharesGib),
		"buckets": int(q.Buckets), "buckets_gib": int(q.BucketsGib),
		"registry_gib": int(q.RegistryGib),
		"floating_ips": int(q.FloatingIps),
		"projects":     int(q.Projects),
	}
}

func (c *Client) GetTenantQuota(ctx context.Context, tenantUUID string) (cap, alloc map[string]int, retErr error) {
	defer c.measured("GetTenantQuota", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetTenantQuota(cctx, &weftv1.GetTenantQuotaRequest{TenantUuid: tenantUUID})
	if err != nil {
		return nil, nil, err
	}
	return quotasFromProto(resp.Cap), quotasFromProto(resp.Allocated), nil
}

func (c *Client) SetTenantQuota(ctx context.Context, tenantUUID string, cap map[string]int) (retErr error) {
	defer c.measured("SetTenantQuota", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetTenantQuota(cctx, &weftv1.SetTenantQuotaRequest{
		TenantUuid: tenantUUID, Cap: quotasToProto(cap),
	})
	return err
}

func (c *Client) SetProjectQuota(ctx context.Context, projectUUID string, q map[string]int) (retErr error) {
	defer c.measured("SetProjectQuota", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetProjectQuota(cctx, &weftv1.SetProjectQuotaRequest{
		ProjectUuid: projectUUID, Quota: quotasToProto(q),
	})
	return err
}

// ---- Shares ----

func (c *Client) ListShares(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListShares", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListShares(cctx, &weftv1.ListSharesRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetShares()))
	for _, s := range resp.GetShares() {
		out = append(out, map[string]any{
			"name":     s.Name,
			"uuid":     s.Uuid,
			"project":  s.ProjectUuid,
			"backend":  s.Backend,
			"size_gb":  s.SizeGb,
			"readonly": s.Readonly,
			"mounts":   s.Mounts,
			"status":   s.Status,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) CreateShare(ctx context.Context, project, name string, sizeGB int64, readonly bool, backend string) (uuid string, retErr error) {
	defer c.measured("CreateShare", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateShare(cctx, &weftv1.CreateShareRequest{
		Project: project, Name: name, SizeGb: sizeGB, Readonly: readonly, Backend: backend,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Share == nil {
		return "", errors.New("nil CreateShare response")
	}
	return resp.Share.Uuid, nil
}

func (c *Client) DeleteShare(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteShare", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteShare(cctx, &weftv1.DeleteShareRequest{Uuid: uuid})
	return err
}

// ---- Floating IPs ----

func (c *Client) ListFloatingIPs(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListFloatingIPs", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListFloatingIPs(cctx, &weftv1.ListFloatingIPsRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetFloatingIps()))
	for _, f := range resp.GetFloatingIps() {
		out = append(out, map[string]any{
			"uuid":      f.Uuid,
			"address":   f.Address,
			"network":   f.Network,
			"project":   f.ProjectUuid,
			"mapped_to": f.MappedTo,
			"status":    f.Status,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) AllocateFloatingIP(ctx context.Context, project, network string) (uuid, address string, retErr error) {
	defer c.measured("AllocateFloatingIP", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.AllocateFloatingIP(cctx, &weftv1.AllocateFloatingIPRequest{Project: project, Network: network})
	if err != nil {
		return "", "", err
	}
	if resp == nil || resp.FloatingIp == nil {
		return "", "", errors.New("nil AllocateFloatingIP response")
	}
	return resp.FloatingIp.Uuid, resp.FloatingIp.Address, nil
}

func (c *Client) ReleaseFloatingIP(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("ReleaseFloatingIP", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.ReleaseFloatingIP(cctx, &weftv1.ReleaseFloatingIPRequest{Uuid: uuid})
	return err
}

func (c *Client) MapFloatingIP(ctx context.Context, uuid, targetKind, targetName string) (retErr error) {
	defer c.measured("MapFloatingIP", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.MapFloatingIP(cctx, &weftv1.MapFloatingIPRequest{
		Uuid: uuid, TargetKind: targetKind, TargetName: targetName,
	})
	return err
}

func (c *Client) UnmapFloatingIP(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("UnmapFloatingIP", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UnmapFloatingIP(cctx, &weftv1.UnmapFloatingIPRequest{Uuid: uuid})
	return err
}

// ============================================================================
// Catalogue + VM-metadata passthroughs (Blocks B / Scripts / C / D / E)
// ============================================================================
//
// These methods cover the five RPC blocks added to weft-proto in commits
// 3608a44 (Flavors), 2684105 (Scripts), ec94187 (VMProperty),
// 70e1309 (UEFIVar), 7703167 (VMSSHKey). weft-agent hasn't implemented
// them yet — each currently returns codes.Unimplemented, and handlers
// fall back to their in-memory store via IsUnimplemented(err) (the same
// dance ListTenants / ListShares / scheduling-rules already do).
//
// The methods exist now so wiring a handler to live-mode is a one-liner
// when weft-agent ships any one of these. Shape mirrors the existing
// List* / Set* methods on this client : []map[string]any for table-
// shaped rows, typed value structs for single-object reads.

// ---- Metrics --------------------------------------------------------
//
// Per-VM metrology snapshot. There is no GetMicroVMMetrics RPC on
// weft-proto today ; this client method always returns
// codes.Unimplemented so callers can fall back to the webui's
// synthetic-data path via IsUnimplemented(err). Wiring is a one-liner
// the day weft-proto grows the RPC (see CHANGELOG follow-up note in
// the Metrics tab landing commit).

// MicroVMMetrics is the typed shape the metrics endpoint returns.
// Fields mirror server.MetricsSnapshot's JSON tags so we can populate
// it 1:1 once a real RPC arrives.
type MicroVMMetrics struct {
	SampledAtUnix int64
	CPUPercent    float64
	MemUsedMiB    uint64
	MemTotalMiB   uint64
	NetRxBps      uint64
	NetTxBps      uint64
	DiskReadBps   uint64
	DiskWriteBps  uint64
	UptimeSeconds uint64
}

// GetMicroVMMetrics returns the latest sample for one VM. Until
// weft-proto ships the RPC this always reports Unimplemented ; the
// caller's IsUnimplemented check then triggers the synthetic fallback
// in the webui.
func (c *Client) GetMicroVMMetrics(ctx context.Context, name, project string) (m *MicroVMMetrics, retErr error) {
	defer c.measured("GetMicroVMMetrics", &retErr)()
	// We deliberately don't dial here ; the call has no underlying
	// RPC yet. Returning Unimplemented immediately keeps the synth
	// path zero-cost and signals "future work" cleanly.
	_ = ctx
	_ = name
	_ = project
	return nil, status.Error(codes.Unimplemented, "GetMicroVMMetrics : weft-proto RPC not yet defined")
}

// ---- Flavors --------------------------------------------------------

func (c *Client) ListFlavors(ctx context.Context, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListFlavors", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListFlavors(cctx, &weftv1.ListFlavorsRequest{
		Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetFlavors()))
	for _, f := range resp.GetFlavors() {
		out = append(out, map[string]any{
			"name":         f.Name,
			"vcpu":         int(f.Vcpu),
			"ram":          f.Ram,
			"ephemeral_gb": int(f.EphemeralGb),
			"gpu":          f.Gpu,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

// GetFlavor returns one flavor as a row-shape map ; the typed Flavor
// struct lives in the webui's flavors.go and the handler builds it from
// the map. Keeps the wclient method uniform with the rest.
func (c *Client) GetFlavor(ctx context.Context, name string) (row map[string]any, retErr error) {
	defer c.measured("GetFlavor", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetFlavor(cctx, &weftv1.GetFlavorRequest{Name: name})
	if err != nil {
		return nil, err
	}
	f := resp.GetFlavor()
	if f == nil {
		return nil, errors.New("nil flavor")
	}
	return map[string]any{
		"name":         f.Name,
		"vcpu":         int(f.Vcpu),
		"ram":          f.Ram,
		"ephemeral_gb": int(f.EphemeralGb),
		"gpu":          f.Gpu,
	}, nil
}

func (c *Client) SetFlavor(ctx context.Context, name string, vcpu int32, ram string, ephemeralGB int32, gpu string) (retErr error) {
	defer c.measured("SetFlavor", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetFlavor(cctx, &weftv1.SetFlavorRequest{
		Flavor: &weftv1.Flavor{
			Name: name, Vcpu: vcpu, Ram: ram, EphemeralGb: ephemeralGB, Gpu: gpu,
		},
	})
	return err
}

func (c *Client) DeleteFlavor(ctx context.Context, name string) (retErr error) {
	defer c.measured("DeleteFlavor", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteFlavor(cctx, &weftv1.DeleteFlavorRequest{Name: name})
	return err
}

// ---- Scripts -------------------------------------------------------

func (c *Client) ListScripts(ctx context.Context, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListScripts", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListScripts(cctx, &weftv1.ListScriptsRequest{
		Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetScripts()))
	for _, s := range resp.GetScripts() {
		out = append(out, map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"body":        s.Body,
			"updated_at":  s.UpdatedAt,
			"updated_by":  s.UpdatedBy,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) GetScript(ctx context.Context, name string) (row map[string]any, retErr error) {
	defer c.measured("GetScript", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetScript(cctx, &weftv1.GetScriptRequest{Name: name})
	if err != nil {
		return nil, err
	}
	s := resp.GetScript()
	if s == nil {
		return nil, errors.New("nil script")
	}
	return map[string]any{
		"name":        s.Name,
		"description": s.Description,
		"body":        s.Body,
		"updated_at":  s.UpdatedAt,
		"updated_by":  s.UpdatedBy,
	}, nil
}

func (c *Client) SetScript(ctx context.Context, name, description, body string) (retErr error) {
	defer c.measured("SetScript", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetScript(cctx, &weftv1.SetScriptRequest{
		Script: &weftv1.Script{Name: name, Description: description, Body: body},
	})
	return err
}

func (c *Client) DeleteScript(ctx context.Context, name string) (retErr error) {
	defer c.measured("DeleteScript", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteScript(cctx, &weftv1.DeleteScriptRequest{Name: name})
	return err
}

// ---- VM properties --------------------------------------------------

// VMProperty mirrors the proto's per-VM annotation. Same JSON tags
// the webui's mem-store VMProperty uses so the live + fallback
// paths produce identical wire shapes.
type VMProperty struct {
	Key           string `json:"key"`
	Value         string `json:"value"`
	GuestReadable bool   `json:"guest_readable"`
	UpdatedAt     string `json:"updated_at"`
}

func (c *Client) ListVMProperties(ctx context.Context, vmName, project string, opts ListOpts) (rows []VMProperty, next string, retErr error) {
	defer c.measured("ListVMProperties", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListVMProperties(cctx, &weftv1.ListVMPropertiesRequest{
		VmName: vmName, Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]VMProperty, 0, len(resp.GetProperties()))
	for _, p := range resp.GetProperties() {
		out = append(out, VMProperty{
			Key: p.Key, Value: p.Value,
			GuestReadable: p.GuestReadable, UpdatedAt: p.UpdatedAt,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) SetVMProperty(ctx context.Context, vmName, project, key, value string, guestReadable bool) (retErr error) {
	defer c.measured("SetVMProperty", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetVMProperty(cctx, &weftv1.SetVMPropertyRequest{
		VmName: vmName, Project: project,
		Property: &weftv1.VMProperty{Key: key, Value: value, GuestReadable: guestReadable},
	})
	return err
}

func (c *Client) DeleteVMProperty(ctx context.Context, vmName, project, key string) (retErr error) {
	defer c.measured("DeleteVMProperty", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteVMProperty(cctx, &weftv1.DeleteVMPropertyRequest{
		VmName: vmName, Project: project, Key: key,
	})
	return err
}

// ---- UEFI variables ------------------------------------------------

// UEFIVar mirrors the proto's per-VM NVRAM entry. Same JSON tags as
// the webui's mem-store UEFIVar.
type UEFIVar struct {
	Namespace  string   `json:"namespace"`
	Name       string   `json:"name"`
	ValueHex   string   `json:"value_hex"`
	Attributes []string `json:"attributes"`
	UpdatedAt  string   `json:"updated_at"`
}

func (c *Client) ListUEFIVars(ctx context.Context, vmName, project string, opts ListOpts) (rows []UEFIVar, next string, retErr error) {
	defer c.measured("ListUEFIVars", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListUEFIVars(cctx, &weftv1.ListUEFIVarsRequest{
		VmName: vmName, Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]UEFIVar, 0, len(resp.GetVars()))
	for _, v := range resp.GetVars() {
		out = append(out, UEFIVar{
			Namespace: v.Namespace, Name: v.Name, ValueHex: v.ValueHex,
			Attributes: v.Attributes, UpdatedAt: v.UpdatedAt,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) SetUEFIVar(ctx context.Context, vmName, project, namespace, name, valueHex string, attributes []string) (retErr error) {
	defer c.measured("SetUEFIVar", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetUEFIVar(cctx, &weftv1.SetUEFIVarRequest{
		VmName: vmName, Project: project,
		Var: &weftv1.UEFIVar{
			Namespace: namespace, Name: name,
			ValueHex: valueHex, Attributes: attributes,
		},
	})
	return err
}

func (c *Client) DeleteUEFIVar(ctx context.Context, vmName, project, namespace, name string) (retErr error) {
	defer c.measured("DeleteUEFIVar", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteUEFIVar(cctx, &weftv1.DeleteUEFIVarRequest{
		VmName: vmName, Project: project, Namespace: namespace, Name: name,
	})
	return err
}

// ---- VM SSH keys ---------------------------------------------------

// VMAgentSSHKey is what weft-agent's per-VM SSH-key registry stores :
// the OpenSSH line as the operator pasted it, plus the SHA256
// fingerprint the server computed. Distinct from the webui's
// catalogue+assignment model (sshkeys.VMSSHKey) — this is the raw
// per-VM registry without name indirection.
type VMAgentSSHKey struct {
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	PublicKey   string `json:"public_key"`
	Comment     string `json:"comment"`
	AddedAt     string `json:"added_at"`
}

func (c *Client) ListVMSSHKeys(ctx context.Context, vmName, project string, opts ListOpts) (rows []VMAgentSSHKey, next string, retErr error) {
	defer c.measured("ListVMSSHKeys", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListVMSSHKeys(cctx, &weftv1.ListVMSSHKeysRequest{
		VmName: vmName, Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]VMAgentSSHKey, 0, len(resp.GetKeys()))
	for _, k := range resp.GetKeys() {
		out = append(out, VMAgentSSHKey{
			Fingerprint: k.Fingerprint, Type: k.Type,
			PublicKey: k.PublicKey, Comment: k.Comment,
			AddedAt: k.AddedAt,
		})
	}
	return out, resp.GetNextPageToken(), nil
}

func (c *Client) AddVMSSHKey(ctx context.Context, vmName, project, publicKey string) (key *VMAgentSSHKey, retErr error) {
	defer c.measured("AddVMSSHKey", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.AddVMSSHKey(cctx, &weftv1.AddVMSSHKeyRequest{
		VmName: vmName, Project: project, PublicKey: publicKey,
	})
	if err != nil {
		return nil, err
	}
	k := resp.GetKey()
	if k == nil {
		return nil, errors.New("nil key")
	}
	return &VMAgentSSHKey{
		Fingerprint: k.Fingerprint, Type: k.Type,
		PublicKey: k.PublicKey, Comment: k.Comment,
		AddedAt: k.AddedAt,
	}, nil
}

func (c *Client) RemoveVMSSHKey(ctx context.Context, vmName, project, fingerprint string) (retErr error) {
	defer c.measured("RemoveVMSSHKey", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RemoveVMSSHKey(cctx, &weftv1.RemoveVMSSHKeyRequest{
		VmName: vmName, Project: project, Fingerprint: fingerprint,
	})
	return err
}

// ============================================================================
// Volume snapshots + backups (5 + 4 RPCs)
// ============================================================================
//
// Snapshot path covers both backends : file-backed parents do a reflink
// clone server-side, block-backed parents (weft-block) dispatch through
// the driver's CreateSnapshot. Revert is block-only — file parents get a
// FailedPrecondition with a clear "only block volumes" error.
//
// Backup path is block-only (file backend has no off-host story yet) and
// ships through one of four target URLs : oci:// (recommended), s3://
// (versitygw / CubeFS objectnode), sftp:// (sftpgo), fs:// (dev/tests).
// Encryption + incremental chain bookkeeping live in weft-block ; the
// client only passes URLs around — passphrase is daemon-side env-only.

// ---- Snapshots ------------------------------------------------------

// VolumeSnapshotInfo is the projection the UI consumes for one row in
// the snapshot table. Mirrors VolumeSnapshotInfo on the wire but keeps
// Go-shaped field names + timestamp string.
type VolumeSnapshotInfo struct {
	UUID       string `json:"uuid"`
	VolumeUUID string `json:"volume_uuid"`
	Name       string `json:"name"`
	SizeGiB    int64  `json:"size_gib"`
	Project    string `json:"project"`
	CreatedAt  string `json:"created_at"`
}

func (c *Client) ListVolumeSnapshots(ctx context.Context, volumeUUID, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListVolumeSnapshots", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_ = opts // server-side filter ignores Limit / PageToken today ; honoured when proto carries them
	resp, err := rpc.ListVolumeSnapshots(cctx, &weftv1.ListVolumeSnapshotsRequest{
		VolumeUuid: volumeUUID, Project: project,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetSnapshots()))
	for _, s := range resp.GetSnapshots() {
		out = append(out, map[string]any{
			"uuid":        s.Uuid,
			"volume_uuid": s.VolumeUuid,
			"name":        s.Name,
			"size_gib":    s.SizeGib,
			"project":     s.Project,
			"created":     tsDate(s.CreatedAtUnixNs),
		})
	}
	return out, "", nil
}

func (c *Client) CreateVolumeSnapshot(ctx context.Context, volumeUUID, name, project string) (snap *VolumeSnapshotInfo, retErr error) {
	defer c.measured("CreateVolumeSnapshot", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateVolumeSnapshot(cctx, &weftv1.CreateVolumeSnapshotRequest{
		VolumeUuid: volumeUUID, Name: name, Project: project,
	})
	if err != nil {
		return nil, err
	}
	s := resp.GetSnapshot()
	if s == nil {
		return nil, nil
	}
	return &VolumeSnapshotInfo{
		UUID:       s.Uuid,
		VolumeUUID: s.VolumeUuid,
		Name:       s.Name,
		SizeGiB:    s.SizeGib,
		Project:    s.Project,
		CreatedAt:  tsDate(s.CreatedAtUnixNs),
	}, nil
}

func (c *Client) RestoreVolumeSnapshot(ctx context.Context, snapshotUUID, newVolumeName, project string) (retErr error) {
	defer c.measured("RestoreVolumeSnapshot", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RestoreVolumeSnapshot(cctx, &weftv1.RestoreVolumeSnapshotRequest{
		SnapshotUuid: snapshotUUID, NewVolumeName: newVolumeName, Project: project,
	})
	return err
}

// RevertVolumeSnapshot rolls a block-backend parent volume back to the
// snapshot's state. File-backend parents reject server-side with a
// FailedPrecondition the caller surfaces verbatim.
func (c *Client) RevertVolumeSnapshot(ctx context.Context, snapshotUUID string) (retErr error) {
	defer c.measured("RevertVolumeSnapshot", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RevertVolumeSnapshot(cctx, &weftv1.RevertVolumeSnapshotRequest{Uuid: snapshotUUID})
	return err
}

func (c *Client) DeleteVolumeSnapshot(ctx context.Context, snapshotUUID string) (retErr error) {
	defer c.measured("DeleteVolumeSnapshot", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteVolumeSnapshot(cctx, &weftv1.DeleteVolumeSnapshotRequest{Uuid: snapshotUUID})
	return err
}

// ---- Backups (block-only) -------------------------------------------

// VolumeBackupInfo is the projection the UI consumes for one row in
// the backup table. URL is the addressing key (opaque to the UI ;
// Delete + Restore take it back verbatim).
type VolumeBackupInfo struct {
	URL          string `json:"url"`
	VolumeUUID   string `json:"volume_uuid"`
	SnapshotUUID string `json:"snapshot_uuid"`
	Project      string `json:"project"`
	SizeBytes    int64  `json:"size_bytes"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
	CreatedAt    string `json:"created_at"`
}

func (c *Client) CreateVolumeBackup(ctx context.Context, snapshotUUID, target, project string) (info *VolumeBackupInfo, retErr error) {
	defer c.measured("CreateVolumeBackup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateVolumeBackup(cctx, &weftv1.CreateVolumeBackupRequest{
		SnapshotUuid: snapshotUUID, Target: target, Project: project,
	})
	if err != nil {
		return nil, err
	}
	b := resp.GetBackup()
	if b == nil {
		return nil, nil
	}
	return &VolumeBackupInfo{
		URL:          b.Url,
		VolumeUUID:   b.VolumeUuid,
		SnapshotUUID: b.SnapshotUuid,
		Project:      b.Project,
		SizeBytes:    b.SizeBytes,
		State:        b.State,
		Error:        b.Error,
		CreatedAt:    tsDate(b.CreatedAtUnixNs),
	}, nil
}

func (c *Client) ListVolumeBackups(ctx context.Context, target, volumeUUID, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListVolumeBackups", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_ = opts
	resp, err := rpc.ListVolumeBackups(cctx, &weftv1.ListVolumeBackupsRequest{
		Target: target, VolumeUuid: volumeUUID, Project: project,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(resp.GetBackups()))
	for _, b := range resp.GetBackups() {
		out = append(out, map[string]any{
			"url":           b.Url,
			"volume_uuid":   b.VolumeUuid,
			"snapshot_uuid": b.SnapshotUuid,
			"project":       b.Project,
			"size_bytes":    b.SizeBytes,
			"state":         b.State,
			"error":         b.Error,
			"created":       tsDate(b.CreatedAtUnixNs),
		})
	}
	return out, "", nil
}

func (c *Client) DeleteVolumeBackup(ctx context.Context, url string) (retErr error) {
	defer c.measured("DeleteVolumeBackup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteVolumeBackup(cctx, &weftv1.DeleteVolumeBackupRequest{Url: url})
	return err
}

func (c *Client) RestoreVolumeBackup(ctx context.Context, url, newVolumeName, project string) (retErr error) {
	defer c.measured("RestoreVolumeBackup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RestoreVolumeBackup(cctx, &weftv1.RestoreVolumeBackupRequest{
		Url: url, NewVolumeName: newVolumeName, Project: project,
	})
	return err
}

// -----------------------------------------------------------------
// Inventory : AZs + Racks (weft-proto v0.7.0)
//
// These methods wrap the AZ + Rack RPCs that landed in v0.7.0 of
// the proto. The webui's api_inventory handlers can now reach the
// live registry instead of writing only into resourceByID — see
// the live-first fallback pattern in api_inventory.go.

// ListAZs returns every registered AZ + the derived rack/host
// counts the proto's AZInfo carries.
func (c *Client) ListAZs(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListAZs", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListAZs(cctx, &weftv1.ListAZsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetAzs()))
	for _, a := range resp.GetAzs() {
		out = append(out, map[string]any{
			"uuid":    a.Uuid,
			"code":    a.Code,
			"name":    a.Name,
			"region":  a.Region,
			"status":  a.Status,
			"racks":   a.Racks,
			"hosts":   a.Hosts,
			"created": tsDate(a.CreatedAtUnixNs),
		})
	}
	return out, nil
}

// CreateAZ registers a new AZ. Returns the assigned UUID + a
// `created` flag (false when the code already lived in the
// registry — idempotent insert).
func (c *Client) CreateAZ(ctx context.Context, code, name, region, statusValue string) (uuid string, created bool, retErr error) {
	defer c.measured("CreateAZ", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateAZ(cctx, &weftv1.CreateAZRequest{
		Code: code, Name: name, Region: region, Status: statusValue,
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetAz().GetUuid(), resp.GetCreated(), nil
}

// UpdateAZ patches the mutable fields. Empty strings keep the
// current value (partial PATCH ; matches the server's contract).
func (c *Client) UpdateAZ(ctx context.Context, uuid, name, region, statusValue string) (retErr error) {
	defer c.measured("UpdateAZ", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UpdateAZ(cctx, &weftv1.UpdateAZRequest{
		Uuid: uuid, Name: name, Region: region, Status: statusValue,
	})
	return err
}

// DeleteAZ removes an AZ. Returns the blocking counts on cascade
// refusal so the caller can surface them verbatim.
func (c *Client) DeleteAZ(ctx context.Context, uuid string) (blockedRacks, blockedHosts int32, retErr error) {
	defer c.measured("DeleteAZ", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return 0, 0, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.DeleteAZ(cctx, &weftv1.DeleteAZRequest{Uuid: uuid})
	if err != nil {
		// The server still returns a partial response carrying the
		// blocking counts on cascade refusal ; surface them
		// alongside the error so the UI can render the right
		// "drain X first" hint.
		if resp != nil {
			return resp.GetBlockedByRacks(), resp.GetBlockedByHosts(), err
		}
		return 0, 0, err
	}
	return 0, 0, nil
}

// ListRacks returns every rack ; azUUID == "" lists across every AZ.
func (c *Client) ListRacks(ctx context.Context, azUUID string) (rows []map[string]any, retErr error) {
	defer c.measured("ListRacks", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListRacks(cctx, &weftv1.ListRacksRequest{AzUuid: azUUID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetRacks()))
	for _, r := range resp.GetRacks() {
		out = append(out, map[string]any{
			"uuid":     r.Uuid,
			"az_uuid":  r.AzUuid,
			"code":     r.Code,
			"name":     r.Name,
			"status":   r.Status,
			"height_u": r.HeightU,
			"hosts":    r.Hosts,
			"created":  tsDate(r.CreatedAtUnixNs),
		})
	}
	return out, nil
}

// CreateRack registers a new rack under azUUID.
func (c *Client) CreateRack(ctx context.Context, azUUID, code, name, statusValue string, heightU int32) (uuid string, created bool, retErr error) {
	defer c.measured("CreateRack", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateRack(cctx, &weftv1.CreateRackRequest{
		AzUuid: azUUID, Code: code, Name: name, Status: statusValue, HeightU: heightU,
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetRack().GetUuid(), resp.GetCreated(), nil
}

// UpdateRack patches the mutable fields. heightU == -1 = "keep
// current" (proto3 int32 sentinel ; matches the server's contract).
func (c *Client) UpdateRack(ctx context.Context, uuid, name, statusValue string, heightU int32) (retErr error) {
	defer c.measured("UpdateRack", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UpdateRack(cctx, &weftv1.UpdateRackRequest{
		Uuid: uuid, Name: name, Status: statusValue, HeightU: heightU,
	})
	return err
}

// DeleteRack removes a rack. Returns the blocking host count on
// cascade refusal.
func (c *Client) DeleteRack(ctx context.Context, uuid string) (blockedHosts int32, retErr error) {
	defer c.measured("DeleteRack", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return 0, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.DeleteRack(cctx, &weftv1.DeleteRackRequest{Uuid: uuid})
	if err != nil {
		if resp != nil {
			return resp.GetBlockedByHosts(), err
		}
		return 0, err
	}
	return 0, nil
}

// -----------------------------------------------------------------
// Network plane : Subnet + LoadBalancer + DNSZone + DNSRecord
// (weft-proto v0.8.0)
//
// These methods wrap the 20 network-plane RPCs that landed in
// v0.8.0 of the proto. The webui's api_subnets / api_networking
// handlers still write into the local resourceByID store ; now
// that the wclient calls are in place, migrating each CRUD path
// to live-first is mechanical and lands in a follow-up commit so
// this drop stays small and reviewable. CLI parity is already
// achieved : operators using `weft subnet create`,
// `weft lb create`, `weft dns-zone create` and
// `weft dns-record create` reach the same live registry the
// migrated handlers will read.

// --- Subnets -----------------------------------------------------

// ListSubnets returns every subnet under networkUUID ; an empty
// networkUUID lists across every accessible network.
func (c *Client) ListSubnets(ctx context.Context, networkUUID string) (rows []map[string]any, retErr error) {
	defer c.measured("ListSubnets", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSubnets(cctx, &weftv1.ListSubnetsRequest{NetworkUuid: networkUUID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetSubnets()))
	for _, s := range resp.GetSubnets() {
		out = append(out, subnetRow(s))
	}
	return out, nil
}

// GetSubnet fetches a single subnet by UUID.
func (c *Client) GetSubnet(ctx context.Context, uuid string) (row map[string]any, retErr error) {
	defer c.measured("GetSubnet", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetSubnet(cctx, &weftv1.GetSubnetRequest{Uuid: uuid})
	if err != nil {
		return nil, err
	}
	return subnetRow(resp.GetSubnet()), nil
}

// CreateSubnet carves a new subnet under networkUUID. The cidr is
// immutable for the lifetime of the subnet ; gateway + dnsServers
// can move via UpdateSubnet.
func (c *Client) CreateSubnet(ctx context.Context, networkUUID, cidr, name, description, gateway string, dnsServers []string) (uuid string, created bool, retErr error) {
	defer c.measured("CreateSubnet", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateSubnet(cctx, &weftv1.CreateSubnetRequest{
		NetworkUuid: networkUUID,
		Name:        name,
		Description: description,
		Cidr:        cidr,
		Gateway:     gateway,
		DnsServers:  dnsServers,
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetSubnet().GetUuid(), resp.GetCreated(), nil
}

// UpdateSubnet patches the mutable fields. Empty strings keep the
// current value (partial PATCH). dnsServers == nil keeps the
// current list ; clearDNSServers=true explicitly clears it
// (proto3 repeated has no nil/empty distinction on the wire — see
// the proto's UpdateSubnetRequest comment).
func (c *Client) UpdateSubnet(ctx context.Context, uuid, name, description, gateway string, dnsServers []string, clearDNSServers bool) (retErr error) {
	defer c.measured("UpdateSubnet", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UpdateSubnet(cctx, &weftv1.UpdateSubnetRequest{
		Uuid:            uuid,
		Name:            name,
		Description:     description,
		Gateway:         gateway,
		ClearDnsServers: clearDNSServers,
		DnsServers:      dnsServers,
	})
	return err
}

// DeleteSubnet removes a subnet. The parent network's port
// registry is the source of truth for cascade safety — the server
// refuses while allocations remain.
func (c *Client) DeleteSubnet(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteSubnet", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteSubnet(cctx, &weftv1.DeleteSubnetRequest{Uuid: uuid})
	return err
}

func subnetRow(s *weftv1.SubnetInfo) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"uuid":         s.GetUuid(),
		"network_uuid": s.GetNetworkUuid(),
		"project_uuid": s.GetProjectUuid(),
		"name":         s.GetName(),
		"description":  s.GetDescription(),
		"cidr":         s.GetCidr(),
		"gateway":      s.GetGateway(),
		"dns_servers":  append([]string(nil), s.GetDnsServers()...),
		"created":      tsDate(s.GetCreatedAtUnixNs()),
	}
}

// --- LoadBalancers -----------------------------------------------

// ListLoadBalancers returns every LB in projectUUID ; an empty
// projectUUID falls back to the caller's accessible set.
func (c *Client) ListLoadBalancers(ctx context.Context, projectUUID string) (rows []map[string]any, retErr error) {
	defer c.measured("ListLoadBalancers", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListLoadBalancers(cctx, &weftv1.ListLoadBalancersRequest{Project: projectUUID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetLoadBalancers()))
	for _, lb := range resp.GetLoadBalancers() {
		out = append(out, loadBalancerRow(lb))
	}
	return out, nil
}

// GetLoadBalancer fetches a single LB by UUID.
func (c *Client) GetLoadBalancer(ctx context.Context, uuid string) (row map[string]any, retErr error) {
	defer c.measured("GetLoadBalancer", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetLoadBalancer(cctx, &weftv1.GetLoadBalancerRequest{Uuid: uuid})
	if err != nil {
		return nil, err
	}
	return loadBalancerRow(resp.GetLoadBalancer()), nil
}

// CreateLoadBalancer registers a new LB. backends is a slice of
// map[string]any with `address` (string) and `weight` (int /
// int32 / float64) entries — the standard JSON shape the webui's
// handlers already deal in.
func (c *Client) CreateLoadBalancer(ctx context.Context, projectUUID, name, listenAddr, protocol string, backends []map[string]any) (uuid string, created bool, retErr error) {
	defer c.measured("CreateLoadBalancer", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateLoadBalancer(cctx, &weftv1.CreateLoadBalancerRequest{
		Project:    projectUUID,
		Name:       name,
		ListenAddr: listenAddr,
		Protocol:   protocol,
		Backends:   lbBackendsFromRows(backends),
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetLoadBalancer().GetUuid(), resp.GetCreated(), nil
}

// UpdateLoadBalancer patches the mutable listener fields. Empty
// strings keep the current value. Backends are managed separately
// via SetLoadBalancerBackends (atomic replace).
func (c *Client) UpdateLoadBalancer(ctx context.Context, uuid, name, listenAddr, protocol string) (retErr error) {
	defer c.measured("UpdateLoadBalancer", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UpdateLoadBalancer(cctx, &weftv1.UpdateLoadBalancerRequest{
		Uuid:       uuid,
		Name:       name,
		ListenAddr: listenAddr,
		Protocol:   protocol,
	})
	return err
}

// SetLoadBalancerBackends replaces the backend list atomically.
// An empty slice clears every member (LB becomes a black hole
// until the next set call).
func (c *Client) SetLoadBalancerBackends(ctx context.Context, uuid string, backends []map[string]any) (retErr error) {
	defer c.measured("SetLoadBalancerBackends", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetLoadBalancerBackends(cctx, &weftv1.SetLoadBalancerBackendsRequest{
		Uuid:     uuid,
		Backends: lbBackendsFromRows(backends),
	})
	return err
}

// DeleteLoadBalancer removes an LB. Returns the blocking
// FloatingIP count on cascade refusal — the caller unmaps the
// FIP first, then retries.
func (c *Client) DeleteLoadBalancer(ctx context.Context, uuid string) (blockedFips int32, retErr error) {
	defer c.measured("DeleteLoadBalancer", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return 0, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.DeleteLoadBalancer(cctx, &weftv1.DeleteLoadBalancerRequest{Uuid: uuid})
	if err != nil {
		if resp != nil {
			return resp.GetBlockedByFips(), err
		}
		return 0, err
	}
	return 0, nil
}

func loadBalancerRow(lb *weftv1.LoadBalancerInfo) map[string]any {
	if lb == nil {
		return map[string]any{}
	}
	backends := make([]map[string]any, 0, len(lb.GetBackends()))
	for _, b := range lb.GetBackends() {
		backends = append(backends, map[string]any{
			"address": b.GetAddress(),
			"weight":  b.GetWeight(),
		})
	}
	return map[string]any{
		"uuid":         lb.GetUuid(),
		"project_uuid": lb.GetProjectUuid(),
		"name":         lb.GetName(),
		"listen_addr":  lb.GetListenAddr(),
		"protocol":     lb.GetProtocol(),
		"backends":     backends,
		"created":      tsDate(lb.GetCreatedAtUnixNs()),
	}
}

// lbBackendsFromRows projects the webui's JSON-style
// []map[string]any backends into the proto's []*LBBackend. Keys
// recognised : `address` (string) and `weight` (any numeric the
// JSON decoder produces — int, int32, int64, float64). Unknown
// or missing keys collapse to their zero value.
func lbBackendsFromRows(rows []map[string]any) []*weftv1.LBBackend {
	if len(rows) == 0 {
		return nil
	}
	out := make([]*weftv1.LBBackend, 0, len(rows))
	for _, r := range rows {
		addr, _ := r["address"].(string)
		weight := int32(0)
		switch v := r["weight"].(type) {
		case int:
			weight = int32(v)
		case int32:
			weight = v
		case int64:
			weight = int32(v)
		case float64:
			weight = int32(v)
		}
		out = append(out, &weftv1.LBBackend{Address: addr, Weight: weight})
	}
	return out
}

// --- DNS Zones ---------------------------------------------------

// ListDNSZones returns every zone in projectUUID ; an empty
// projectUUID falls back to the caller's accessible set.
func (c *Client) ListDNSZones(ctx context.Context, projectUUID string) (rows []map[string]any, retErr error) {
	defer c.measured("ListDNSZones", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListDNSZones(cctx, &weftv1.ListDNSZonesRequest{Project: projectUUID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetZones()))
	for _, z := range resp.GetZones() {
		out = append(out, dnsZoneRow(z))
	}
	return out, nil
}

// GetDNSZone fetches a single zone by UUID.
func (c *Client) GetDNSZone(ctx context.Context, uuid string) (row map[string]any, retErr error) {
	defer c.measured("GetDNSZone", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetDNSZone(cctx, &weftv1.GetDNSZoneRequest{Uuid: uuid})
	if err != nil {
		return nil, err
	}
	return dnsZoneRow(resp.GetZone()), nil
}

// CreateDNSZone registers a new apex under projectUUID. ttl == 0
// asks the server for the default (3600).
func (c *Client) CreateDNSZone(ctx context.Context, projectUUID, name, soaEmail string, ttl int32) (uuid string, created bool, retErr error) {
	defer c.measured("CreateDNSZone", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateDNSZone(cctx, &weftv1.CreateDNSZoneRequest{
		Project:  projectUUID,
		Name:     name,
		SoaEmail: soaEmail,
		Ttl:      ttl,
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetZone().GetUuid(), resp.GetCreated(), nil
}

// UpdateDNSZone patches the mutable fields. Empty soaEmail keeps
// the current value ; ttl == -1 keeps the current value (proto3
// int32 has no nil ; matches the server's contract).
func (c *Client) UpdateDNSZone(ctx context.Context, uuid, soaEmail string, ttl int32) (retErr error) {
	defer c.measured("UpdateDNSZone", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UpdateDNSZone(cctx, &weftv1.UpdateDNSZoneRequest{
		Uuid:     uuid,
		SoaEmail: soaEmail,
		Ttl:      ttl,
	})
	return err
}

// DeleteDNSZone removes a zone. Returns the blocking record count
// on cascade refusal so the UI can surface a "drain X records
// first" hint verbatim.
func (c *Client) DeleteDNSZone(ctx context.Context, uuid string) (blockedRecords int32, retErr error) {
	defer c.measured("DeleteDNSZone", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return 0, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.DeleteDNSZone(cctx, &weftv1.DeleteDNSZoneRequest{Uuid: uuid})
	if err != nil {
		if resp != nil {
			return resp.GetBlockedByRecords(), err
		}
		return 0, err
	}
	return 0, nil
}

func dnsZoneRow(z *weftv1.DNSZoneInfo) map[string]any {
	if z == nil {
		return map[string]any{}
	}
	return map[string]any{
		"uuid":         z.GetUuid(),
		"project_uuid": z.GetProjectUuid(),
		"name":         z.GetName(),
		"soa_email":    z.GetSoaEmail(),
		"ttl":          z.GetTtl(),
		"records":      z.GetRecords(),
		"created":      tsDate(z.GetCreatedAtUnixNs()),
	}
}

// --- DNS Records -------------------------------------------------

// ListDNSRecords returns every record under zoneUUID.
func (c *Client) ListDNSRecords(ctx context.Context, zoneUUID string) (rows []map[string]any, retErr error) {
	defer c.measured("ListDNSRecords", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListDNSRecords(cctx, &weftv1.ListDNSRecordsRequest{ZoneUuid: zoneUUID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetRecords()))
	for _, r := range resp.GetRecords() {
		out = append(out, dnsRecordRow(r))
	}
	return out, nil
}

// CreateDNSRecord registers a new record under zoneUUID.
// recordType is one of "A" | "AAAA" | "CNAME" | "MX" | "TXT" |
// "SRV". priority is only meaningful for MX + SRV (ignored
// otherwise). ttl == 0 inherits the zone default.
func (c *Client) CreateDNSRecord(ctx context.Context, zoneUUID, name, recordType, value string, ttl, priority int32) (uuid string, created bool, retErr error) {
	defer c.measured("CreateDNSRecord", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateDNSRecord(cctx, &weftv1.CreateDNSRecordRequest{
		ZoneUuid: zoneUUID,
		Name:     name,
		Type:     recordType,
		Value:    value,
		Ttl:      ttl,
		Priority: priority,
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetRecord().GetUuid(), resp.GetCreated(), nil
}

// UpdateDNSRecord patches the mutable fields. The proto's
// UpdateDNSRecordRequest only carries value + ttl + priority —
// name + type are immutable in v0.8.0 (delete + recreate to
// rename or change the record class). The wrapper keeps the
// fuller `name, recordType` parameters in the signature for
// caller symmetry but the proto strips them on the wire ; the
// server is the source of truth.
//
// value == "" keeps the current value ; ttl == -1 keeps the
// current value ; priority == -1 keeps the current value
// (proto3 int32 has no nil — matches the server's contract).
func (c *Client) UpdateDNSRecord(ctx context.Context, uuid, name, recordType, value string, ttl, priority int32) (retErr error) {
	_ = name
	_ = recordType
	defer c.measured("UpdateDNSRecord", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UpdateDNSRecord(cctx, &weftv1.UpdateDNSRecordRequest{
		Uuid:     uuid,
		Value:    value,
		Ttl:      ttl,
		Priority: priority,
	})
	return err
}

// DeleteDNSRecord removes a record.
func (c *Client) DeleteDNSRecord(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteDNSRecord", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteDNSRecord(cctx, &weftv1.DeleteDNSRecordRequest{Uuid: uuid})
	return err
}

func dnsRecordRow(r *weftv1.DNSRecordInfo) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	return map[string]any{
		"uuid":      r.GetUuid(),
		"zone_uuid": r.GetZoneUuid(),
		"name":      r.GetName(),
		"type":      r.GetType(),
		"value":     r.GetValue(),
		"ttl":       r.GetTtl(),
		"priority":  r.GetPriority(),
		"created":   tsDate(r.GetCreatedAtUnixNs()),
	}
}

// --- Tier 4-6 : VolumeProperty + Share (extended) + Bucket + SSHKeyCatalogue + SchedulingRule + RegistryRemote (weft-proto v0.9.0)

// --- VolumeProperty ----------------------------------------------

// GetVolumeProperty fetches a single (volumeUUID, key) tuple from the
// volume's free-form metadata bag. The agent owns the canonical store ;
// the webui's existing micro-vm metadata endpoints reuse the same
// shape so the SPA can target either without a wire change.
func (c *Client) GetVolumeProperty(ctx context.Context, volumeUUID, key string) (value string, retErr error) {
	defer c.measured("GetVolumeProperty", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetVolumeProperty(cctx, &weftv1.GetVolumePropertyRequest{
		VolumeUuid: volumeUUID, Key: key,
	})
	if err != nil {
		return "", err
	}
	return resp.GetProperty().GetValue(), nil
}

// SetVolumeProperty upserts the (volumeUUID, key) -> value tuple.
// Empty value is preserved as-is — the agent's contract treats an
// empty string as a legitimate value, distinct from absence.
func (c *Client) SetVolumeProperty(ctx context.Context, volumeUUID, key, value string) (retErr error) {
	defer c.measured("SetVolumeProperty", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetVolumeProperty(cctx, &weftv1.SetVolumePropertyRequest{
		VolumeUuid: volumeUUID, Key: key, Value: value,
	})
	return err
}

// DeleteVolumeProperty removes a single key from a volume's property
// bag. Idempotent on the server side ; missing keys do not error.
func (c *Client) DeleteVolumeProperty(ctx context.Context, volumeUUID, key string) (retErr error) {
	defer c.measured("DeleteVolumeProperty", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteVolumeProperty(cctx, &weftv1.DeleteVolumePropertyRequest{
		VolumeUuid: volumeUUID, Key: key,
	})
	return err
}

// --- Share extended (Get + Resize) -------------------------------

// GetShare fetches a single share by UUID. Mirrors the ListShares
// projection so the dashboard's row schema stays identical between
// list + get views.
func (c *Client) GetShare(ctx context.Context, uuid string) (row map[string]any, retErr error) {
	defer c.measured("GetShare", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetShare(cctx, &weftv1.GetShareRequest{Uuid: uuid})
	if err != nil {
		return nil, err
	}
	return shareRow(resp.GetShare()), nil
}

// ResizeShare grows a share's hard quota. newSizeGiB must be strictly
// greater than the current size — the server rejects shrinks with
// FailedPrecondition. The proto field is named new_size_gb ; the
// wrapper sticks to "GiB" in the Go signature to match the units used
// elsewhere in the webui (CreateShare's size_gb is similarly GiB on
// the wire — pre-existing naming we live with).
func (c *Client) ResizeShare(ctx context.Context, uuid string, newSizeGiB int64) (retErr error) {
	defer c.measured("ResizeShare", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.ResizeShare(cctx, &weftv1.ResizeShareRequest{
		Uuid: uuid, NewSizeGb: newSizeGiB,
	})
	return err
}

func shareRow(s *weftv1.ShareInfo) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"name":     s.GetName(),
		"uuid":     s.GetUuid(),
		"project":  s.GetProjectUuid(),
		"backend":  s.GetBackend(),
		"size_gb":  s.GetSizeGb(),
		"readonly": s.GetReadonly(),
		"mounts":   s.GetMounts(),
		"status":   s.GetStatus(),
	}
}

// --- Buckets (S3-compatible) -------------------------------------

// ListBuckets returns every bucket in projectUUID ; an empty
// projectUUID falls back to the caller's accessible set.
func (c *Client) ListBuckets(ctx context.Context, projectUUID string) (rows []map[string]any, retErr error) {
	defer c.measured("ListBuckets", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListBuckets(cctx, &weftv1.ListBucketsRequest{Project: projectUUID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetBuckets()))
	for _, b := range resp.GetBuckets() {
		out = append(out, bucketRow(b))
	}
	return out, nil
}

// GetBucket fetches a single bucket by UUID. The response includes
// the secret_access_key + policy fields that the list response omits
// (admin-only by server contract — the wrapper does no extra gating).
func (c *Client) GetBucket(ctx context.Context, uuid string) (row map[string]any, retErr error) {
	defer c.measured("GetBucket", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetBucket(cctx, &weftv1.GetBucketRequest{Uuid: uuid})
	if err != nil {
		return nil, err
	}
	return bucketRow(resp.GetBucket()), nil
}

// CreateBucket registers a new S3 bucket under projectUUID. The proto
// has no policy field on CreateBucketRequest — policies move via
// SetBucketPolicy. When the caller passes a non-empty policy the
// wrapper chains a SetBucketPolicy call after a successful create so
// the create-with-policy affordance round-trips in a single API call
// on the webui's surface.
func (c *Client) CreateBucket(ctx context.Context, projectUUID, name, endpoint, region, accessKeyID, secretAccessKey, policy string) (uuid string, created bool, retErr error) {
	defer c.measured("CreateBucket", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateBucket(cctx, &weftv1.CreateBucketRequest{
		Project:         projectUUID,
		Name:            name,
		Endpoint:        endpoint,
		Region:          region,
		AccessKeyId:     accessKeyID,
		SecretAccessKey: secretAccessKey,
	})
	if err != nil {
		return "", false, err
	}
	newUUID := resp.GetBucket().GetUuid()
	if policy != "" && newUUID != "" {
		// Best-effort policy attach on a freshly created bucket. A
		// policy that fails to apply does not roll back the bucket
		// itself — the caller sees the error and can retry the
		// SetBucketPolicy independently.
		cctx2, cancel2 := rpcCtx(withBearer(ctx))
		defer cancel2()
		if _, perr := rpc.SetBucketPolicy(cctx2, &weftv1.SetBucketPolicyRequest{
			Uuid: newUUID, Policy: policy,
		}); perr != nil {
			return newUUID, resp.GetCreated(), perr
		}
	}
	return newUUID, resp.GetCreated(), nil
}

// DeleteBucket removes a bucket. The server is the source of truth for
// cascade safety (objects must be drained first ; the policy is
// removed atomically with the bucket).
func (c *Client) DeleteBucket(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteBucket", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteBucket(cctx, &weftv1.DeleteBucketRequest{Uuid: uuid})
	return err
}

// GetBucketPolicy returns the IAM-style policy JSON for a bucket.
// Empty string means "no policy" (not an error).
func (c *Client) GetBucketPolicy(ctx context.Context, uuid string) (policy string, retErr error) {
	defer c.measured("GetBucketPolicy", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.GetBucketPolicy(cctx, &weftv1.GetBucketPolicyRequest{Uuid: uuid})
	if err != nil {
		return "", err
	}
	return resp.GetPolicy(), nil
}

// SetBucketPolicy atomically replaces the bucket policy with the
// supplied JSON. An empty policy clears it.
func (c *Client) SetBucketPolicy(ctx context.Context, uuid, policy string) (retErr error) {
	defer c.measured("SetBucketPolicy", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetBucketPolicy(cctx, &weftv1.SetBucketPolicyRequest{
		Uuid: uuid, Policy: policy,
	})
	return err
}

func bucketRow(b *weftv1.BucketInfo) map[string]any {
	if b == nil {
		return map[string]any{}
	}
	return map[string]any{
		"uuid":              b.GetUuid(),
		"project_uuid":      b.GetProjectUuid(),
		"name":              b.GetName(),
		"endpoint":          b.GetEndpoint(),
		"region":            b.GetRegion(),
		"access_key_id":     b.GetAccessKeyId(),
		"secret_access_key": b.GetSecretAccessKey(),
		"policy":            b.GetPolicy(),
		"created":           tsDate(b.GetCreatedAtUnixNs()),
	}
}

// --- SSHKey catalogue (cluster-wide) ------------------------------

// ListSSHKeyCatalogue returns every entry in the cluster-wide SSH-key
// catalogue. The proto has no project / scope filter — the catalogue
// is global by design (per-VM authorization layers on top via
// MicroVMSSHKey assignments).
func (c *Client) ListSSHKeyCatalogue(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListSSHKeyCatalogue", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSSHKeyCatalogue(cctx, &weftv1.ListSSHKeyCatalogueRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetKeys()))
	for _, k := range resp.GetKeys() {
		out = append(out, sshKeyCatalogueRow(k))
	}
	return out, nil
}

// AddSSHKeyCatalogue registers a single key. The server parses the
// public key, computes the SHA256 fingerprint, and rejects unknown
// algorithms. Idempotent on (name, fingerprint).
func (c *Client) AddSSHKeyCatalogue(ctx context.Context, name, publicKey, comment string) (uuid, fingerprint string, added bool, retErr error) {
	defer c.measured("AddSSHKeyCatalogue", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.AddSSHKeyCatalogue(cctx, &weftv1.AddSSHKeyCatalogueRequest{
		Name: name, PublicKey: publicKey, Comment: comment,
	})
	if err != nil {
		return "", "", false, err
	}
	return resp.GetKey().GetUuid(), resp.GetKey().GetFingerprint(), resp.GetAdded(), nil
}

// RemoveSSHKeyCatalogue removes one entry by UUID. The proto also
// accepts a name as a fallback ; the wrapper sticks to UUID because
// that's the stable handle once the entry is in the catalogue.
func (c *Client) RemoveSSHKeyCatalogue(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("RemoveSSHKeyCatalogue", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.RemoveSSHKeyCatalogue(cctx, &weftv1.RemoveSSHKeyCatalogueRequest{Uuid: uuid})
	return err
}

// ImportSSHKeyCatalogue ingests an authorized_keys-formatted blob in
// one shot. The server splits by line, parses each (skipping
// comments / blanks), and dedups against the existing fingerprints.
// Returns (imported, skipped) counts ; the SPA renders them as the
// op's progress.
func (c *Client) ImportSSHKeyCatalogue(ctx context.Context, blob string) (imported int32, skipped int32, retErr error) {
	defer c.measured("ImportSSHKeyCatalogue", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return 0, 0, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ImportSSHKeyCatalogue(cctx, &weftv1.ImportSSHKeyCatalogueRequest{Blob: blob})
	if err != nil {
		return 0, 0, err
	}
	return int32(len(resp.GetImported())), resp.GetSkippedDuplicates(), nil
}

func sshKeyCatalogueRow(k *weftv1.SSHKeyCatalogueEntry) map[string]any {
	if k == nil {
		return map[string]any{}
	}
	return map[string]any{
		"uuid":        k.GetUuid(),
		"name":        k.GetName(),
		"public_key":  k.GetPublicKey(),
		"fingerprint": k.GetFingerprint(),
		"comment":     k.GetComment(),
		"added":       tsDate(k.GetAddedAtUnixNs()),
	}
}

// --- Scheduling rules (cluster-wide) -----------------------------

// ListSchedulingRules returns every scheduling rule in the cluster.
// The proto has no project / scope filter — rules are cluster-wide.
// The projectUUID parameter is accepted for symmetry with the rest of
// the webui's listing wrappers but is currently ignored on the wire ;
// caller-side filtering happens after the row projection lands.
func (c *Client) ListSchedulingRules(ctx context.Context, projectUUID string) (rows []map[string]any, retErr error) {
	_ = projectUUID
	defer c.measured("ListSchedulingRules", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSchedulingRules(cctx, &weftv1.ListSchedulingRulesRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetRules()))
	for _, r := range resp.GetRules() {
		out = append(out, schedulingRuleRow(r))
	}
	return out, nil
}

// CreateSchedulingRule registers a new placement rule. The proto's
// CreateSchedulingRuleRequest has no project field — rules are
// cluster-wide. The projectUUID parameter is accepted for symmetry
// with the rest of the webui's create wrappers and dropped on the wire.
func (c *Client) CreateSchedulingRule(ctx context.Context, projectUUID, name, selector string, targetCount int32, antiAffinity string) (uuid string, created bool, retErr error) {
	_ = projectUUID
	defer c.measured("CreateSchedulingRule", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateSchedulingRule(cctx, &weftv1.CreateSchedulingRuleRequest{
		Name:         name,
		Selector:     selector,
		TargetCount:  targetCount,
		AntiAffinity: antiAffinity,
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetRule().GetUuid(), resp.GetCreated(), nil
}

// UpdateSchedulingRule patches the mutable fields. selector / antiAffinity
// empty = keep current ; targetCount == -1 = keep current (proto3 int32
// has no nil — matches the server's contract).
func (c *Client) UpdateSchedulingRule(ctx context.Context, uuid, name, selector string, targetCount int32, antiAffinity string) (retErr error) {
	_ = name // The proto's UpdateSchedulingRuleRequest does not carry name (immutable in v0.9.0). Kept in the signature for caller symmetry ; dropped on the wire.
	defer c.measured("UpdateSchedulingRule", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.UpdateSchedulingRule(cctx, &weftv1.UpdateSchedulingRuleRequest{
		Uuid:         uuid,
		Selector:     selector,
		TargetCount:  targetCount,
		AntiAffinity: antiAffinity,
	})
	return err
}

// DeleteSchedulingRule removes a placement rule. The cascade contract
// (whether bound VMs prevent deletion) lives on the server.
func (c *Client) DeleteSchedulingRule(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteSchedulingRule", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteSchedulingRule(cctx, &weftv1.DeleteSchedulingRuleRequest{Uuid: uuid})
	return err
}

func schedulingRuleRow(r *weftv1.SchedulingRuleInfo) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	return map[string]any{
		"uuid":          r.GetUuid(),
		"name":          r.GetName(),
		"selector":      r.GetSelector(),
		"target_count":  r.GetTargetCount(),
		"anti_affinity": r.GetAntiAffinity(),
		"created":       tsDate(r.GetCreatedAtUnixNs()),
	}
}

// --- Registry remotes (cluster-wide) -----------------------------

// ListRegistryRemotes returns every configured OCI registry mirror.
func (c *Client) ListRegistryRemotes(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListRegistryRemotes", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListRegistryRemotes(cctx, &weftv1.ListRegistryRemotesRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetRemotes()))
	for _, r := range resp.GetRemotes() {
		out = append(out, registryRemoteRow(r))
	}
	return out, nil
}

// SetRegistryRemote upserts a remote by name. credentialSecretRef
// refers into the secret store ; empty = anonymous access.
func (c *Client) SetRegistryRemote(ctx context.Context, name, endpoint string, insecure bool, credentialSecretRef string) (uuid string, created bool, retErr error) {
	defer c.measured("SetRegistryRemote", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", false, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.SetRegistryRemote(cctx, &weftv1.SetRegistryRemoteRequest{
		Name:                name,
		Endpoint:            endpoint,
		Insecure:            insecure,
		CredentialSecretRef: credentialSecretRef,
	})
	if err != nil {
		return "", false, err
	}
	return resp.GetRemote().GetUuid(), resp.GetCreated(), nil
}

// DeleteRegistryRemote removes a remote by UUID.
func (c *Client) DeleteRegistryRemote(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteRegistryRemote", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteRegistryRemote(cctx, &weftv1.DeleteRegistryRemoteRequest{Uuid: uuid})
	return err
}

// SearchRegistryRemote proxies a query to the remote registry's
// catalogue endpoint. Today the server returns canned results /
// stub data ; the wrapper's shape is forward-compatible. The result
// is a slice of {repository: "<repo>"} rows so the dashboard's
// existing artifact renderer works without a special case.
func (c *Client) SearchRegistryRemote(ctx context.Context, query string) (results []map[string]any, retErr error) {
	defer c.measured("SearchRegistryRemote", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.SearchRegistryRemote(cctx, &weftv1.SearchRegistryRemoteRequest{Query: query})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetRepositories()))
	for _, r := range resp.GetRepositories() {
		out = append(out, map[string]any{
			"repository":    r,
			"registry_name": resp.GetRegistryName(),
		})
	}
	return out, nil
}

func registryRemoteRow(r *weftv1.RegistryRemoteInfo) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	return map[string]any{
		"uuid":                  r.GetUuid(),
		"name":                  r.GetName(),
		"endpoint":              r.GetEndpoint(),
		"insecure":              r.GetInsecure(),
		"credential_secret_ref": r.GetCredentialSecretRef(),
		"created":               tsDate(r.GetCreatedAtUnixNs()),
	}
}
