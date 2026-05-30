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
	vzclient "github.com/openweft/weft-client"
	vzdv1 "github.com/openweft/weft-proto"
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
	rpc     vzdv1.VzdServiceClient
	Metrics *telemetry.Recorder
}

// New builds a client that will dial socket on the first RPC. socket follows
// the weft-client convention : a unix path (e.g. ~/.weft/weft.sock —
// some older deployments still see the legacy ~/.vzd/vzd.sock) or an
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

func (c *Client) dial() (vzdv1.VzdServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpc != nil {
		return c.rpc, nil
	}
	// We install our own interceptors here so the bearer comes from the
	// request context (one access token per signed-in user) rather than
	// the on-disk cache vzclient.CachedTokenSource() reads. Vzclient's
	// Client() already installs its own bearer interceptor on top — both
	// stamp `authorization` metadata, and the per-request one wins when
	// it sets a value (metadata.AppendToOutgoingContext concatenates,
	// weft-agent's validator accepts the first valid bearer).
	rpc, conn, err := vzclient.Client(c.socket)
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

// Each List* below returns dashboard rows whose keys match the columns the
// frontend already declares for that resource. Mock rows share the same
// shape (see internal/server/resources.go), so the table renders either.

func (c *Client) ListProjects(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListProjects", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListProjects(cctx, &vzdv1.ListProjectsRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("nil ListProjects response")
	}
	out := make([]map[string]any, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		out = append(out, map[string]any{
			"name":    p.Name,
			"uuid":    p.Uuid,
			"created": tsDate(p.CreatedAtUnixNs),
		})
	}
	return out, nil
}

func (c *Client) ListVMs(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListVMs", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListVMs(cctx, &vzdv1.ListVMsRequest{Project: project})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetVms()))
	for _, v := range resp.GetVms() {
		out = append(out, map[string]any{
			"name":    v.Name,
			"image":   v.Image,
			"status":  vzclient.StateString(v.State),
			"cpu":     v.Cpu,
			"mem_mb":  v.MemMb,
			"disk_gb": v.DiskGb,
			"ip":      v.Ip,
			"project": v.Project,
		})
	}
	return out, nil
}

func (c *Client) ListNetworks(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListNetworks", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListNetworks(cctx, &vzdv1.ListNetworksRequest{Project: project})
	if err != nil {
		return nil, err
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
	return out, nil
}

func (c *Client) ListHosts(ctx context.Context, az string) (rows []map[string]any, retErr error) {
	defer c.measured("ListHosts", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListHosts(cctx, &vzdv1.ListHostsRequest{Az: az})
	if err != nil {
		return nil, err
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
	return out, nil
}

func (c *Client) ListVolumes(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListVolumes", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListVolumes(cctx, &vzdv1.ListVolumesRequest{Project: project})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetVolumes()))
	for _, v := range resp.GetVolumes() {
		out = append(out, map[string]any{
			"name":        v.Name,
			"size_gib":    v.SizeGib,
			"format":      v.Format,
			"attached_to": v.AttachedToUuid,
			"project":     v.ProjectUuid,
			"created":     tsDate(v.CreatedAtUnixNs),
		})
	}
	return out, nil
}

func (c *Client) ListUsers(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListUsers", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListUsers(cctx, &vzdv1.ListUsersRequest{})
	if err != nil {
		return nil, err
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
	return out, nil
}

func (c *Client) ListSecurityGroups(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListSecurityGroups", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSecurityGroups(cctx, &vzdv1.ListSecurityGroupsRequest{Project: project})
	if err != nil {
		return nil, err
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
	return out, nil
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
	resp, err := rpc.CreateProject(cctx, &vzdv1.CreateProjectRequest{Name: name})
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
	_, err = rpc.DeleteProject(cctx, &vzdv1.DeleteProjectRequest{Uuid: uuid})
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
	resp, err := rpc.ListProjectMembers(cctx, &vzdv1.ListProjectMembersRequest{ProjectUuid: projectUUID})
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
	_, err = rpc.AddProjectMember(cctx, &vzdv1.AddProjectMemberRequest{
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
	_, err = rpc.RemoveProjectMember(cctx, &vzdv1.RemoveProjectMemberRequest{
		ProjectUuid: projectUUID, UserUuid: userUUID,
	})
	return err
}

// CreateVM creates a new microVM. The proto carries name/image/cpu/
// mem/disk/sshPub/project ; flavor mapping (tenant view) is the
// webui's responsibility.
type CreateVMOpts struct {
	Name, Image, Project, SSHPubKey string
	CPU                             uint32
	MemMB, DiskGB                   uint64
}

func (c *Client) CreateVM(ctx context.Context, o CreateVMOpts) (retErr error) {
	defer c.measured("CreateVM", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.CreateVM(cctx, &vzdv1.CreateVMRequest{
		Name: o.Name, Image: o.Image, Project: o.Project,
		Cpu: o.CPU, MemMb: o.MemMB, DiskGb: o.DiskGB, SshPub: o.SSHPubKey,
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
	_, err = rpc.StartVM(cctx, &vzdv1.StartVMRequest{Name: name, Project: project})
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
	_, err = rpc.StopVM(cctx, &vzdv1.StopVMRequest{Name: name, Project: project})
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
	_, err = rpc.DeleteVM(cctx, &vzdv1.DeleteVMRequest{Name: name, Project: project})
	return err
}

// VMStatus returns the live VMInfo for a single VM. Marshalled to the
// same map shape the list endpoints already emit, so the drawer can
// reuse the table-cell helpers (status badges, etc.).
func (c *Client) VMStatus(ctx context.Context, name, project string) (info map[string]any, retErr error) {
	defer c.measured("VMStatus", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.VMStatus(cctx, &vzdv1.VMStatusRequest{Name: name, Project: project})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Vm == nil {
		return nil, errors.New("nil VMStatus response")
	}
	v := resp.Vm
	return map[string]any{
		"name":    v.Name,
		"image":   v.Image,
		"status":  vzclient.StateString(v.State),
		"os":      v.Os,
		"cpu":     v.Cpu,
		"mem_mb":  v.MemMb,
		"disk_gb": v.DiskGb,
		"ip":      v.Ip,
	}, nil
}

// VMTimings returns the recorded lifecycle events for a VM (state
// transitions, network up, exec ready, …). Each event has a name, a
// wall-clock ns timestamp, and a meta map. We translate ts_unix_ns to
// an RFC-3339 string so the frontend can render without re-encoding.
func (c *Client) VMTimings(ctx context.Context, name, project string) (events []map[string]any, retErr error) {
	defer c.measured("VMTimings", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.VMTimings(cctx, &vzdv1.VMTimingsRequest{Name: name, Project: project})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetEvents()))
	for _, e := range resp.GetEvents() {
		out = append(out, map[string]any{
			"name": e.Name,
			"ts":   time.Unix(0, e.TsUnixNs).UTC().Format(time.RFC3339Nano),
			"meta": e.Meta,
		})
	}
	return out, nil
}

// VMLogs returns the tail of the console log. tailBytes=0 reads
// everything ; the frontend defaults to a sensible cap (~64 KiB).
func (c *Client) VMLogs(ctx context.Context, name, project string, tailBytes int64) (out map[string]any, retErr error) {
	defer c.measured("VMLogs", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.VMLogs(cctx, &vzdv1.VMLogsRequest{Name: name, Project: project, TailBytes: tailBytes})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"contents":    string(resp.GetContents()),
		"total_bytes": resp.GetTotalBytes(),
	}, nil
}

// CreateNetwork / DeleteNetwork.
type CreateNetworkOpts struct {
	Project, Name, CIDR, Gateway, Type string
	DNSServers                         []string
}

func (c *Client) CreateNetwork(ctx context.Context, o CreateNetworkOpts) (retErr error) {
	defer c.measured("CreateNetwork", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.CreateNetwork(cctx, &vzdv1.CreateNetworkRequest{
		Project: o.Project, Name: o.Name, Cidr: o.CIDR, Gateway: o.Gateway,
		DnsServers: o.DNSServers, Type: o.Type,
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
	_, err = rpc.DeleteNetwork(cctx, &vzdv1.DeleteNetworkRequest{Uuid: uuid})
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
	_, err = rpc.CreateVolume(cctx, &vzdv1.CreateVolumeRequest{
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
	_, err = rpc.DeleteVolume(cctx, &vzdv1.DeleteVolumeRequest{Uuid: uuid})
	return err
}

// SecurityRule mirrors the proto's per-rule shape but exposed as a
// public type so handlers can decode the SPA's payload without
// touching the vzdv1 alias.
type SecurityRule struct {
	Direction       string `json:"direction"` // "ingress" | "egress"
	Protocol        string `json:"protocol"`  // "tcp" | "udp" | "icmp" | "any"
	PortMin         int32  `json:"port_min"`
	PortMax         int32  `json:"port_max"`
	RemoteCIDR      string `json:"remote_cidr"`
	RemoteGroupUUID string `json:"remote_group_uuid"`
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
	rules := make([]*vzdv1.SecurityRule, 0, len(o.Rules))
	for _, r := range o.Rules {
		rules = append(rules, &vzdv1.SecurityRule{
			Direction: r.Direction, Protocol: r.Protocol,
			PortMin: r.PortMin, PortMax: r.PortMax,
			RemoteCidr: r.RemoteCIDR, RemoteGroupUuid: r.RemoteGroupUUID,
		})
	}
	resp, err := rpc.CreateSecurityGroup(cctx, &vzdv1.CreateSecurityGroupRequest{
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
	_, err = rpc.DeleteSecurityGroup(cctx, &vzdv1.DeleteSecurityGroupRequest{Uuid: uuid})
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
	protoRules := make([]*vzdv1.SecurityRule, 0, len(rules))
	for _, r := range rules {
		protoRules = append(protoRules, &vzdv1.SecurityRule{
			Direction: r.Direction, Protocol: r.Protocol,
			PortMin: r.PortMin, PortMax: r.PortMax,
			RemoteCidr: r.RemoteCIDR, RemoteGroupUuid: r.RemoteGroupUUID,
		})
	}
	_, err = rpc.SetSecurityGroupRules(cctx, &vzdv1.SetSecurityGroupRulesRequest{
		Uuid: uuid, Rules: protoRules,
	})
	return err
}

// GetSecurityGroup returns one SG by UUID. There's no dedicated
// GetSecurityGroup RPC ; we list and filter. Good enough at SG-list
// scale (typically dozens, not thousands per project).
func (c *Client) GetSecurityGroup(ctx context.Context, uuid string) (rules []SecurityRule, retErr error) {
	defer c.measured("GetSecurityGroup", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSecurityGroups(cctx, &vzdv1.ListSecurityGroupsRequest{})
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
	return nil, errors.New("security group not found")
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

// UserUUIDByEmail returns the UUID for the given email, or "" if no
// user matches. Walks ListUsers() ; the user count is small.
func (c *Client) UserUUIDByEmail(ctx context.Context, email string) (string, error) {
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListUsers(cctx, &vzdv1.ListUsersRequest{})
	if err != nil {
		return "", err
	}
	want := strings.ToLower(strings.TrimSpace(email))
	for _, u := range resp.GetUsers() {
		if strings.EqualFold(u.Email, want) {
			return u.Uuid, nil
		}
	}
	return "", nil
}

// ProjectUUIDByName resolves a project name to its UUID.
func (c *Client) ProjectUUIDByName(ctx context.Context, name string) (string, error) {
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListProjects(cctx, &vzdv1.ListProjectsRequest{})
	if err != nil {
		return "", err
	}
	for _, p := range resp.Projects {
		if p.Name == name {
			return p.Uuid, nil
		}
	}
	return "", nil
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

func (c *Client) ListTenants(ctx context.Context) (rows []map[string]any, retErr error) {
	defer c.measured("ListTenants", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListTenants(cctx, &vzdv1.ListTenantsRequest{})
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
	resp, err := rpc.CreateTenant(cctx, &vzdv1.CreateTenantRequest{Name: name, Domain: domain})
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
	_, err = rpc.AddTenantAdmin(cctx, &vzdv1.AddTenantAdminRequest{
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
	_, err = rpc.AddTenantMember(cctx, &vzdv1.AddTenantMemberRequest{
		TenantUuid: tenantUUID, Email: email, Groups: groups,
	})
	return err
}

// ---- Quotas ----
//
// The webui's Quotas struct doesn't depend on vzdv1 ; the
// translation table here keeps the two shapes from drifting.

func quotasToProto(in map[string]int) *vzdv1.Quotas {
	return &vzdv1.Quotas{
		Vcpu: int32(in["vcpu"]), RamGib: int32(in["ram_gib"]),
		Volumes: int32(in["volumes"]), VolumesGib: int32(in["volumes_gib"]),
		Shares: int32(in["shares"]), SharesGib: int32(in["shares_gib"]),
		Buckets: int32(in["buckets"]), BucketsGib: int32(in["buckets_gib"]),
		RegistryGib: int32(in["registry_gib"]),
		FloatingIps: int32(in["floating_ips"]),
		Projects:    int32(in["projects"]),
	}
}

func quotasFromProto(q *vzdv1.Quotas) map[string]int {
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
	resp, err := rpc.GetTenantQuota(cctx, &vzdv1.GetTenantQuotaRequest{TenantUuid: tenantUUID})
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
	_, err = rpc.SetTenantQuota(cctx, &vzdv1.SetTenantQuotaRequest{
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
	_, err = rpc.SetProjectQuota(cctx, &vzdv1.SetProjectQuotaRequest{
		ProjectUuid: projectUUID, Quota: quotasToProto(q),
	})
	return err
}

// ---- Shares ----

func (c *Client) ListShares(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListShares", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListShares(cctx, &vzdv1.ListSharesRequest{Project: project})
	if err != nil {
		return nil, err
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
	return out, nil
}

func (c *Client) CreateShare(ctx context.Context, project, name string, sizeGB int64, readonly bool, backend string) (uuid string, retErr error) {
	defer c.measured("CreateShare", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateShare(cctx, &vzdv1.CreateShareRequest{
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
	_, err = rpc.DeleteShare(cctx, &vzdv1.DeleteShareRequest{Uuid: uuid})
	return err
}

// ---- Floating IPs ----

func (c *Client) ListFloatingIPs(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListFloatingIPs", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListFloatingIPs(cctx, &vzdv1.ListFloatingIPsRequest{Project: project})
	if err != nil {
		return nil, err
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
	return out, nil
}

func (c *Client) AllocateFloatingIP(ctx context.Context, project, network string) (uuid, address string, retErr error) {
	defer c.measured("AllocateFloatingIP", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.AllocateFloatingIP(cctx, &vzdv1.AllocateFloatingIPRequest{Project: project, Network: network})
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
	_, err = rpc.ReleaseFloatingIP(cctx, &vzdv1.ReleaseFloatingIPRequest{Uuid: uuid})
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
	_, err = rpc.MapFloatingIP(cctx, &vzdv1.MapFloatingIPRequest{
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
	_, err = rpc.UnmapFloatingIP(cctx, &vzdv1.UnmapFloatingIPRequest{Uuid: uuid})
	return err
}
