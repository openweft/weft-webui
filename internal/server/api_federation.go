// api_federation.go — federation-lite peer listing for the dashboard.
//
//   GET /api/federation/peers   — surfaces the same rows the operator
//                                 sees from `weft federation list`.
//
// Federation transport in weft is HTTP-pull (see weft/federation :
// each peer's /cluster-info endpoint is polled on a 30 s cadence and
// classified live/stale/unreachable from LastSeen + LastError). The
// agent's in-process `federation.Poller` holds the snapshot ; this
// handler reads it through the gRPC `ListFederationPeers` RPC added in
// weft-proto v0.5.0. Per [[openweft_pull_model]] the call reads the
// locally-cached pull state — no remote pull happens on the hot path.
//
// Live wiring : when `live` is non-nil and the agent answers, the rows
// come straight from the poller snapshot. When the agent returns
// Unimplemented (older weft binary or feature gate off), the handler
// falls back to a small canned set so the SPA stays explorable in
// dev / preview environments.
package server

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/wclient"
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
	}, func(ctx context.Context, _ *struct{}) (*listFederationPeersOutput, error) {
		out := &listFederationPeersOutput{}
		out.Body = listFederationPeers(ctx)
		return out, nil
	})
}

type listFederationPeersOutput struct {
	Body []FederationPeer
}

// listFederationPeers asks the agent for the current poller snapshot.
// Falls back to a small canned set if the daemon isn't wired (dev
// preview) or returns Unimplemented (older binary).
func listFederationPeers(ctx context.Context) []FederationPeer {
	if live != nil {
		rows, err := live.ListFederationPeers(ctx)
		if err == nil {
			return mapFederationRows(rows)
		}
		// On any non-Unimplemented error we still degrade to the
		// canned set rather than 500 — the dashboard panel should
		// stay usable even when the federation surface is mid-roll.
	}
	return canonicalFederationFallback()
}

func mapFederationRows(in []wclient.FederationPeerRow) []FederationPeer {
	out := make([]FederationPeer, 0, len(in))
	for _, r := range in {
		out = append(out, FederationPeer{
			Name:           r.Name,
			URL:            r.URL,
			Region:         r.Region,
			Weight:         r.Weight,
			LastSeenUnixNS: r.LastSeenUnixNS,
			Status:         r.Status,
			LastError:      r.LastError,
		})
	}
	return out
}

// canonicalFederationFallback returns an empty slice — the agent is
// the source of truth and the SPA renders "no federation configured"
// when the response is empty. Kept as a function so future preview
// modes can plug in a richer fixture without touching the handler.
func canonicalFederationFallback() []FederationPeer { return nil }

// classifyFederationStatus mirrors federation.classifyStatus in the
// weft repo : zero LastSeen → unreachable ; LastSeen older than
// staleTTL → stale ; else live. Kept for callers that build a
// FederationPeer without going through the wclient adapter — the
// agent does the classification server-side, so this is only used in
// tests / synthetic fixtures.
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
