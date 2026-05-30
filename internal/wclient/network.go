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
	weftclient "github.com/openweft/weft-client"
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
	conn, err := weftclient.Dial(c.socket)
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
// either source without code change. opts is the page knob shared with
// every other wclient List* (see ListOpts in wclient.go).
func (c *NetworkClient) ListRouters(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListRouters", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListRouters(cctx, &netv1.ListRoutersRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
	}
	if resp == nil {
		return nil, "", errors.New("nil ListRouters response")
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
	return out, resp.GetNextPageToken(), nil
}

func (c *NetworkClient) ListLoadBalancers(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListLoadBalancers", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListLoadBalancers(cctx, &netv1.ListLoadBalancersRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
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
	return out, resp.GetNextPageToken(), nil
}

func (c *NetworkClient) ListDNSZones(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListDNSZones", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListDNSZones(cctx, &netv1.ListDNSZonesRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
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
	return out, resp.GetNextPageToken(), nil
}

func (c *NetworkClient) ListDNSRecords(ctx context.Context, zoneUUID string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListDNSRecords", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListDNSRecords(cctx, &netv1.ListDNSRecordsRequest{
		ZoneUuid: zoneUUID, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
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
	return out, resp.GetNextPageToken(), nil
}

func (c *NetworkClient) ListSchedulingRules(ctx context.Context, project string, opts ListOpts) (rows []map[string]any, next string, retErr error) {
	defer c.measured("ListSchedulingRules", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListSchedulingRules(cctx, &netv1.ListSchedulingRulesRequest{
		Project: project, Limit: opts.Limit, PageToken: opts.PageToken,
	})
	if err != nil {
		return nil, "", err
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
	return out, resp.GetNextPageToken(), nil
}

// --- Mutators ------------------------------------------------------
//
// Every Create takes a typed opts struct so the handler can decode the
// SPA's body once and forward without re-massaging the field names.
// Delete handlers key by UUID (the daemon's stable identifier).

type CreateRouterOpts struct {
	Project, Name, Kind, Backend, External string
	Networks                               []string
}

func (c *NetworkClient) CreateRouter(ctx context.Context, o CreateRouterOpts) (uuid string, retErr error) {
	defer c.measured("CreateRouter", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateRouter(cctx, &netv1.CreateRouterRequest{
		Project: o.Project, Name: o.Name, Kind: o.Kind, Backend: o.Backend,
		Networks: o.Networks, External: o.External,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Router == nil {
		return "", errors.New("nil CreateRouter response")
	}
	return resp.Router.Uuid, nil
}

func (c *NetworkClient) DeleteRouter(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteRouter", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteRouter(cctx, &netv1.DeleteRouterRequest{Uuid: uuid})
	return err
}

type CreateLoadBalancerOpts struct {
	Project, Name, Mode, AZ string
	Port                    uint32
	Backends                []string
}

func (c *NetworkClient) CreateLoadBalancer(ctx context.Context, o CreateLoadBalancerOpts) (uuid string, retErr error) {
	defer c.measured("CreateLoadBalancer", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateLoadBalancer(cctx, &netv1.CreateLoadBalancerRequest{
		Project: o.Project, Name: o.Name, Mode: o.Mode, Port: o.Port,
		Backends: o.Backends, Az: o.AZ,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.LoadBalancer == nil {
		return "", errors.New("nil CreateLoadBalancer response")
	}
	return resp.LoadBalancer.Uuid, nil
}

func (c *NetworkClient) DeleteLoadBalancer(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteLoadBalancer", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteLoadBalancer(cctx, &netv1.DeleteLoadBalancerRequest{Uuid: uuid})
	return err
}

func (c *NetworkClient) SetLoadBalancerBackends(ctx context.Context, uuid string, backends []string) (retErr error) {
	defer c.measured("SetLoadBalancerBackends", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.SetLoadBalancerBackends(cctx, &netv1.SetLoadBalancerBackendsRequest{
		Uuid: uuid, Backends: backends,
	})
	return err
}

type CreateDNSZoneOpts struct {
	Project, Name, Role, PushTarget string
	TTLDefault                      int32
}

func (c *NetworkClient) CreateDNSZone(ctx context.Context, o CreateDNSZoneOpts) (uuid string, retErr error) {
	defer c.measured("CreateDNSZone", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateDNSZone(cctx, &netv1.CreateDNSZoneRequest{
		Project: o.Project, Name: o.Name, Role: o.Role,
		TtlDefault: o.TTLDefault, PushTarget: o.PushTarget,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Zone == nil {
		return "", errors.New("nil CreateDNSZone response")
	}
	return resp.Zone.Uuid, nil
}

func (c *NetworkClient) DeleteDNSZone(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteDNSZone", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteDNSZone(cctx, &netv1.DeleteDNSZoneRequest{Uuid: uuid})
	return err
}

type CreateDNSRecordOpts struct {
	ZoneUUID, Name, Type, Value string
	TTL                         int32
}

func (c *NetworkClient) CreateDNSRecord(ctx context.Context, o CreateDNSRecordOpts) (uuid string, retErr error) {
	defer c.measured("CreateDNSRecord", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateDNSRecord(cctx, &netv1.CreateDNSRecordRequest{
		ZoneUuid: o.ZoneUUID, Name: o.Name, Type: o.Type, Value: o.Value, Ttl: o.TTL,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Record == nil {
		return "", errors.New("nil CreateDNSRecord response")
	}
	return resp.Record.Uuid, nil
}

func (c *NetworkClient) DeleteDNSRecord(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteDNSRecord", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteDNSRecord(cctx, &netv1.DeleteDNSRecordRequest{Uuid: uuid})
	return err
}

type CreateSchedulingRuleNetOpts struct {
	Project, Name, Selector, AZ, Rack, Host string
	Count                                   int32
}

func (c *NetworkClient) CreateSchedulingRule(ctx context.Context, o CreateSchedulingRuleNetOpts) (uuid string, retErr error) {
	defer c.measured("CreateSchedulingRule", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.CreateSchedulingRule(cctx, &netv1.CreateSchedulingRuleRequest{
		Project: o.Project, Name: o.Name, Count: o.Count,
		Selector: o.Selector, Az: o.AZ, Rack: o.Rack, Host: o.Host,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Rule == nil {
		return "", errors.New("nil CreateSchedulingRule response")
	}
	return resp.Rule.Uuid, nil
}

func (c *NetworkClient) DeleteSchedulingRule(ctx context.Context, uuid string) (retErr error) {
	defer c.measured("DeleteSchedulingRule", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	_, err = rpc.DeleteSchedulingRule(cctx, &netv1.DeleteSchedulingRuleRequest{Uuid: uuid})
	return err
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
