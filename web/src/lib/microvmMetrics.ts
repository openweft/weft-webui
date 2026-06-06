// microvmMetrics.ts — polling-driven ring buffer of per-VM metric
// snapshots. Components subscribe to a derived store keyed by VM
// name ; the polling loop is started/stopped by the drawer when the
// Metrics tab is mounted/unmounted.
//
// Why polling and not SSE : the platform event bus already carries
// derived per-VM signals (firewall status, lifecycle events), and a
// dedicated SSE stream for fine-grained metrics would burn an open
// connection per VM. A single ~5 s poll keeps the path narrow and
// matches what a typical Prometheus dashboard does.
//
// Ring buffer : 90 samples × 5 s = 7.5 minutes of history. The chart
// renders the full ring on every update ; trimming old entries
// happens on push so the in-memory footprint stays bounded even for
// long-lived drawer sessions.

import { writable, derived, type Readable } from 'svelte/store';
import { getMicroVMMetrics, type MetricsSnapshot } from './api';

/** Maximum number of samples kept per VM. At the default 5-second
 *  poll cadence this is 7.5 minutes of history — long enough to spot
 *  a CPU spike, short enough to fit a 600-pixel-wide canvas without
 *  resampling. */
export const METRICS_RING_CAPACITY = 90;

/** Default poll cadence (ms). Five seconds is a fair compromise
 *  between responsiveness and load on the server's synth path. */
export const METRICS_POLL_MS = 5_000;

/** Per-VM ring buffers. Components read this map ; the polling
 *  loop is the only writer. */
export const metricsByVM = writable<Record<string, MetricsSnapshot[]>>({});

/** pushSample appends one sample to a VM's ring, trimming the
 *  oldest entry once capacity is reached. Exported so the unit
 *  tests can drive the store without touching the polling loop. */
export function pushSample(name: string, snap: MetricsSnapshot): void {
  metricsByVM.update((m) => {
    const prev = m[name] ?? [];
    const next = prev.length >= METRICS_RING_CAPACITY
      ? [...prev.slice(1), snap]
      : [...prev, snap];
    return { ...m, [name]: next };
  });
}

/** clearVM drops the ring for one VM. Called from the drawer's
 *  onDestroy so closing+re-opening a drawer starts from a fresh
 *  series. */
export function clearVM(name: string): void {
  metricsByVM.update((m) => {
    if (!(name in m)) return m;
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { [name]: _, ...rest } = m;
    return rest;
  });
}

/** seriesForVM is the typed accessor a chart component subscribes
 *  to. Empty array when no samples have arrived yet — the chart
 *  treats that as "loading". */
export function seriesForVM(name: string): Readable<MetricsSnapshot[]> {
  return derived(metricsByVM, ($m) => $m[name] ?? []);
}

/** startMetricsPolling kicks off a setInterval loop that fetches
 *  one snapshot every cadenceMs and appends it to the VM's ring.
 *  Returns a stop function the caller invokes from onDestroy.
 *
 *  The first sample is requested immediately so the chart isn't
 *  blank for the first cadence ; subsequent failures are silently
 *  swallowed (the previous tail stays visible). One transient
 *  network blip shouldn't blow up the panel. */
export function startMetricsPolling(
  name: string,
  cadenceMs = METRICS_POLL_MS,
): () => void {
  let stopped = false;

  const tick = async () => {
    if (stopped) return;
    try {
      const snap = await getMicroVMMetrics(name);
      if (!stopped) pushSample(name, snap);
    } catch {
      // Swallow : keep the existing tail visible rather than wiping
      // the chart on every transient error.
    }
  };

  // Immediate first hit so the UI isn't empty for cadenceMs.
  void tick();
  const handle = setInterval(tick, cadenceMs);

  return () => {
    stopped = true;
    clearInterval(handle);
  };
}
