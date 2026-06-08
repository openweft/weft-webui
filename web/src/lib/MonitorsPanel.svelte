<script lang="ts">
  // MonitorsPanel — operator-facing view of the cross-host respawn HA
  // topology landed in weft v0.4.1 (respawn V0.1.3).
  //
  // One weft-agent monitor per host ; each holds an etcd lease at
  // /weft/coord/hosts/<host_uuid>. The set of live leases is the set
  // of healthy + reachable weft agents — a drop is the canonical
  // signal of a DC partition or rack outage.
  //
  // Polling is 5s and lives entirely client-side ; the server's
  // /api/monitors handler re-reads etcd on every call (3s ceiling).
  // Badge color is a function of count vs expected_count :
  //
  //   count == expected_count                 → badge-success
  //   ceil(expected/2) <= count < expected    → badge-warning (degraded
  //                                              but etcd quorum still ok)
  //   count < ceil(expected/2)                → badge-error (quorum lost)
  //
  // expected_count == 0 collapses everything to a neutral "baseline
  // unknown" state so an unconfigured cluster doesn't render a
  // misleading "0 of 0 healthy".

  import { onMount, onDestroy } from 'svelte';
  import { listMonitors, type MonitorsSnapshot, type MonitorHost } from './api';

  const POLL_MS = 5_000;

  let snapshot = $state<MonitorsSnapshot>({ monitors: [], count: 0, expected_count: 0 });
  let loading = $state(true);
  let err = $state('');
  let timer: ReturnType<typeof setInterval> | null = null;

  async function refresh() {
    try {
      snapshot = await listMonitors();
      err = '';
    } catch (e) {
      err = String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void refresh();
    timer = setInterval(refresh, POLL_MS);
  });
  onDestroy(() => {
    if (timer !== null) clearInterval(timer);
  });

  // Quorum-aware badge state. expected_count == 0 collapses everything
  // to a neutral surface so an unconfigured cluster doesn't render
  // misleading colors.
  let badge = $derived.by(() => {
    const { count, expected_count } = snapshot;
    if (expected_count <= 0) {
      return { cls: 'badge-ghost', label: 'baseline unknown' };
    }
    if (count >= expected_count) {
      return { cls: 'badge-success', label: 'all monitors up' };
    }
    const quorum = Math.ceil(expected_count / 2);
    if (count >= quorum) {
      return { cls: 'badge-warning', label: 'degraded' };
    }
    return { cls: 'badge-error', label: 'quorum lost' };
  });

  // Render the monitor list ordered by hostname (server already sorts,
  // but a defensive re-sort keeps the UI stable if the wire shape ever
  // drifts).
  let monitors = $derived<MonitorHost[]>(
    [...snapshot.monitors].sort((a, b) => a.hostname.localeCompare(b.hostname)),
  );

  // Relative uptime : "running for 2h 15m". Coarse on purpose —
  // exact-seconds drift isn't operationally useful here.
  function uptime(startedAt: string): string {
    if (!startedAt) return '—';
    const ms = Date.parse(startedAt);
    if (Number.isNaN(ms)) return '—';
    let sec = Math.max(0, Math.floor((Date.now() - ms) / 1000));
    const d = Math.floor(sec / 86400); sec -= d * 86400;
    const h = Math.floor(sec / 3600);  sec -= h * 3600;
    const m = Math.floor(sec / 60);
    if (d > 0) return `running for ${d}d ${h}h`;
    if (h > 0) return `running for ${h}h ${m}m`;
    if (m > 0) return `running for ${m}m`;
    return `running for ${sec}s`;
  }
</script>

<section class="rounded-box border border-base-300 bg-base-100 p-4">
  <header class="flex items-baseline gap-3">
    <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/70">
      Live monitors
    </h3>
    <span class="badge {badge.cls}">
      {snapshot.count}{snapshot.expected_count > 0 ? ` of ${snapshot.expected_count}` : ''}
      &nbsp;·&nbsp;{badge.label}
    </span>
    <span class="ml-auto text-xs text-base-content/50">
      cross-host respawn HA · polling every {Math.round(POLL_MS / 1000)}s
    </span>
  </header>

  {#if loading && snapshot.monitors.length === 0}
    <div class="mt-3 flex items-center gap-2 text-sm text-base-content/60">
      <span class="loading loading-spinner loading-xs"></span>
      reading <span class="font-mono">/weft/coord/hosts/</span>…
    </div>
  {:else if err}
    <div class="alert alert-warning mt-3 text-sm">monitors API : {err}</div>
  {:else if monitors.length === 0}
    <p class="mt-3 text-sm text-base-content/60">
      No live monitors visible. Either the etcd source is unconfigured
      (set <span class="font-mono">WEFT_ETCD_ENDPOINTS</span>) or every
      weft-agent lease has expired — check the cluster bring-up logs.
    </p>
  {:else}
    <ul class="mt-3 divide-y divide-base-300">
      {#each monitors as m (m.host_uuid)}
        <li class="flex items-center gap-3 py-2 text-sm">
          <span class="font-mono font-medium">{m.hostname || m.host_uuid}</span>
          {#if m.hypervisor}
            <span class="badge badge-ghost badge-sm">{m.hypervisor}</span>
          {/if}
          {#if m.version}
            <span class="badge badge-outline badge-sm">{m.version}</span>
          {/if}
          <span class="ml-auto tabular-nums text-base-content/60">{uptime(m.started_at)}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>
