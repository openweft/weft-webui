<script lang="ts">
  import { getRows, type ResourceMeta, type Row } from '../api';
  import ResourceTable from './ResourceTable.svelte';
  import CreateVMModal from './CreateVMModal.svelte';
  import CreateVolumeModal from './CreateVolumeModal.svelte';
  import CreateNetworkModal from './CreateNetworkModal.svelte';
  import CreateSchedulingRuleModal from './CreateSchedulingRuleModal.svelte';
  import MicroVMDrawer from './MicroVMDrawer.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  let rows = $state<Row[]>([]);
  let loading = $state(true);
  let error = $state('');
  let query = $state('');

  function refresh() {
    const id = meta.id;
    loading = true;
    error = '';
    getRows(id)
      .then((r) => (rows = r))
      .catch((e) => (error = String(e)))
      .finally(() => (loading = false));
  }

  // Re-fetch whenever the selected resource changes.
  $effect(() => {
    query = '';
    refresh();
  });

  // Per-resource extra filter. dns-records gains a zone selector so
  // a busy table (one row per record across many zones) stays usable.
  let zoneFilter = $state('');
  let zones = $derived.by<string[]>(() => {
    if (meta.id !== 'dns-records') return [];
    const set = new Set<string>();
    for (const r of rows) {
      if (typeof r.zone === 'string') set.add(r.zone);
    }
    return [...set].sort();
  });

  // Reset the zone selector whenever the resource changes.
  $effect(() => {
    meta.id; // dependency
    zoneFilter = '';
  });

  let filtered = $derived.by(() => {
    let xs = rows;
    if (meta.id === 'dns-records' && zoneFilter) {
      xs = xs.filter((r) => r.zone === zoneFilter);
    }
    if (query.trim() !== '') {
      const q = query.toLowerCase();
      xs = xs.filter((r) =>
        Object.values(r).some((v) => String(v).toLowerCase().includes(q)),
      );
    }
    return xs;
  });

  // ---- "+ New X" affordance ----
  //
  // Only the resources that have a Create endpoint show the button as
  // active. Everything else keeps it disabled with a hint — clicking
  // a stub button used to do nothing, now it's at least honest.
  let createOpen = $state(false);
  // Row-detail drawer. Currently only wired for microvms ; other
  // resources will get their own drawer components as the relevant
  // RPCs land (volumes → VolumeInfo, networks → NetworkInfo, …).
  let selectedRow = $state<Row | null>(null);
  function handleSelect(row: Row) {
    if (meta.id === 'microvms') selectedRow = row;
  }
  const creatable = ['microvms', 'volumes', 'networks', 'scheduling-rules'];
  let canCreate = $derived(creatable.includes(meta.id));
  let createLabel = $derived(
    meta.label
      .replace(/s$/, '')
      .replace('microVMs', 'microVM')
      .replace('Scheduling Rule', 'rule'),
  );
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      {filtered.length} of {rows.length}
      {rows.length === 1 ? 'item' : 'items'} · section {meta.section}
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    {#if meta.id === 'dns-records' && zones.length > 1}
      <select class="select select-sm select-bordered" bind:value={zoneFilter}>
        <option value="">all zones ({zones.length})</option>
        {#each zones as z (z)}
          <option value={z}>{z}</option>
        {/each}
      </select>
    {/if}
    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter…" bind:value={query} />
    </label>
    <button
      class="btn btn-sm btn-primary gap-1"
      disabled={!canCreate}
      title={canCreate ? '' : 'Creation flow not wired yet'}
      onclick={() => (createOpen = true)}
    >
      <span class="text-base leading-none">+</span> New {createLabel}
    </button>
  </div>
</div>

<div class="mt-4">
  {#if loading}
    <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
  {:else if error}
    <div class="alert alert-error">{error}</div>
  {:else}
    <ResourceTable columns={meta.columns} rows={filtered}
      resourceId={meta.id} onChange={refresh}
      onSelect={meta.id === 'microvms' ? handleSelect : undefined} />
  {/if}
</div>

<!-- Create modals : one per resource that has a wired endpoint.
     They're driven by the same {createOpen} flag and the page only
     ever mounts the relevant one. -->
{#if meta.id === 'microvms'}
  <CreateVMModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'volumes'}
  <CreateVolumeModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'networks'}
  <CreateNetworkModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'scheduling-rules'}
  <CreateSchedulingRuleModal bind:open={createOpen} onCreated={refresh} />
{/if}

{#if meta.id === 'microvms' && selectedRow}
  <MicroVMDrawer
    row={selectedRow}
    onClose={() => (selectedRow = null)}
    onChanged={refresh}
  />
{/if}
