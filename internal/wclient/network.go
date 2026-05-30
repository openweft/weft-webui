// network.go — gRPC client for weft-network, the sibling control
// plane that owns Routers / Load Balancers / DNS / Scheduling Rules.
//
// Same shape as the main Client in this package (lazy dial, bearer
// from request context, measured histogram per call). Separate
// process / socket from the agent : the operator sets
// --weft-network-socket and the webui reaches both daemons in
// parallel without coupling.
//
// When NetworkSocket isn't configured (the common dev case for now),
// NetworkClient stays nil and handlers fall back to their mock store.
package wclient

import (
	"context"
	"errors"
	"sync"
	"time"

	netv1 "github.com/openweft/weft-network-proto"
	vzclient "github.com/openweft/weft-client"
	"google.golang.org/grpc"
)

// NetworkClient mirrors Client but for weft-network. Dialed lazily on
// first call, the connection lives for the process lifetime.
type NetworkClient struct {
	socket  string
	mu      sync.Mutex
	conn    *grpc.ClientConn
	rpc     netv1.NetworkControlPlaneClient
	Metrics telemetryRecorder // interface so we don't import the telemetry pkg here
}

// telemetryRecorder is the subset of telemetry.Recorder this package
// actually uses. Letting the consumer pass any matching type avoids a
// telemetry → wclient → telemetry cycle.
type telemetryRecorder interface {
	ObserveGRPC(method, status string, dur time.Duration)
}

// NewNetwork builds the client. socket follows the weft-client
// convention : unix path or ssh:// URL.
func NewNetwork(socket string) *NetworkClient { return &NetworkClient{socket: socket} }

func (c *NetworkClient) dial() (netv1.NetworkControlPlaneClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpc != nil {
		return c.rpc, nil
	}
	// Reuse weft-client's Dial : it handles ssh:// transport, the
	// bearer interceptor stack, etc. We only need a fresh
	// NetworkControlPlaneClient on top of the resulting conn.
	conn, err := vzclient.Dial(c.socket)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	c.rpc = netv1.NewNetworkControlPlaneClient(conn)
	return c.rpc, nil
}

// Close releases the cached connection (best-effort).
func (c *NetworkClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.rpc = nil, nil
	return err
}

// measured wraps the same defer-closure pattern as Client.measured ;
// kept here so the network client doesn't reach into the agent
// client's private helpers.
func (c *NetworkClient) measured(method string, errPtr *error) func() {
	if c.Metrics == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		st := "ok"
		if errPtr != nil && *errPtr != nil {
			st = grpcCodeName(*errPtr)
		}
		c.Metrics.ObserveGRPC(method, st, time.Since(start))
	}
}

// grpcCodeName is the local shortcut to status.Code(err).String().
// Tiny helper so the per-method defers stay readable.
func grpcCodeName(err error) string {
	if err == nil {
		return "ok"
	}
	// Avoid importing grpc/status here for one call ; the err message
	// is good enough for the metric label cardinality.
	return "error"
}

// ListRouters returns the registered routers. Same row shape the
// existing static `routers` resource emits, so the table can render
// either source without code change.
func (c *NetworkClient) ListRouters(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListRouters", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListRouters(cctx, &netv1.ListRoutersRequest{Project: project})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("nil ListRouters response")
	}
	out := make([]map[string]any, 0, len(resp.GetRouters()))
	for _, r := range resp.GetRouters() {
		out = append(out, map[string]any{
			"uuid":       r.Uuid,
			"name":       r.Name,
			"type":       r.Kind,      // table column is "type"
			"backend":    r.Backend,
			"networks":   joinStrings(r.Networks),
			"external":   r.External,
			"peer_state": r.PeerState,
			"project":    r.Project,
			"status":     r.Status,
		})
	}
	return out, nil
}

func (c *NetworkClient) ListLoadBalancers(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListLoadBalancers", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListLoadBalancers(cctx, &netv1.ListLoadBalancersRequest{Project: project})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetLoadBalancers()))
	for _, lb := range resp.GetLoadBalancers() {
		out = append(out, map[string]any{
			"uuid":       lb.Uuid,
			"name":       lb.Name,
			"mode":       lb.Mode,
			"address":    lb.Address,
			"port":       lb.Port,
			"backends":   joinStrings(lb.Backends),
			"az":         lb.Az,
			"controller": lb.Controller,
			"project":    lb.Project,
			"status":     lb.Status,
		})
	}
	return out, nil
}

func (c *NetworkClient) ListDNSZones(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListDNSZones", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListDNSZones(cctx, &netv1.ListDNSZonesRequest{Project: project})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetZones()))
	for _, z := range resp.GetZones() {
		out = append(out, map[string]any{
			"uuid":        z.Uuid,
			"name":        z.Name,
			"role":        z.Role,
			"records":     z.Records,
			"ttl_default": z.TtlDefault,
			"backend":     z.Backend,
			"push_target": z.PushTarget,
			"push_state":  z.PushState,
			"project":     z.Project,
			"status":      z.Status,
		})
	}
	return out, nil
}

func (c *NetworkClient) ListDNSRecords(ctx context.Context, zoneUUID string) (rows []map[string]any, retErr error) {
	defer c.measured("ListDNSRecords", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListDNSRecords(cctx, &netv1.ListDNSRecordsRequest{ZoneUuid: zoneUUID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetRecords()))
	for _, rec := range resp.GetRecords() {
		out = append(out, map[string]any{
			"uuid":   rec.Uuid,
			"name":   rec.Name,
			"zone":   rec.Zone,
			"type":   rec.Type,
			"value":  rec.Value,
			"ttl":    rec.Ttl,
			"source": rec.Source,
		})
	}
	return out, nil
}

func (c *NetworkClient) ListSchedulingRules(ctx context.Context, project string) (rows []map[string]any, retErr error) {
	defer c.measured("ListSchedulingRules", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSchedulingRules(cctx, &netv1.ListSchedulingRulesRequest{Project: project})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.GetRules()))
	for _, r := range resp.GetRules() {
		// Same row shape the schedulingDB store emits, so the table
		// renders either path identically.
		placement := []string{}
		if r.Az != "" {
			placement = append(placement, "az="+r.Az)
		}
		if r.Rack != "" {
			placement = append(placement, "rack="+r.Rack)
		}
		if r.Host != "" {
			placement = append(placement, "host="+r.Host)
		}
		p := "any"
		if len(placement) > 0 {
			p = joinStrings(placement)
		}
		out = append(out, map[string]any{
			"uuid":      r.Uuid,
			"name":      r.Name,
			"count":     formatRatio(int(r.Ready), int(r.Count)),
			"placement": p,
			"selector":  r.Selector,
			"project":   r.Project,
			"status":    r.Status,
		})
	}
	return out, nil
}

// --- small shared helpers ------------------------------------------

func joinStrings(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

func formatRatio(ready, want int) string {
	if want == 0 && ready == 0 {
		return "0/0"
	}
	// strconv.Itoa would do it, but keeping the helper local matches
	// the rest of the package's "no extra imports for tiny utils" style.
	return itoa(ready) + "/" + itoa(want)
}
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
