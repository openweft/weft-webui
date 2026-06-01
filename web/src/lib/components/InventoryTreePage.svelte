<script lang="ts">
  // InventoryTreePage — collapsible tree of AZ → Rack → Host →
  // microVM. Primary surface for inventory placement : the operator
  // walks the hierarchy, selects a node, and the right pane shows
  // its details + an Edit button that routes to the corresponding
  // per-resource panel (AZs / Racks / Hosts / microVMs) for CRUD.
  //
  // We deliberately don't duplicate create / edit modals here — the
  // dedicated panels are the source of truth. The tree adds the
  // hierarchical navigation that flat tables can't surface.
  //
  // Live signal : polls /api/resources every 5 s and re-renders,
  // same cadence as InventoryMapPage.
  import { onMount, onDestroy } from 'svelte';
  import { getRowsPage, getAllRows, type Row, type ResourceMeta } from '../api';

  let { meta }: { meta: ResourceMeta } = $props();

  let azs   = $state<Row[]>([]);
  let racks = $state<Row[]>([]);
  let hosts = $state<Row[]>([]);
  let vms   = $state<Row[]>([]);
  let loadErr = $state('');
  let lastRefresh = $state<string>('');

  // Selection : `{kind, key}` where kind ∈ {az, rack, host, vm} and
  // key is the row's stable identifier (code for az/rack, name for
  // host/vm). null = nothing selected.
  type Selected = { kind: 'az' | 'rack' | 'host' | 'vm'; key: string } | null;
  let selected = $state<Selected>(null);

  // Expanded set : nodes the operator has opened. Initial state =
  // all AZs expanded so the cluster is visible at a glance.
  let expanded = $state<Set<string>>(new Set());

  let pollTimer: ReturnType<typeof setInterval> | undefined;

  async function refresh() {
    try {
      // /api/resources/{id} validates limit ∈ [0, 1000] (huma
      // schema). AZs / racks / hosts comfortably fit in one page —
      // a cluster running thousands of those would have other
      // problems first. microVMs can grow past 1 k, so we walk the
      // next-page-token chain via getAllRows (capped at 50 k
      // total to bound dashboard memory).
      const [a, r, h, v] = await Promise.all([
        getRowsPage('azs',   { limit: 1000 }),
        getRowsPage('racks', { limit: 1000 }),
        getRowsPage('hosts', { limit: 1000 }),
        getAllRows('microvms', { perPage: 1000, maxPages: 50 }),
      ]);
      azs   = a.rows ?? [];
      racks = r.rows ?? [];
      hosts = h.rows ?? [];
      vms   = v;
      // First load only : expand every AZ so the hierarchy is
      // immediately visible. Subsequent refreshes preserve the
      // operator's collapse state.
      if (expanded.size === 0 && azs.length > 0) {
        const next = new Set<string>();
        for (const az of azs) next.add('az:' + String(az.code ?? ''));
        expanded = next;
      }
      loadErr = '';
      lastRefresh = new Date().toLocaleTimeString();
    } catch (e) {
      loadErr = String(e);
    }
  }

  onMount(() => {
    refresh();
    pollTimer = setInterval(refresh, 5000);
  });
  onDestroy(() => { if (pollTimer) clearInterval(pollTimer); });

  function toggle(nodeID: string) {
    const next = new Set(expanded);
    if (next.has(nodeID)) next.delete(nodeID);
    else next.add(nodeID);
    expanded = next;
  }

  // ---- joins -----------------------------------------------------

  let racksByAZ = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const r of racks) {
      const az = String(r.az ?? '');
      (m.get(az) ?? m.set(az, []).get(az))!.push(r);
    }
    return m;
  });

  let hostsByRack = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const h of hosts) {
      const k = String(h.az ?? '') + '|' + String(h.rack ?? '');
      (m.get(k) ?? m.set(k, []).get(k))!.push(h);
    }
    return m;
  });

  let vmsByHost = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const v of vms) {
      const host = String(v.host ?? '');
      if (!host) continue;
      (m.get(host) ?? m.set(host, []).get(host))!.push(v);
    }
    return m;
  });

  // ---- selection details ----------------------------------------

  let selectedRow = $derived.by<Row | null>(() => {
    if (!selected) return null;
    switch (selected.kind) {
      case 'az':   return azs.find((a) => String(a.code ?? '') === selected!.key) ?? null;
      case 'rack': {
        // key = "<azCode>|<rackCode>"
        const [az, code] = selected!.key.split('|');
        return racks.find((r) => String(r.az ?? '') === az && String(r.code ?? '') === code) ?? null;
      }
      case 'host': return hosts.find((h) => String(h.name ?? '') === selected!.key) ?? null;
      case 'vm':   return vms.find((v) => String(v.name ?? '') === selected!.key) ?? null;
    }
  });

  let selectedTitle = $derived.by(() => {
    if (!selected || !selectedRow) return '';
    switch (selected.kind) {
      case 'az':   return `${selectedRow.code} · ${selectedRow.name}`;
      case 'rack': return `Rack ${selectedRow.code} in ${selectedRow.az}`;
      case 'host': return String(selectedRow.name);
      case 'vm':   return String(selectedRow.name);
    }
  });

  function statusDot(status: string): string {
    switch (status) {
      case 'active':       return 'bg-success';
      case 'draining':     return 'bg-warning';
      case 'down':         return 'bg-error';
      case 'provisioning': return 'bg-info';
      case 'running':      return 'bg-success';
      case 'starting':     return 'bg-info';
      case 'stopped':      return 'bg-base-content/30';
      case 'failed':       return 'bg-error';
      default:             return 'bg-base-content/30';
    }
  }

</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      Hierarchical placement editor · {azs.length} AZ · {racks.length} racks ·
      {hosts.length} hosts · {vms.length} microVMs
      {#if lastRefresh}
        · <span class="text-xs text-base-content/40">refreshed {lastRefresh}</span>
      {/if}
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <button class="btn btn-sm btn-ghost gap-1" onclick={refresh} title="Force refresh">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M3 12a9 9 0 1 0 3-6.7M3 4v5h5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      Refresh
    </button>
  </div>
</div>

{#if loadErr}
  <div class="alert alert-error mt-4 py-2 text-sm">{loadErr}</div>
{/if}

<div class="mt-4 flex gap-4">
  <!-- Master : tree -->
  <section class="w-96 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Hierarchy</h3>
      <span class="ml-auto text-xs text-base-content/50">{azs.length} AZ</span>
    </div>
    <div class="rounded-box border border-base-300 bg-base-100 p-2">
      {#each azs as az (az.uuid ?? az.code)}
        {@const azID = 'az:' + String(az.code ?? '')}
        {@const isAZExpanded = expanded.has(azID)}
        {@const azRacks = racksByAZ.get(String(az.code ?? '')) ?? []}
        {@const isAZSelected = selected?.kind === 'az' && selected.key === String(az.code ?? '')}

        <!-- AZ row -->
        <div class="flex items-center gap-1 rounded px-1 py-1 text-sm
                    {isAZSelected ? 'bg-primary text-primary-content' : 'hover:bg-base-200'}">
          <button class="btn btn-ghost btn-xs px-1"
            onclick={() => toggle(azID)} aria-label="toggle">
            <svg class="h-3.5 w-3.5 transition-transform {isAZExpanded ? 'rotate-90' : ''}"
              viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="m9 18 6-6-6-6" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </button>
          <button class="flex-1 text-left flex items-center gap-2"
            onclick={() => selected = { kind: 'az', key: String(az.code ?? '') }}>
            <span class="inline-block w-2 h-2 rounded-full {statusDot(String(az.status ?? ''))}"></span>
            <span class="font-mono font-semibold">{az.code}</span>
            <span class="text-xs opacity-70 truncate">{az.name}</span>
            <span class="ml-auto badge badge-xs badge-ghost">{azRacks.length}r</span>
          </button>
        </div>

        {#if isAZExpanded}
          {#each azRacks as rack (rack.uuid ?? rack.code)}
            {@const rackID = 'rack:' + String(az.code ?? '') + '|' + String(rack.code ?? '')}
            {@const isRackExpanded = expanded.has(rackID)}
            {@const rackHosts = hostsByRack.get(String(az.code ?? '') + '|' + String(rack.code ?? '')) ?? []}
            {@const isRackSelected = selected?.kind === 'rack'
              && selected.key === String(az.code ?? '') + '|' + String(rack.code ?? '')}

            <div class="ml-5 flex items-center gap-1 rounded px-1 py-1 text-sm
                        {isRackSelected ? 'bg-primary text-primary-content' : 'hover:bg-base-200'}">
              <button class="btn btn-ghost btn-xs px-1"
                onclick={() => toggle(rackID)} aria-label="toggle">
                <svg class="h-3.5 w-3.5 transition-transform {isRackExpanded ? 'rotate-90' : ''}"
                  viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="m9 18 6-6-6-6" stroke-linecap="round" stroke-linejoin="round"/>
                </svg>
              </button>
              <button class="flex-1 text-left flex items-center gap-2"
                onclick={() => selected = { kind: 'rack', key: String(az.code ?? '') + '|' + String(rack.code ?? '') }}>
                <span class="inline-block w-2 h-2 rounded-full {statusDot(String(rack.status ?? ''))}"></span>
                <span class="font-mono">{rack.code}</span>
                <span class="text-xs opacity-60">· {rack.position}</span>
                <span class="ml-auto badge badge-xs badge-ghost">{rackHosts.length}h</span>
              </button>
            </div>

            {#if isRackExpanded}
              {#each rackHosts as host (host.uuid ?? host.name)}
                {@const hostID = 'host:' + String(host.name ?? '')}
                {@const isHostExpanded = expanded.has(hostID)}
                {@const hostVMs = vmsByHost.get(String(host.name ?? '')) ?? []}
                {@const isHostSelected = selected?.kind === 'host' && selected.key === String(host.name ?? '')}

                <div class="ml-10 flex items-center gap-1 rounded px-1 py-1 text-xs
                            {isHostSelected ? 'bg-primary text-primary-content' : 'hover:bg-base-200'}">
                  <button class="btn btn-ghost btn-xs px-1"
                    onclick={() => toggle(hostID)} aria-label="toggle"
                    disabled={hostVMs.length === 0}>
                    {#if hostVMs.length > 0}
                      <svg class="h-3 w-3 transition-transform {isHostExpanded ? 'rotate-90' : ''}"
                        viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="m9 18 6-6-6-6" stroke-linecap="round" stroke-linejoin="round"/>
                      </svg>
                    {:else}
                      <span class="inline-block w-3"></span>
                    {/if}
                  </button>
                  <button class="flex-1 text-left flex items-center gap-2"
                    onclick={() => selected = { kind: 'host', key: String(host.name ?? '') }}>
                    <span class="inline-block w-2 h-2 rounded-full {statusDot(String(host.status ?? ''))}"></span>
                    <span class="font-mono">{host.name}</span>
                    <span class="opacity-60">·</span>
                    <span class="opacity-70">{host.arch}/{host.hypervisor}</span>
                    {#if hostVMs.length > 0}
                      <span class="ml-auto badge badge-xs badge-ghost">{hostVMs.length}vm</span>
                    {/if}
                  </button>
                </div>

                {#if isHostExpanded}
                  {#each hostVMs as vm (vm.uuid ?? vm.name)}
                    {@const isVMSelected = selected?.kind === 'vm' && selected.key === String(vm.name ?? '')}
                    <div class="ml-16 flex items-center gap-1 rounded px-1 py-0.5 text-xs
                                {isVMSelected ? 'bg-primary text-primary-content' : 'hover:bg-base-200'}">
                      <span class="inline-block w-3"></span>
                      <button class="flex-1 text-left flex items-center gap-2"
                        onclick={() => selected = { kind: 'vm', key: String(vm.name ?? '') }}>
                        <span class="inline-block w-2 h-2 rounded-full {statusDot(String(vm.state ?? ''))}"></span>
                        <span class="font-mono">{vm.name}</span>
                        <span class="opacity-60">·</span>
                        <span class="opacity-70 truncate">{vm.image}</span>
                      </button>
                    </div>
                  {/each}
                {/if}
              {/each}
              {#if rackHosts.length === 0}
                <div class="ml-10 px-1 py-0.5 text-xs italic text-base-content/40">
                  no hosts in this rack
                </div>
              {/if}
            {/if}
          {/each}
          {#if azRacks.length === 0}
            <div class="ml-5 px-1 py-0.5 text-xs italic text-base-content/40">
              no racks in this AZ
            </div>
          {/if}
        {/if}
      {/each}
      {#if azs.length === 0}
        <div class="px-2 py-4 text-center text-sm text-base-content/40">
          No AZs declared yet. Open the AZs panel to create one.
        </div>
      {/if}
    </div>
  </section>

  <!-- Detail : selected node -->
  <section class="min-w-0 flex-1 flex flex-col gap-3">
    {#if !selected || !selectedRow}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a node in the tree on the left.
      </div>
    {:else}
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-lg font-semibold">{selectedTitle}</h3>
          <p class="text-xs text-base-content/50">
            <span class="badge badge-xs badge-ghost">{selected.kind}</span>
            {#if selectedRow.status}
              · <span class="inline-flex items-center gap-1">
                <span class="inline-block w-2 h-2 rounded-full {statusDot(String(selectedRow.status))}"></span>
                {selectedRow.status}
              </span>
            {/if}
            {#if selectedRow.state}
              · <span class="inline-flex items-center gap-1">
                <span class="inline-block w-2 h-2 rounded-full {statusDot(String(selectedRow.state))}"></span>
                {selectedRow.state}
              </span>
            {/if}
          </p>
        </div>
      </div>

      <div class="rounded-box border border-base-300 bg-base-100 p-4">
        <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60 mb-2">
          Fields
        </h4>
        <dl class="grid grid-cols-2 gap-y-2 text-sm">
          {#each Object.entries(selectedRow) as [k, v] (k)}
            {#if v !== null && v !== undefined && v !== ''}
              <dt class="text-base-content/50 font-mono text-xs">{k}</dt>
              <dd class="text-base-content truncate">{v}</dd>
            {/if}
          {/each}
        </dl>
      </div>

      <!-- Context-aware shortcut summary : what's under this node -->
      {#if selected.kind === 'az'}
        {@const azCode = String(selectedRow.code ?? '')}
        {@const azRacks = racksByAZ.get(azCode) ?? []}
        {@const azHosts = hosts.filter((h) => String(h.az ?? '') === azCode)}
        {@const azVMs = vms.filter((v) => azHosts.some((h) => String(h.name ?? '') === String(v.host ?? '')))}
        <div class="rounded-box border border-base-300 bg-base-100 p-4">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60 mb-2">
            Contents
          </h4>
          <dl class="grid grid-cols-3 gap-y-2 text-sm">
            <div><dt class="text-xs text-base-content/50">Racks</dt><dd class="font-mono">{azRacks.length}</dd></div>
            <div><dt class="text-xs text-base-content/50">Hosts</dt><dd class="font-mono">{azHosts.length}</dd></div>
            <div><dt class="text-xs text-base-content/50">microVMs</dt><dd class="font-mono">{azVMs.length}</dd></div>
          </dl>
        </div>
      {:else if selected.kind === 'rack'}
        {@const rackHosts = hostsByRack.get(selected.key) ?? []}
        {@const rackVMs = vms.filter((v) => rackHosts.some((h) => String(h.name ?? '') === String(v.host ?? '')))}
        <div class="rounded-box border border-base-300 bg-base-100 p-4">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60 mb-2">
            Contents
          </h4>
          <dl class="grid grid-cols-2 gap-y-2 text-sm">
            <div><dt class="text-xs text-base-content/50">Hosts</dt><dd class="font-mono">{rackHosts.length}</dd></div>
            <div><dt class="text-xs text-base-content/50">microVMs</dt><dd class="font-mono">{rackVMs.length}</dd></div>
          </dl>
        </div>
      {:else if selected.kind === 'host'}
        {@const hostVMs = vmsByHost.get(selected.key) ?? []}
        <div class="rounded-box border border-base-300 bg-base-100 p-4">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60 mb-2">
            microVMs on this host ({hostVMs.length})
          </h4>
          {#if hostVMs.length === 0}
            <p class="text-sm text-base-content/50 italic">none</p>
          {:else}
            <ul class="space-y-1 text-sm">
              {#each hostVMs as vm (vm.uuid ?? vm.name)}
                <li class="flex items-center gap-2">
                  <span class="inline-block w-2 h-2 rounded-full {statusDot(String(vm.state ?? ''))}"></span>
                  <span class="font-mono">{vm.name}</span>
                  <span class="text-base-content/50">·</span>
                  <span class="text-xs text-base-content/60">{vm.image}</span>
                  <span class="ml-auto text-xs text-base-content/40">{vm.state}</span>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
    {/if}
  </section>
</div>
