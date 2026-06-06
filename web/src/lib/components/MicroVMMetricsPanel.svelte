<script lang="ts">
  // Metrics tab body — fan-outs the per-VM ring buffer into a
  // stack of TimeSeriesChart instances (CPU, memory, network, disk).
  // The polling loop is started on mount and stopped on destroy
  // through startMetricsPolling ; the ring is intentionally kept
  // beyond unmount so re-opening the drawer for the same VM keeps
  // the history visible. clearVM is only called when the operator
  // explicitly hits "Reset".

  import { onMount, onDestroy } from 'svelte';
  import { get } from 'svelte/store';
  import TimeSeriesChart from './TimeSeriesChart.svelte';
  import {
    seriesForVM, startMetricsPolling, clearVM,
    METRICS_POLL_MS,
  } from '../microvmMetrics';
  import type { MetricsSnapshot } from '../api';

  let { name }: { name: string } = $props();

  // Per-VM derived store — re-derived when `name` changes (drawer
  // re-mount with a different row).
  let series = $derived(seriesForVM(name));
  let samples = $state<MetricsSnapshot[]>([]);
  let stopPoll: (() => void) | null = null;

  // Bind the derived store to a local $state slot so the template
  // re-renders on every push. Svelte 5 doesn't auto-subscribe to a
  // value-returning derived ; we manually subscribe + unsubscribe.
  $effect(() => {
    const unsub = series.subscribe((v) => {
      samples = v;
    });
    return unsub;
  });

  onMount(() => {
    stopPoll = startMetricsPolling(name, METRICS_POLL_MS);
  });
  onDestroy(() => {
    stopPoll?.();
  });

  // Time-series projection helpers. Each chart receives an array of
  // numbers ; the most recent value drives the "current" badge in
  // the chart legend.
  let cpu       = $derived(samples.map((s) => s.cpu_percent));
  let memUsed   = $derived(samples.map((s) => s.mem_used_mib));
  let memPct    = $derived(samples.map((s) => s.mem_total_mib > 0 ? (s.mem_used_mib / s.mem_total_mib) * 100 : 0));
  let netRx     = $derived(samples.map((s) => s.net_rx_bps));
  let netTx     = $derived(samples.map((s) => s.net_tx_bps));
  let diskRead  = $derived(samples.map((s) => s.disk_read_bps));
  let diskWrite = $derived(samples.map((s) => s.disk_write_bps));

  let last = $derived(samples.length > 0 ? samples[samples.length - 1] : undefined);
  let isMock = $derived(last?.mock === true);

  function fmtBytesPerSec(n: number): string {
    if (n < 1024) return `${n.toFixed(0)} B/s`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB/s`;
    if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(2)} MiB/s`;
    return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GiB/s`;
  }
  function fmtUptime(sec: number): string {
    const d = Math.floor(sec / 86400);
    const h = Math.floor((sec % 86400) / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = Math.floor(sec % 60);
    if (d > 0) return `${d}d ${h}h ${m}m`;
    if (h > 0) return `${h}h ${m}m ${s}s`;
    return `${m}m ${s}s`;
  }

  function resetRing() {
    if (!confirm('Clear local metric history for this VM ? Polling continues from the next tick.')) return;
    clearVM(name);
    // Re-bind so the local $state reflects the cleared store immediately.
    samples = get(series);
  }
</script>

<div class="space-y-3">
  <div class="flex items-baseline gap-2">
    <h3 class="text-sm font-semibold">Live metrics</h3>
    {#if isMock}
      <span class="badge badge-xs badge-warning" title="No GetMicroVMMetrics RPC defined yet ; the curves are synthetic.">mock data</span>
    {/if}
    <span class="text-xs text-base-content/50">
      polling every {Math.round(METRICS_POLL_MS / 1000)} s · {samples.length} sample{samples.length === 1 ? '' : 's'} held
    </span>
    <button class="ml-auto btn btn-xs btn-ghost" onclick={resetRing} title="Drop the in-memory ring">Reset</button>
  </div>

  {#if samples.length === 0}
    <div class="py-8 text-center">
      <span class="loading loading-spinner loading-md"></span>
      <p class="text-xs text-base-content/50 mt-2">waiting for the first sample…</p>
    </div>
  {:else}
    <!-- Inline summary chips : at-a-glance current values. -->
    <dl class="grid grid-cols-[8rem_1fr] gap-y-1 text-sm">
      {#if last}
        <dt class="text-base-content/60">uptime</dt>
        <dd class="tabular-nums">{fmtUptime(last.uptime_seconds)}</dd>
        <dt class="text-base-content/60">memory</dt>
        <dd class="tabular-nums">{last.mem_used_mib} / {last.mem_total_mib} MiB</dd>
      {/if}
    </dl>

    <TimeSeriesChart
      yLabel="CPU"
      unit="%"
      yMin={0}
      yMax={100}
      formatY={(v) => v.toFixed(1)}
      series={[
        { name: 'cpu', values: cpu, color: '#3abff8' },
      ]}
    />

    <TimeSeriesChart
      yLabel="Memory"
      unit="%"
      yMin={0}
      yMax={100}
      formatY={(v) => v.toFixed(1)}
      series={[
        { name: 'mem%', values: memPct, color: '#36d399' },
      ]}
    />

    <TimeSeriesChart
      yLabel="Network"
      formatY={fmtBytesPerSec}
      series={[
        { name: 'rx', values: netRx, color: '#3abff8' },
        { name: 'tx', values: netTx, color: '#fbbd23' },
      ]}
    />

    <TimeSeriesChart
      yLabel="Disk"
      formatY={fmtBytesPerSec}
      series={[
        { name: 'read',  values: diskRead,  color: '#36d399' },
        { name: 'write', values: diskWrite, color: '#f87272' },
      ]}
    />

    <!-- memUsed kept around as a hidden source for the "Memory MiB"
         absolute chart if we want to surface it later ; for now the
         percent view is enough not to clutter the panel. -->
    {#if false}<TimeSeriesChart series={[{ name: 'mem MiB', values: memUsed }]} />{/if}
  {/if}
</div>
