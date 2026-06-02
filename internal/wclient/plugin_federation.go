package wclient

// plugin_federation.go — webui-facing wrappers around the
// `ListFederationPeers` + plugin catalogue / install RPCs that landed
// in weft-proto v0.5.0. The webui server-side handlers
// (api_federation.go, api_plugins.go) call these to swap the canned
// mock data for live agent state. Both surfaces respect
// IsUnimplemented(err) for graceful fall-through to the local store.

import (
	"context"
	"errors"

	weftv1 "github.com/openweft/weft-proto"
)

// FederationPeerRow mirrors the dashboard's `FederationPeer` shape so
// the api_federation handler can map directly. Kept inside wclient
// rather than re-imported from internal/server to keep the dependency
// arrow pointing the right way (server → wclient, never the other
// way).
type FederationPeerRow struct {
	Name           string
	URL            string
	Region         string
	Weight         int
	LastSeenUnixNS int64
	Status         string
	LastError      string
}

// ListFederationPeers reads the agent's in-process federation.Poller
// snapshot. Per [[openweft_pull_model]] the call is a read of the
// locally-cached pull state ; the agent does NOT trigger a remote pull
// on the hot path.
func (c *Client) ListFederationPeers(ctx context.Context) (rows []FederationPeerRow, retErr error) {
	defer c.measured("ListFederationPeers", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListFederationPeers(cctx, &weftv1.ListFederationPeersRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("nil ListFederationPeers response")
	}
	out := make([]FederationPeerRow, 0, len(resp.GetPeers()))
	for _, p := range resp.GetPeers() {
		out = append(out, FederationPeerRow{
			Name:           p.GetName(),
			URL:            p.GetUrl(),
			Region:         p.GetRegion(),
			Weight:         int(p.GetWeight()),
			LastSeenUnixNS: p.GetLastSeenUnixNs(),
			Status:         p.GetStatus(),
			LastError:      p.GetLastError(),
		})
	}
	return out, nil
}

// PluginInputRow / PluginCatalogueRow / PluginInstanceRow are the
// dashboard-friendly shapes for the three plugin RPCs. They mirror
// the proto messages 1:1 — the server-side huma handler just copies
// fields across so the wire JSON stays unchanged from the SPA's POV.

type PluginInputRow struct {
	Name     string
	Type     string
	Default  string
	Required bool
	Secret   bool
	Help     string
}

type PluginCatalogueRow struct {
	Name        string
	Version     string
	Kind        string
	Description string
	Inputs      []PluginInputRow
}

type PluginInstanceRow struct {
	Name              string
	InstanceUUID      string
	Project           string
	VMs               []string
	InstalledAtUnixNS int64
	Status            string
}

// ListPluginCatalogue returns the parsed catalogue manifests the agent
// has on disk. Empty slice when the agent isn't configured with a
// catalogue dir.
func (c *Client) ListPluginCatalogue(ctx context.Context) (rows []PluginCatalogueRow, retErr error) {
	defer c.measured("ListPluginCatalogue", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListPluginCatalogue(cctx, &weftv1.ListPluginCatalogueRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]PluginCatalogueRow, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		row := PluginCatalogueRow{
			Name:        e.GetName(),
			Version:     e.GetVersion(),
			Kind:        e.GetKind(),
			Description: e.GetDescription(),
			Inputs:      make([]PluginInputRow, 0, len(e.GetInputs())),
		}
		for _, in := range e.GetInputs() {
			row.Inputs = append(row.Inputs, PluginInputRow{
				Name:     in.GetName(),
				Type:     in.GetType(),
				Default:  in.GetDefault(),
				Required: in.GetRequired(),
				Secret:   in.GetSecret(),
				Help:     in.GetHelp(),
			})
		}
		out = append(out, row)
	}
	return out, nil
}

// ListInstalledPlugins returns the agent's installed-instance registry
// (Manager.List).
func (c *Client) ListInstalledPlugins(ctx context.Context) (rows []PluginInstanceRow, retErr error) {
	defer c.measured("ListInstalledPlugins", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.ListInstalledPlugins(cctx, &weftv1.ListInstalledPluginsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]PluginInstanceRow, 0, len(resp.GetInstances()))
	for _, p := range resp.GetInstances() {
		out = append(out, PluginInstanceRow{
			Name:              p.GetName(),
			InstanceUUID:      p.GetInstanceUuid(),
			Project:           p.GetProject(),
			VMs:               append([]string(nil), p.GetVmUuids()...),
			InstalledAtUnixNS: p.GetInstalledAtUnixNs(),
			Status:            p.GetStatus(),
		})
	}
	return out, nil
}

// InstallPlugin invokes the agent's idempotent install pipeline.
// Returns the freshly-minted (or re-used, on re-install) instance UUID.
func (c *Client) InstallPlugin(ctx context.Context, name, project string, inputs map[string]string) (instanceUUID string, retErr error) {
	defer c.measured("InstallPlugin", &retErr)()
	rpc, err := c.dial()
	if err != nil {
		return "", err
	}
	cctx, cancel := rpcCtx(withBearer(ctx))
	defer cancel()
	resp, err := rpc.InstallPlugin(cctx, &weftv1.InstallPluginRequest{
		Name:    name,
		Project: project,
		Inputs:  inputs,
	})
	if err != nil {
		return "", err
	}
	return resp.GetInstanceUuid(), nil
}
