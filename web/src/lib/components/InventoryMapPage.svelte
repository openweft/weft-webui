<script lang="ts">
  // InventoryMapPage — placement view of the cluster.
  //
  // Two layouts behind a 2D / 3D toggle :
  //
  //  - 2D (default, rack-elevation) : flat datacenter view in the
  //    style of count.racku.la. Each AZ gets a horizontal section
  //    with one column per rack ; each column is a tall rectangle
  //    showing host slots from top to bottom, with microVMs packed
  //    inside each host's slot. Space-efficient — dozens of racks
  //    fit on screen.
  //
  //  - 3D (isometric) : the axonometric view, AZs as ground tiles,
  //    racks as 3D boxes rising out of them, microVM dots on top.
  //    Pretty for screenshots ; not space-efficient for big fleets.
  //
  // Both views poll /api/resources every 5 s.

  import { onMount, onDestroy } from 'svelte';
  import { getRowsPage, getAllRows, type Row, type ResourceMeta } from '../api';
  import Iso3DView from './Iso3DView.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  let azs   = $state<Row[]>([]);
  let racks = $state<Row[]>([]);
  let hosts = $state<Row[]>([]);
  let vms   = $state<Row[]>([]);
  let loadErr = $state('');
  let lastRefresh = $state<string>('');
  let viewMode = $state<'2d' | '3d'>('2d');

  let pollTimer: ReturnType<typeof setInterval> | undefined;

  async function refresh() {
    try {
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

  // ---- joins ----------------------------------------------------

  let racksByAZ = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const r of racks) {
      const az = String(r.az ?? '');
      const arr = m.get(az) ?? [];
      arr.push(r);
      m.set(az, arr);
    }
    return m;
  });

  let hostsByRack = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const h of hosts) {
      const k = String(h.az ?? '') + '|' + String(h.rack ?? '');
      const arr = m.get(k) ?? [];
      arr.push(h);
      m.set(k, arr);
    }
    return m;
  });

  let vmsByHost = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const v of vms) {
      const host = String(v.host ?? '');
      if (!host) continue;
      const arr = m.get(host) ?? [];
      arr.push(v);
      m.set(host, arr);
    }
    return m;
  });

  let totalVMs = $derived(vms.length);

  // ---- helpers --------------------------------------------------

  function hostStatusClass(status: string): string {
    switch (status) {
      case 'active':       return 'bg-success/15 border-success/60';
      case 'draining':     return 'bg-warning/15 border-warning/60';
      case 'down':         return 'bg-error/15 border-error/60';
      case 'provisioning': return 'bg-info/15 border-info/60';
      default:             return 'bg-base-200 border-base-300';
    }
  }

  function vmColor(state: string): string {
    switch (state) {
      case 'running':  return '#3b82f6';
      case 'starting': return '#f59e0b';
      case 'stopped':  return '#9ca3af';
      case 'failed':   return '#ef4444';
      default:         return '#6b7280';
    }
  }

  // Density indicator for the rack header : ratio of (hosts in rack)
  // to a soft cap of 8 (we don't have rated capacity on the seed).
  function rackFill(n: number): number {
    return Math.min(1, n / 8);
  }

  // Per-arch chip styling. amd64 / arm64 are the bulk of the fleet ;
  // riscv64 + loong64 are the "exotic" archs the scheduler can also
  // target — we colour them distinctly so heterogeneous clusters
  // are visible at a glance.
  function archClass(arch: string): string {
    switch (arch) {
      case 'amd64':   return 'bg-sky-500/15 text-sky-700 border-sky-500/40';
      case 'arm64':   return 'bg-emerald-500/15 text-emerald-700 border-emerald-500/40';
      case 'riscv64': return 'bg-violet-500/15 text-violet-700 border-violet-500/40';
      case 'loong64': return 'bg-amber-500/15 text-amber-700 border-amber-500/40';
      default:        return 'bg-base-200 text-base-content/60 border-base-300';
    }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      {azs.length} AZ · {racks.length} racks ·
      {hosts.length} hosts · {totalVMs} microVMs
      {#if lastRefresh}
        · <span class="text-xs text-base-content/40">refreshed {lastRefresh}</span>
      {/if}
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <!-- View toggle. tabs-border is daisyUI 5 ; the older
         tabs-bordered name was renamed in v5. -->
    <div role="tablist" class="tabs tabs-box tabs-sm">
      <button role="tab" class="tab" class:tab-active={viewMode === '2d'}
        onclick={() => (viewMode = '2d')}>2D</button>
      <button role="tab" class="tab" class:tab-active={viewMode === '3d'}
        onclick={() => (viewMode = '3d')}>3D</button>
    </div>
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

{#if viewMode === '2d'}
  <!-- ===== 2D rack-elevation view ===== -->
  <div class="mt-4 space-y-6">
    {#each azs as az, azIdx (az.uuid ?? az.code ?? azIdx)}
      {@const azRacks = racksByAZ.get(String(az.code ?? '')) ?? []}
      <section class="rounded-box border border-base-300 bg-base-100">
        <!-- AZ banner -->
        <header class="flex items-center justify-between border-b border-base-300 px-4 py-2">
          <div>
            <div class="text-sm font-semibold text-base-content">
              {az.code}
              <span class="font-normal text-base-content/50">· {az.name}</span>
            </div>
            <div class="text-xs text-base-content/40">{az.region ?? ''} · {azRacks.length} racks</div>
          </div>
          <span class="badge badge-sm badge-ghost">{az.status ?? 'unknown'}</span>
        </header>

        <!-- Rack row : horizontal scroll for large AZs. -->
        <div class="flex gap-4 overflow-x-auto p-4">
          {#each azRacks as rack, rkIdx (rack.uuid ?? rkIdx)}
            {@const hostsInRack = hostsByRack.get(String(az.code ?? '') + '|' + String(rack.code ?? '')) ?? []}

            <article class="flex w-44 shrink-0 flex-col rounded-md border border-base-300 bg-base-200/40">
              <!-- Rack header : code + density bar -->
              <header class="flex items-center justify-between border-b border-base-300 px-2 py-1">
                <span class="font-mono text-xs font-semibold">{rack.code}</span>
                <span class="text-[10px] text-base-content/50">{hostsInRack.length} / 8U</span>
              </header>
              <!-- Density indicator strip -->
              <div class="h-1 bg-base-300">
                <div class="h-full bg-primary/70" style="width: {rackFill(hostsInRack.length) * 100}%"></div>
              </div>

              <!-- Host slots, stacked top-to-bottom like a rack
                   elevation. Empty slots are visualised so the
                   rack reads as having spare U capacity. -->
              <div class="flex flex-col gap-1 p-2">
                {#each hostsInRack as host (host.uuid ?? host.name)}
                  {@const hostVMs = vmsByHost.get(String(host.name ?? '')) ?? []}
                  <div
                    class="rounded border px-2 py-1 {hostStatusClass(String(host.status ?? ''))}"
                    title={`${host.name} · ${host.arch} · ${host.hypervisor} · ${hostVMs.length} VMs`}
                  >
                    <div class="flex items-center justify-between">
                      <span class="truncate font-mono text-[11px] font-medium">{host.name}</span>
                      <span class="ml-1 inline-flex shrink-0 items-center rounded border px-1 py-[1px] font-mono text-[9px] {archClass(String(host.arch ?? ''))}">
                        {host.arch}
                      </span>
                    </div>
                    <!-- VM dots packed into the host card. Show
                         up to 24 ; overflow becomes "+N". -->
                    {#if hostVMs.length > 0}
                      <div class="mt-1 flex flex-wrap gap-0.5">
                        {#each hostVMs.slice(0, 24) as vm (vm.uuid ?? vm.name)}
                          <span
                            class="inline-block h-2 w-2 rounded-sm"
                            style="background:{vmColor(String(vm.state ?? ''))}"
                            title="{vm.name} · {vm.state} · {vm.image ?? ''}"
                          ></span>
                        {/each}
                        {#if hostVMs.length > 24}
                          <span class="text-[9px] text-base-content/50">+{hostVMs.length - 24}</span>
                        {/if}
                      </div>
                    {/if}
                  </div>
                {/each}

                <!-- Empty U slots for the soft 8-U cap, so the rack
                     visually reads "X of 8 used". -->
                {#each Array(Math.max(0, 8 - hostsInRack.length)) as _, slotIdx (slotIdx)}
                  <div class="h-5 rounded border border-dashed border-base-300/60 bg-base-100/40"></div>
                {/each}
              </div>
            </article>
          {/each}

          {#if azRacks.length === 0}
            <div class="text-sm italic text-base-content/40 p-4">no racks declared</div>
          {/if}
        </div>
      </section>
    {/each}

    {#if azs.length === 0}
      <div class="rounded-box border border-base-300 bg-base-100 p-8 text-center text-base-content/50">
        no availability zones declared
      </div>
    {/if}
  </div>
{:else}
  <!-- ===== 3D isometric view ===== -->
  <Iso3DView {azs} {racksByAZ} {hostsByRack} {vmsByHost} />
{/if}

<!-- Legend pinned below both views. -->
<div class="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-box border border-base-300 bg-base-100 px-4 py-2 text-xs">
  <span class="font-semibold text-base-content/70">Hosts</span>
  <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded bg-success/15 border border-success/60"></span> active</span>
  <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded bg-warning/15 border border-warning/60"></span> draining</span>
  <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded bg-error/15 border border-error/60"></span> down</span>
  <span class="mx-2 h-3 w-px bg-base-300"></span>
  <span class="font-semibold text-base-content/70">Arch</span>
  <span class="inline-flex items-center rounded border px-1 py-[1px] font-mono text-[10px] {archClass('amd64')}">amd64</span>
  <span class="inline-flex items-center rounded border px-1 py-[1px] font-mono text-[10px] {archClass('arm64')}">arm64</span>
  <span class="inline-flex items-center rounded border px-1 py-[1px] font-mono text-[10px] {archClass('riscv64')}">riscv64</span>
  <span class="inline-flex items-center rounded border px-1 py-[1px] font-mono text-[10px] {archClass('loong64')}">loong64</span>
  <span class="mx-2 h-3 w-px bg-base-300"></span>
  <span class="font-semibold text-base-content/70">microVMs</span>
  <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-sm" style="background:#3b82f6"></span> running</span>
  <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-sm" style="background:#f59e0b"></span> starting</span>
  <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-sm" style="background:#9ca3af"></span> stopped</span>
  <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-sm" style="background:#ef4444"></span> failed</span>
</div>

