// firewallStatus.ts — singleton in-memory map of vmUUID →
// FirewallStatus, fed by the platform-event stream (singleton
// EventSource in events.ts) on every "firewall.status" event.
//
// Why no fetch / no API call ? Because the in-VM agent publishes
// its full state every 10 s on a NATS subject the host re-emits as
// a synthetic platform event. So the SSE pipe carries the live data
// already — components just have to read this derived store.
//
// A freshly-opened tab sees nothing until the next agent tick
// (≤ 10 s) ; that's the same latency / replay model the rest of
// the SSE-driven panels use.

import { derived, type Readable } from 'svelte/store';
import { eventFeed, type PlatformEvent } from './events';

export interface FirewallStatus {
  /** "Healthy" when the in-VM reconciler last read its nftables
   *  table OK, "Degraded" when it errored. Pass-through from
   *  pod.FirewallStatus.Overall. */
  overall: string;
  /** True when the "weft-fw" nftables table is currently
   *  installed in the guest kernel. False = no policy yet
   *  (boot before first publish) or it was flushed externally. */
  tableInstalled: boolean;
  /** Total rule count across input + output chains, including the
   *  unconditional ct/lo defaults the reconciler always installs
   *  (the badge component subtracts those when rendering). */
  rulesInstalled: number;
  /** Last read failure message from the reconciler ; empty when
   *  Overall == "Healthy". */
  lastError: string;
  /** Wall-clock the agent stamped the publish (UTC unix seconds).
   *  The badge uses it to fade to grey when stale (no publish in
   *  > 3 ticks). */
  publishedAtUnix: number;
}

/** Defaults the reconciler installs unconditionally :
 *  `ct state established,related accept` + `iifname "lo" accept`.
 *  See network.ApplyFirewall in weft-microvm-init/pkg/network. */
export const FIREWALL_DEFAULT_RULE_COUNT = 2;

/** Derived store : vmUUID → latest FirewallStatus seen on the
 *  event bus. Keys appear as agents emit their first status ;
 *  they're never removed (a deleted VM stops emitting, the entry
 *  just goes stale — the badge handles that visually). */
export const firewallStatusByVM: Readable<Record<string, FirewallStatus>> = derived(
  eventFeed,
  ($feed: PlatformEvent[]) => {
    const acc: Record<string, FirewallStatus> = {};
    // Walk the buffer oldest → newest so newer publishes overwrite
    // older ones for the same VM. eventFeed prepends new entries,
    // so we reverse for chronological order.
    for (let i = $feed.length - 1; i >= 0; i--) {
      const ev = $feed[i];
      if (ev.kind !== 'firewall.status' || !ev.subject) continue;
      acc[ev.subject] = parseStatus(ev.meta ?? {});
    }
    return acc;
  },
  {},
);

function parseStatus(meta: Record<string, string>): FirewallStatus {
  return {
    overall: meta.Overall ?? 'Unknown',
    tableInstalled: meta.TableInstalled === 'true',
    rulesInstalled: numOrZero(meta.RulesInstalled),
    lastError: meta.LastError ?? '',
    publishedAtUnix: numOrZero(meta.PublishedAtUnix),
  };
}

function numOrZero(s: string | undefined): number {
  if (!s) return 0;
  const n = parseInt(s, 10);
  return Number.isFinite(n) ? n : 0;
}

/** isStale : true when the last publish is older than `staleAfterSec`
 *  seconds. Default 35 s = three 10-s ticks plus a half-tick buffer
 *  so a single missed publish doesn't flip the badge. */
export function isStale(s: FirewallStatus | undefined, nowUnix: number, staleAfterSec = 35): boolean {
  if (!s || s.publishedAtUnix === 0) return true;
  return nowUnix - s.publishedAtUnix > staleAfterSec;
}
