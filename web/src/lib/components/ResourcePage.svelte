<script lang="ts">
  import { getRows, type ResourceMeta, type Row } from '../api';
  import ResourceTable from './ResourceTable.svelte';
  import CreateVMModal from './CreateVMModal.svelte';
  import CreateVolumeModal from './CreateVolumeModal.svelte';

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

  let filtered = $derived(
    query.trim() === ''
      ? rows
      : rows.filter((r) =>
          Object.values(r).some((v) => String(v).toLowerCase().includes(query.toLowerCase())),
        ),
  );

  // ---- "+ New X" affordance ----
  //
  // Only the resources that have a Create endpoint show the button as
  // active. Everything else keeps it disabled with a hint — clicking
  // a stub button used to do nothing, now it's at least honest.
  let createOpen = $state(false);
  const creatable = ['microvms', 'volumes'];
  let canCreate = $derived(creatable.includes(meta.id));
  let createLabel = $derived(meta.label.replace(/s$/, '').replace('microVMs', 'microVM'));
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
      resourceId={meta.id} onChange={refresh} />
  {/if}
</div>

<!-- Create modals : one per resource that has a wired endpoint.
     They're driven by the same {createOpen} flag and the page only
     ever mounts the relevant one. -->
{#if meta.id === 'microvms'}
  <CreateVMModal bind:open={createOpen} onCreated={refresh} />
{:else if meta.id === 'volumes'}
  <CreateVolumeModal bind:open={createOpen} onCreated={refresh} />
{/if}
