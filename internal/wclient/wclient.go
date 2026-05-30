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

	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/telemetry"
	vzclient "github.com/openweft/weft-client"
	vzdv1 "github.com/openweft/weft-proto"
	"google.golang.org/grpc"
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
// the weft-client convention : a unix path (e.g. ~/.vzd/vzd.sock) or an
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
	// vzd's validator accepts the first valid bearer).
	rpc, conn, err := vzclient.Client(c.socket)
	if err != nil {
		return nil, err
	}
	c.rpc, c.conn = rpc, conn
	return c.rpc, nil
}

// withBearer derives a new context that carries the signed-in user's
// access token as gRPC outgoing metadata. No user / no token = the
// context is returned unchanged ; vzd then sees an unauthenticated
// call and decides per its auth-mode whether to reject it.
//
// Bypassing this when a token is already present (e.g. a daemon
// running in dev-mode that ignores auth) keeps the webui usable
// against a no-auth vzd without crashing on every list call.
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
