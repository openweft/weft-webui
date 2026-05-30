<script lang="ts">
  import { getRows, type ResourceMeta, type Row } from '../api';
  import ResourceTable from './ResourceTable.svelte';
  import CreateVMModal from './CreateVMModal.svelte';
  import CreateVolumeModal from './CreateVolumeModal.svelte';
  import CreateNetworkModal from './CreateNetworkModal.svelte';
  import CreateSchedulingRuleModal from './CreateSchedulingRuleModal.svelte';
  import CreateSecurityGroupModal from './CreateSecurityGroupModal.svelte';
  import AllocateFloatingIPModal from './AllocateFloatingIPModal.svelte';
  import MapFloatingIPModal from './MapFloatingIPModal.svelte';
  import CreateRouterModal from './CreateRouterModal.svelte';
  import CreateLoadBalancerModal from './CreateLoadBalancerModal.svelte';
  import CreateDNSZoneModal from './CreateDNSZoneModal.svelte';
  import CreateDNSRecordModal from './CreateDNSRecordModal.svelte';
  import MicroVMDrawer from './MicroVMDrawer.svelte';
  import SecurityGroupDrawer from './SecurityGroupDrawer.svelte';

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
  // selectableDrawer : resources that open a detail drawer on row click.
  const selectableDrawer = ['microvms', 'security-groups'];
  function handleSelect(row: Row) {
    if (selectableDrawer.includes(meta.id)) selectedRow = row;
  }

  // Row dropdown intercept for FIP "Map" — ResourceTable invokes this
  // when the user picks the action that isn't a plain mutation.
  function handleRowAction(row: Row, action: string) {
    if (meta.id === 'floating-ips' && action === 'map') {
      mapFipTarget = { uuid: String(row.uuid), address: String(row.address) };
    }
  }
  const creatable = [
    'microvms', 'volumes', 'networks', 'scheduling-rules', 'security-groups',
    'floating-ips', 'routers', 'loadbalancers', 'dns-zones', 'dns-records',
  ];
  let canCreate = $derived(creatable.includes(meta.id));
  let createLabel = $derived(
    meta.label
      .replace(/s$/, '')
      .replace('microVMs', 'microVM')
      .replace('Scheduling Rule', 'rule')
      .replace('Security Group', 'SG')
      .replace('Floating IP', 'FIP')
      .replace('Load Balancer', 'LB')
      .replace('DNS Zone', 'zone')
      .replace('DNS Record', 'record'),
  );

  // Floating-IP "Map" action carries an extra modal triggered from the
  // row dropdown ; ResourcePage owns its open state so the modal lives
  // outside ResourceTable.
  let mapFipTarget = $state<{ uuid: string; address: string } | null>(null);
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
      onSelect={selectableDrawer.includes(meta.id) ? handleSelect : undefined}
      onAction={handleRowAction} />
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
{:else if meta.id === 'security-groups'}
  <CreateSecurityGroupModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'floating-ips'}
  <AllocateFloatingIPModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'routers'}
  <CreateRouterModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'loadbalancers'}
  <CreateLoadBalancerModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'dns-zones'}
  <CreateDNSZoneModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'dns-records'}
  <CreateDNSRecordModal bind:open={createOpen} onCreated={refresh} />
{/if}

{#if mapFipTarget}
  <MapFloatingIPModal
    fip={mapFipTarget}
    onClose={() => (mapFipTarget = null)}
    onMapped={refresh}
  />
{/if}

{#if meta.id === 'microvms' && selectedRow}
  <MicroVMDrawer
    row={selectedRow}
    onClose={() => (selectedRow = null)}
    onChanged={refresh}
  />
{:else if meta.id === 'security-groups' && selectedRow}
  <SecurityGroupDrawer
    row={selectedRow}
    onClose={() => (selectedRow = null)}
    onChanged={refresh}
  />
{/if}
