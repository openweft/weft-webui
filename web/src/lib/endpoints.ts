// endpoints.ts — multi-DC HA failover for the dashboard's API traffic.
//
// The same Svelte bundle runs in two very different hosts :
//
//   1. A plain browser tab served by one DC's weft-webui. There is
//      exactly one origin and no failover to do — `baseUrl` stays ''
//      (origin-relative), every store below sits in its idle state,
//      and this module is effectively inert. Today's behaviour, byte
//      for byte.
//
//   2. A native client shell (weft-app-osx / -gtk / -windows / mobile)
//      that wraps the dashboard in a WebView and reaches the cluster
//      over an authenticated transport (WireGuard mesh or an SSH
//      local-forward — see weft-app-core). The shell knows about every
//      DC's weft-webui and hands us that knowledge two ways :
//
//        * window.__WEFT_ENDPOINTS__  — a static list injected before
//          the bundle loads. With >1 entry the API client rotates
//          across origins itself (multi-origin failover).
//
//        * window.__weftFailoverNotice(from,to) — a callback the shell
//          invokes when it transparently re-points a *single* stable
//          loopback origin at a different DC (single-origin
//          native-swap). The origin never changes, so cookies / OIDC
//          session / in-memory SPA state all survive ; we only raise
//          the banner so the user knows a switch happened.
//
// Both layers cooperate : same-origin retry (below) smooths the brief
// gap while the shell swaps a transport, and multi-origin rotation
// covers the case where the shell prefers to expose all DCs at once.

import { writable, type Writable } from 'svelte/store';

export interface WeftEndpoint {
  /** Human label for the DC, e.g. "DC-A". Shown in the banner. */
  name: string;
  /** Origin to send API calls to, e.g. "https://10.80.0.11:8443" or
   *  "http://127.0.0.1:8645" (an SSH local-forward). No trailing slash. */
  url: string;
}

interface InjectedConfig {
  endpoints?: WeftEndpoint[];
  /** Hold-down before a recovered DC may be re-selected (anti-flap). */
  quarantineMs?: number;
  /** Initial active DC name. In single-gateway mode (one loopback origin,
   *  the shell does failover behind it) endpoints[] only carries the
   *  loopback ; this field lets the shell push the real active DC name
   *  for the persistent Topbar chip. Updated by __weftFailoverNotice. */
  currentDC?: string;
}

declare global {
  interface Window {
    __WEFT_ENDPOINTS__?: InjectedConfig;
    /** Called by the native shell after a single-origin transport swap. */
    __weftFailoverNotice?: (from: string, to: string) => void;
    /** Lets the native shell replace the endpoint list at runtime
     *  (e.g. after re-resolving SRV records). */
    __weftSetEndpoints?: (cfg: InjectedConfig) => void;
  }
}

// ---- configuration ----------------------------------------------

const cfg: InjectedConfig =
  (typeof window !== 'undefined' && window.__WEFT_ENDPOINTS__) || {};

let endpoints: WeftEndpoint[] = cfg.endpoints ?? [];
const QUARANTINE_MS = cfg.quarantineMs ?? 15_000;

/** True only when a native shell injected ≥1 endpoint. In a plain
 *  browser this stays false and the module is inert. Exported as a
 *  `let` so it tracks a runtime endpoint swap (live binding). */
export let endpointsEnabled = endpoints.length > 0;

let activeIdx = 0;
// url -> epoch-ms until which the endpoint is quarantined after a
// failure (hysteresis : keeps a flapping DC from being re-promoted
// the instant it answers one probe).
const quarantineUntil = new Map<string, number>();

// ---- reactive state for the banner ------------------------------

export interface FailoverState {
  /** A switch happened and the user hasn't dismissed the notice. */
  switched: boolean;
  fromName?: string;
  toName?: string;
  /** Every known DC is currently unreachable. */
  allDown: boolean;
}

export const failover: Writable<FailoverState> = writable({
  switched: false,
  allDown: false,
});

export function dismissFailover() {
  failover.update((s) => ({ ...s, switched: false }));
}

// ---- persistent DC indicator -----------------------------------
// Reactive store the Topbar subscribes to so the chip can render the
// currently-active DC at all times — independent of the failover
// banner, which is transient. Initialized from injected currentDC or
// the first endpoint's name ; updated by __weftFailoverNotice.
export const currentDC: Writable<string> = writable(
  cfg.currentDC ?? (endpoints[0]?.name ?? ''),
);

// ---- selection ---------------------------------------------------

/** The origin to prefix onto API paths. '' in browser mode (origin-
 *  relative, exactly as before). */
export function activeBase(): string {
  return endpointsEnabled ? endpoints[activeIdx].url : '';
}

function activeName(): string {
  return endpointsEnabled ? endpoints[activeIdx].name : '';
}

/** Pick the highest-priority (lowest-index) endpoint that isn't
 *  quarantined. Returns the chosen index, or -1 if all are down. The
 *  `now` arg is injected for testability. */
function pickHealthy(now: number): number {
  for (let i = 0; i < endpoints.length; i++) {
    const until = quarantineUntil.get(endpoints[i].url) ?? 0;
    if (until <= now) return i;
  }
  return -1;
}

/** Mark the active endpoint failed and move to the next healthy one.
 *  Returns true if a *different* endpoint was selected. */
export function rotate(now: number = Date.now()): boolean {
  if (!endpointsEnabled) return false;
  const fromIdx = activeIdx;
  const from = endpoints[fromIdx];
  quarantineUntil.set(from.url, now + QUARANTINE_MS);

  const next = pickHealthy(now);
  if (next === -1) {
    // Nothing healthy. Keep pointing where we were (the SPA will keep
    // retrying same-origin in case the shell swaps under us) and tell
    // the user everything is down.
    failover.update((s) => ({ ...s, allDown: true }));
    return false;
  }
  if (next === fromIdx) return false;

  activeIdx = next;
  failover.set({
    switched: true,
    fromName: from.name,
    toName: endpoints[next].name,
    allDown: false,
  });
  return true;
}

/** A request succeeded against `base`. Clears the all-down flag and,
 *  if `base` is the current active endpoint, lets the banner relax. */
export function noteSuccess(base: string, now: number = Date.now()) {
  if (!endpointsEnabled) return;
  failover.update((s) => (s.allDown ? { ...s, allDown: false } : s));
  // A success means the endpoint is live — let it out of quarantine
  // early so we don't keep avoiding a DC that has clearly recovered.
  if (quarantineUntil.has(base)) quarantineUntil.delete(base);
}

// ---- native-shell driven notices --------------------------------

if (typeof window !== 'undefined') {
  // The shell re-pointed a single stable origin at a different DC.
  window.__weftFailoverNotice = (from: string, to: string) => {
    failover.set({ switched: true, fromName: from, toName: to, allDown: false });
    // The persistent Topbar chip tracks `to` separately from the
    // transient banner so it stays accurate after the user dismisses.
    currentDC.set(to);
  };
  // The shell wants to swap the endpoint list (e.g. after re-resolving
  // DNS). Reset selection to the new highest priority.
  window.__weftSetEndpoints = (next: InjectedConfig) => {
    endpoints = next.endpoints ?? [];
    endpointsEnabled = endpoints.length > 0;
    activeIdx = 0;
    quarantineUntil.clear();
    if (next.currentDC) currentDC.set(next.currentDC);
    else if (endpoints.length > 0) currentDC.set(endpoints[0].name);
  };
}

// ---- URL helper for non-fetch consumers (EventSource) ------------

/** Join the active base with an origin-relative API path. In browser
 *  mode returns the path unchanged. */
export function withBase(path: string): string {
  const base = activeBase();
  if (!base) return path;
  return base.replace(/\/$/, '') + (path.startsWith('/') ? path : `/${path}`);
}

export function currentEndpointName(): string {
  return activeName();
}

/** Number of known DC endpoints (0 in browser mode). */
export function endpointCount(): number {
  return endpoints.length;
}
