// Package wclient is the thin adapter between the weft-webui HTTP handlers
// and the real weft control-plane gRPC API (vzd / weft-client). It hides
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

	vzclient "github.com/openweft/weft-client"
	vzdv1 "github.com/openweft/weft-proto"
	"google.golang.org/grpc"
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
type Client struct {
	socket string
	mu     sync.Mutex
	conn   *grpc.ClientConn
	rpc    vzdv1.VzdServiceClient
}

// New builds a client that will dial socket on the first RPC. socket follows
// the weft-client convention : a unix path (e.g. ~/.vzd/vzd.sock) or an
// ssh:// URL routed through the SSH transport.
func New(socket string) *Client { return &Client{socket: socket} }

func (c *Client) dial() (vzdv1.VzdServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpc != nil {
		return c.rpc, nil
	}
	rpc, conn, err := vzclient.Client(c.socket)
	if err != nil {
		return nil, err
	}
	c.rpc, c.conn = rpc, conn
	return c.rpc, nil
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

func (c *Client) ListProjects(ctx context.Context) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
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

func (c *Client) ListVMs(ctx context.Context, project string) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
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

func (c *Client) ListNetworks(ctx context.Context, project string) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
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

func (c *Client) ListHosts(ctx context.Context, az string) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
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

func (c *Client) ListVolumes(ctx context.Context, project string) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
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

func (c *Client) ListUsers(ctx context.Context) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
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

func (c *Client) ListSecurityGroups(ctx context.Context, project string) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
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
