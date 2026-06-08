// Package etcdsource is the thin etcd v3 client weft-webui uses to
// read the cross-host respawn HA topology surfaced at
// `/weft/coord/hosts/<host_uuid>` (see weft v0.4.1 / respawn V0.1.3).
//
// The package is deliberately tiny : one dialer (Open), one read
// (Hosts), one member-count read (MemberCount), one Close. Anything
// richer (watches, transactions) belongs in a dedicated package — the
// monitors panel only needs a periodic snapshot.
//
// Dialing is best-effort : a missing endpoint set, a TLS failure, or
// a name-resolution miss all surface as a sentinel error the caller
// can degrade on. weft-webui collapses that into "monitors offline"
// rather than 502'ing the API endpoint — the dashboard panel renders
// honestly in detached / preview mode.
package etcdsource

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/openweft/weft-webui/internal/server"
)

// ErrNoEndpoints is returned by Open when the endpoint list is empty.
// Surfaces in main.go as "monitors offline" rather than fatal — an
// operator running weft-webui without etcd (dev mode, preview) keeps
// a usable dashboard.
var ErrNoEndpoints = errors.New("etcdsource: no endpoints configured")

// Options carries the dial parameters. Endpoints is the only
// mandatory field ; everything else has a sane default.
type Options struct {
	// Endpoints lists the etcd cluster members the client dials.
	// Same shape clientv3 expects : "host:port", optionally
	// "scheme://host:port" when TLS is in play. Empty = error.
	Endpoints []string

	// DialTimeout caps the initial dial. Defaults to 5s.
	DialTimeout time.Duration

	// Prefix overrides the etcd key prefix the source reads. Default
	// "/weft/coord/hosts/" (matches weft v0.4.1's HostLiveness
	// publisher). Useful for staging clusters that namespace the
	// prefix.
	Prefix string
}

// Source is the read-only etcd v3 handle weft-webui owns. Embed-
// friendly : pass it to server.SetMonitorsSource() and the api_monitors
// handler picks it up.
type Source struct {
	cli    *clientv3.Client
	prefix string
}

// Open dials the etcd cluster and returns a Source ready for reads.
// The caller owns Close(). A dial failure is returned verbatim so the
// operator sees the real error in the boot log ; main.go treats it
// as non-fatal and logs a warning.
func Open(o Options) (*Source, error) {
	if len(o.Endpoints) == 0 {
		return nil, ErrNoEndpoints
	}
	prefix := o.Prefix
	if prefix == "" {
		prefix = "/weft/coord/hosts/"
	}
	// Trailing slash is load-bearing for the WithPrefix() read below
	// (otherwise a prefix of "/weft/coord/hosts" would also match
	// "/weft/coord/hosts-summary" if that key ever existed).
	if prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	dt := o.DialTimeout
	if dt <= 0 {
		dt = 5 * time.Second
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   o.Endpoints,
		DialTimeout: dt,
	})
	if err != nil {
		return nil, fmt.Errorf("etcdsource: dial: %w", err)
	}
	return &Source{cli: cli, prefix: prefix}, nil
}

// Close releases the underlying etcd client. Safe to call on a nil
// receiver so deferred-Close in main.go doesn't have to nil-check.
func (s *Source) Close() error {
	if s == nil || s.cli == nil {
		return nil
	}
	return s.cli.Close()
}

// Hosts reads every key under the prefix and decodes each value into
// a server.MonitorHost. Results are sorted by hostname for stable
// rendering on the SPA side. A decode error on a single key is
// logged (via the caller's slog) but never aborts the read — a
// half-written key shouldn't blank the whole panel.
func (s *Source) Hosts(ctx context.Context) ([]server.MonitorHost, error) {
	if s == nil || s.cli == nil {
		return nil, ErrNoEndpoints
	}
	resp, err := s.cli.Get(ctx, s.prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("etcdsource: list %s: %w", s.prefix, err)
	}
	out := make([]server.MonitorHost, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		h, err := server.DecodeMonitorHost(kv.Value)
		if err != nil {
			// Skip malformed entries — a half-written lease shouldn't
			// take the whole panel down. The next reconcile cycle
			// from the publishing agent will rewrite the value.
			continue
		}
		// Last-resort fallback : if the JSON missed host_uuid, derive
		// it from the key suffix so the row still renders.
		if h.HostUUID == "" {
			h.HostUUID = path.Base(string(kv.Key))
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		// Hostname first ; UUID as tiebreaker so the order stays
		// deterministic across reads.
		if out[i].Hostname != out[j].Hostname {
			return out[i].Hostname < out[j].Hostname
		}
		return out[i].HostUUID < out[j].HostUUID
	})
	return out, nil
}

// MemberCount returns the size of the etcd cluster (one entry per
// running etcd peer). Used as the default expected_count for
// /api/monitors when the operator hasn't pinned a static value.
//
// Note this is the etcd member count, not the weft-agent count.
// They coincide in the canonical 3-DC topology (one etcd member per
// DC, one weft-agent per host with one host per DC) ; an operator
// running a 5-member etcd cluster with 3 weft-agents should pin
// WEFT_WEBUI_EXPECTED_MONITORS=3 to get the right baseline.
func (s *Source) MemberCount(ctx context.Context) (int, error) {
	if s == nil || s.cli == nil {
		return 0, ErrNoEndpoints
	}
	resp, err := s.cli.MemberList(ctx)
	if err != nil {
		return 0, fmt.Errorf("etcdsource: member list: %w", err)
	}
	return len(resp.Members), nil
}
