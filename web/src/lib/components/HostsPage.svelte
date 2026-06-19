<script lang="ts">
  // HostsPage — cluster-admin view of the host inventory.
  //
  // Layout : table top-down, with a filter strip (search + AZ dropdown)
  // and per-row action buttons (Cordon / Uncordon / Remove). Click a row
  // to open the HostDrawer with the full detail and the same lifecycle
  // actions. Matches the master-detail UX shipped in NetworksPage /
  // LoadBalancersPage : single list, single typed page rather than a
  // generic ResourcePage fallback.
  //
  // Refresh strategy : initial fetch on mount + auto-refresh every 5 s
  // + reactive refresh when the global events stream emits a `host.*`
  // event. Three layers because the events stream is admin-only on the
  // user listener and the auto-refresh keeps the table fresh when the
  // SSE is offline (failover banner up, etc.).
  import { onDestroy } from 'svelte';
  import { lastEvents } from '../events';
  import {
    listHosts, cordonHost, uncordonHost, removeHost, getMe,
    type HostRow, type Me, type ResourceMeta,
  } from '../api';
  import HostDrawer from './HostDrawer.svelte';

  // meta carries the resource catalogue entry — label + columns. We
  // ignore the columns (the typed table below renders its own) but
  // surface the label in the header for parity with the other pages.
  let { meta }: { meta: ResourceMeta } = $props();

  let hosts = $state<HostRow[]>([]);
  let loading = $state(true);
  let err = $state('');
  let actionBusy = $state(false);
  let actionErr = $state('');

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && me.cluster_admin);
  $effect(() => { getMe().then((u) => (me = u)).catch(() => {/* api.ts handled */}); });

  let query = $state('');
  let azFilter = $state('');

  // selectedUUID drives the drawer ; empty = drawer closed.
  let selectedUUID = $state('');
  let selected = $derived<HostRow | null>(
    hosts.find((h) => h.uuid === selectedUUID) ?? null,
  );

  async function refresh() {
    try {
      hosts = await listHosts();
      err = '';
    } catch (e) {
      err = String(e);
    } finally {
      loading = false;
    }
  }

  // Initial load + 5 s poll. The poll covers the case where the SSE
  // stream is down (failover, admin listener vs user listener) ; on
  // a healthy admin session the SSE subscription below preempts it.
  $effect(() => {
    void refresh();
    const t = setInterval(refresh, 5_000);
    return () => clearInterval(t);
  });

  // Reactive : every time the global event feed lands a `host.*` event
  // we trigger a re-fetch. The events module already maps host.* to
  // resource id "hosts" — we just have to react.
  let lastSeenTs = $state('');
  $effect(() => {
    const evs = $lastEvents;
    if (!evs.length) return;
    const hostEv = evs.find((e) => e.kind.startsWith('host.'));
    if (!hostEv || hostEv.ts === lastSeenTs) return;
    lastSeenTs = hostEv.ts;
    void refresh();
  });

  onDestroy(() => { /* interval cleanup handled by the $effect's return */ });

  let azs = $derived.by(() => {
    const s = new Set<string>();
    for (const h of hosts) if (h.az) s.add(h.az);
    return Array.from(s).sort();
  });

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return hosts.filter((h) => {
      if (azFilter && h.az !== azFilter) return false;
      if (!q) return true;
      return (
        h.name.toLowerCase().includes(q)
        || (h.uuid ?? '').toLowerCase().includes(q)
        || (h.rack ?? '').toLowerCase().includes(q)
        || (h.hypervisor ?? '').toLowerCase().includes(q)
        || (h.arch ?? '').toLowerCase().includes(q)
        || (h.status ?? '').toLowerCase().includes(q)
      );
    });
  });

  function statusBadge(s: string): string {
    switch (s) {
      case 'active':       return 'badge-success';
      case 'draining':     return 'badge-warning';
      case 'provisioning': return 'badge-info';
      case 'down':         return 'badge-error';
      case 'removed':      return 'badge-ghost';
      default:             return 'badge-ghost';
    }
  }

  async function rowCordon(h: HostRow, ev: MouseEvent) {
    ev.stopPropagation();
    actionBusy = true; actionErr = '';
    try { await cordonHost(h.uuid, h); await refresh(); } catch (e) { actionErr = String(e); }
    finally { actionBusy = false; }
  }

  async function rowUncordon(h: HostRow, ev: MouseEvent) {
    ev.stopPropagation();
    actionBusy = true; actionErr = '';
    try { await uncordonHost(h.uuid, h); await refresh(); } catch (e) { actionErr = String(e); }
    finally { actionBusy = false; }
  }

  async function rowRemove(h: HostRow, ev: MouseEvent) {
    ev.stopPropagation();
    if (!confirm(
      `Remove host "${h.name}" (${h.uuid}) from the inventory ?\n\n` +
      `The host record is deleted from etcd ; in-flight VMs on this host stay ` +
      `running until the operator drains them.`,
    )) return;
    actionBusy = true; actionErr = '';
    try {
      await removeHost(h.uuid);
      if (selectedUUID === h.uuid) selectedUUID = '';
      await refresh();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }

  function clickRow(h: HostRow) {
    selectedUUID = selectedUUID === h.uuid ? '' : h.uuid;
    actionErr = '';
  }

  function onDrawerRemoved(uuid: string) {
    if (selectedUUID === uuid) selectedUUID = '';
    void refresh();
  }
</script>

<section class="flex flex-col gap-3">
  <header class="flex items-baseline gap-3">
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-xs text-base-content/50">
      Cluster inventory — register via <code class="font-mono">weft host register</code>.
    </p>
  </header>

  <!-- Filter strip -->
  <div class="flex items-center gap-2">
    <label class="input input-sm input-bordered flex items-center gap-2 max-w-md">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter hosts…" bind:value={query} />
    </label>

    <select class="select select-sm select-bordered" bind:value={azFilter} title="Filter by availability zone">
      <option value="">All AZs ({hosts.length})</option>
      {#each azs as az (az)}
        <option value={az}>{az}</option>
      {/each}
    </select>

    <span class="ml-auto text-xs text-base-content/50">
      {filtered.length} of {hosts.length} hosts
    </span>
  </div>

  {#if err}<div class="alert alert-error py-2 text-sm">{err}</div>{/if}
  {#if actionErr}<div class="alert alert-error py-2 text-sm">{actionErr}</div>{/if}

  <!-- Table -->
  <div class="rounded-box border border-base-300 bg-base-100 overflow-x-auto">
    <table class="table table-sm">
      <thead>
        <tr>
          <th>Hostname</th>
          <th>AZ</th>
          <th>Rack</th>
          <th>Hypervisor</th>
          <th>Arch</th>
          <th>Status</th>
          <th>Connected</th>
          <th>Last seen</th>
          <th class="text-right">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#if loading}
          <tr><td colspan="9" class="py-8 text-center">
            <span class="loading loading-spinner"></span>
          </td></tr>
        {:else if filtered.length === 0}
          <tr><td colspan="9" class="py-8 text-center text-base-content/50">
            {hosts.length === 0
              ? 'No hosts registered. Use "weft host register" on the CLI to add the first node.'
              : 'No hosts match the filter.'}
          </td></tr>
        {:else}
          {#each filtered as h (h.uuid)}
            <tr class="hover cursor-pointer"
              class:bg-primary={selectedUUID === h.uuid}
              class:text-primary-content={selectedUUID === h.uuid}
              onclick={() => clickRow(h)}>
              <td class="font-mono">
                <div class="flex flex-col">
                  <span>{h.name}</span>
                  <span class="text-[10px] opacity-60">{h.uuid}</span>
                </div>
              </td>
              <td class="font-mono">{h.az || '—'}</td>
              <td class="font-mono">{h.rack || '—'}</td>
              <td class="font-mono text-xs">{h.hypervisor || '—'}</td>
              <td class="font-mono text-xs">{h.arch || '—'}</td>
              <td>
                <span class="badge badge-sm {statusBadge(h.status)}">{h.status || 'unknown'}</span>
              </td>
              <td>
                {#if h.connected === true}
                  <span class="badge badge-xs badge-success">live</span>
                {:else if h.connected === false}
                  <span class="badge badge-xs badge-error">off</span>
                {:else}
                  <span class="text-base-content/40">—</span>
                {/if}
              </td>
              <td class="font-mono text-xs">{(h.last_seen ?? '').slice(0, 19) || '—'}</td>
              <td class="text-right">
                {#if canEdit}
                  <div class="flex justify-end gap-1">
                    {#if h.status === 'draining'}
                      <button class="btn btn-xs btn-ghost"
                        disabled={actionBusy}
                        title="Mark this host eligible for scheduling again"
                        onclick={(e) => rowUncordon(h, e)}>
                        Uncordon
                      </button>
                    {:else}
                      <button class="btn btn-xs btn-ghost"
                        disabled={actionBusy}
                        title="Stop scheduling new workloads on this host"
                        onclick={(e) => rowCordon(h, e)}>
                        Cordon
                      </button>
                    {/if}
                    <button class="btn btn-xs btn-ghost text-error"
                      disabled={actionBusy}
                      title="Delete the host record"
                      onclick={(e) => rowRemove(h, e)}>
                      Remove
                    </button>
                  </div>
                {:else}
                  <span class="text-xs text-base-content/40">read-only</span>
                {/if}
              </td>
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </div>
</section>

{#if selected}
  <HostDrawer
    host={selected}
    {canEdit}
    onClose={() => (selectedUUID = '')}
    onChanged={refresh}
    onRemoved={onDrawerRemoved}
  />
{/if}
