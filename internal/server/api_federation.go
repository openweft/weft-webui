// api_federation.go — federation-lite peer listing for the dashboard.
//
//   GET /api/federation/peers   — surfaces the same rows the operator
//                                 sees from `weft federation list`.
//
// Federation transport in weft is HTTP-pull (see weft/federation :
// each peer's /cluster-info endpoint is polled on a 30 s cadence and
// classified live/stale/unreachable from LastSeen + LastError). There
// is intentionally NO gRPC RPC for this in weft-proto ; the per-peer
// state lives in the agent's in-memory poller cache (federation.Poller).
//
// Wiring story :
//   - Live  : the webui would query an agent-side endpoint that
//             exposes the Poller.Snapshot() table. The agent-side
//             surface doesn't exist yet (no gRPC RPC ; the only HTTP
//             route the federation package owns is /cluster-info on a
//             peer's own server). Tracking gap : a future weft-proto
//             AgentInfo / Federation.ListPeers RPC.
//   - Mock  : this file returns canned data so the SPA's Federation
//             page lights up against the in-memory store. The shape
//             mirrors federation.PeerState so swapping to a live RPC
//             is a one-method change in the helper below.
package server

import (
	"context"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// FederationPeer is the dashboard-facing row. Shape kept close to
// federation.PeerState in the weft repo (Name + URL + LastSeen +
// Status) plus Region + Weight pulled from the peer's most-recently
// observed manifest entry. LastSeenUnixNS is nanoseconds-since-epoch
// so the SPA can format relative durations without tripping over
// timezone or RFC-3339 parsing.
type FederationPeer struct {
	Name           string `json:"name"               doc:"Peer cluster name (manifest-derived once seen, else the operator-supplied label)"`
	URL            string `json:"url"                doc:"Peer /cluster-info URL — the poller targets this on every tick"`
	Region         string `json:"region"             doc:"Free-form locality from the peer's manifest entry (e.g. 'eu-west-3') ; empty if never seen"`
	Weight         int    `json:"weight"             doc:"Placement bias from the peer's manifest entry ; 0 means default (100)"`
	LastSeenUnixNS int64  `json:"last_seen_unix_ns"  doc:"Nanoseconds since epoch of the last successful poll, 0 before the first success"`
	Status         string `json:"status"             doc:"'live' | 'stale' | 'unreachable' — derived from LastSeen + LastError + StaleTTL on each poll" enum:"live,stale,unreachable"`
	LastError      string `json:"last_error,omitempty" doc:"Most recent poll error, empty when healthy"`
}

func mountFederationAPI(api huma.API, _ Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "list-federation-peers",
		Method:      "GET",
		Path:        "/api/federation/peers",
		Summary:     "List federation peers with last-seen + status",
		Description: "Surfaces the same rows the operator sees from `weft federation list`. Status is one of 'live' (recent successful poll), 'stale' (last poll older than the stale TTL, default 5 minutes) or 'unreachable' (no successful poll on record or current poll failed).",
		Tags:        []string{"federation"},
	}, func(_ context.Context, _ *struct{}) (*listFederationPeersOutput, error) {
		out := &listFederationPeersOutput{}
		out.Body = listFederationPeers()
		return out, nil
	})
}

type listFederationPeersOutput struct {
	Body []FederationPeer
}

// ---- mock peer store ----------------------------------------------
//
// Canned three-cluster federation : one live, one stale, one
// unreachable so the SPA exercises every badge color. Status is
// re-derived on each read so an operator who tweaks the times in
// peer-mutation tooling sees a consistent table.

var (
	federationMu    sync.Mutex
	federationSeed  = seedFederationPeers()
	federationStale = 5 * time.Minute // matches federation.DefaultPeerStaleTTL
)

func seedFederationPeers() []FederationPeer {
	now := time.Now().UTC()
	return []FederationPeer{
		{
			Name:           "acme-eu",
			URL:            "https://weft-eu.acme.example/cluster-info",
			Region:         "eu-west-3",
			Weight:         100,
			LastSeenUnixNS: now.Add(-25 * time.Second).UnixNano(),
			Status:         "live",
		},
		{
			Name:           "acme-us",
			URL:            "https://weft-us.acme.example/cluster-info",
			Region:         "us-east-1",
			Weight:         200,
			LastSeenUnixNS: now.Add(-9 * time.Minute).UnixNano(),
			Status:         "stale",
			LastError:      "http 503",
		},
		{
			Name:           "acme-ap",
			URL:            "https://weft-ap.acme.example/cluster-info",
			Region:         "ap-southeast-1",
			Weight:         50,
			LastSeenUnixNS: 0,
			Status:         "unreachable",
			LastError:      "dial tcp: connection refused",
		},
	}
}

func listFederationPeers() []FederationPeer {
	federationMu.Lock()
	defer federationMu.Unlock()
	now := time.Now()
	out := make([]FederationPeer, 0, len(federationSeed))
	for _, p := range federationSeed {
		cp := p
		cp.Status = classifyFederationStatus(cp.LastSeenUnixNS, cp.LastError, now, federationStale)
		out = append(out, cp)
	}
	return out
}

// classifyFederationStatus mirrors federation.classifyStatus in the
// weft repo : zero LastSeen → unreachable ; LastSeen older than
// staleTTL → stale ; else live. The error string is informational —
// a healthy poll clears it, a fresh failure surfaces it without
// re-bucketing (Status stays 'live' between two consecutive failures
// inside staleTTL, matching the poller's hysteresis).
func classifyFederationStatus(lastSeenUnixNS int64, _ string, now time.Time, staleTTL time.Duration) string {
	if lastSeenUnixNS == 0 {
		return "unreachable"
	}
	last := time.Unix(0, lastSeenUnixNS)
	if staleTTL > 0 && now.Sub(last) > staleTTL {
		return "stale"
	}
	return "live"
}
