<script lang="ts">
  // PluginsPage — superadmin surface for the "weft plugin install"
  // model. Split-view :
  //
  //   - LEFT  : catalogue of installable plugins (cards, kind badge,
  //             description). Clicking a card opens the install drawer.
  //   - RIGHT : currently-installed instances with their bound VMs.
  //
  // The install drawer (PluginInstallDrawer.svelte) renders a form
  // from the catalogue entry's `inputs[]` schema and POSTs the values
  // to /api/plugins/install ; the returned instance_uuid is surfaced
  // back to the operator so they can reference it in CLI commands.
  import {
    listPluginCatalogue, listPluginInstances,
    type PluginCatalogueEntry, type PluginInstance,
    type ResourceMeta,
  } from '../api';
  import PluginInstallDrawer from './PluginInstallDrawer.svelte';

  let {
    meta,
    onChange,
  }: {
    meta: ResourceMeta;
    // Fires after a successful install so the parent (App.svelte) can
    // re-fetch the resource catalogue ; new plugin kinds may surface
    // new sidebar sections in the future.
    onChange: () => void;
  } = $props();

  let catalogue = $state<PluginCatalogueEntry[]>([]);
  let instances = $state<PluginInstance[]>([]);
  let loading = $state(true);
  let err = $state('');
  let query = $state('');
  let kindFilter = $state('all');
  let drawerOpen = $state(false);
  let drawerEntry = $state<PluginCatalogueEntry | null>(null);

  async function refresh() {
    loading = true; err = '';
    try {
      const [c, i] = await Promise.all([listPluginCatalogue(), listPluginInstances()]);
      catalogue = c;
      instances = i;
    } catch (e) {
      err = String(e);
    } finally {
      loading = false;
    }
  }
  $effect(() => { void refresh(); });

  // Kinds present in the catalogue, ordered by first-seen so a fresh
  // category bubbles up to the end of the filter combo without
  // shuffling familiar ones.
  let allKinds = $derived.by(() => {
    const seen: string[] = [];
    for (const e of catalogue) {
      if (!seen.includes(e.kind)) seen.push(e.kind);
    }
    return seen;
  });

  let kindCounts = $derived.by(() => {
    const m = new Map<string, number>();
    for (const e of catalogue) m.set(e.kind, (m.get(e.kind) ?? 0) + 1);
    return m;
  });

  let filteredCatalogue = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return catalogue.filter((e) => {
      if (kindFilter !== 'all' && e.kind !== kindFilter) return false;
      if (!q) return true;
      return (
        e.name.toLowerCase().includes(q)
        || e.description.toLowerCase().includes(q)
        || e.kind.toLowerCase().includes(q)
      );
    });
  });

  function instanceCount(name: string): number {
    return instances.filter((i) => i.name === name).length;
  }

  function openInstall(entry: PluginCatalogueEntry) {
    drawerEntry = entry;
    drawerOpen = true;
  }

  function onInstalled() {
    drawerOpen = false;
    drawerEntry = null;
    void refresh();
    onChange();
  }

  function statusBadge(status: string): { class: string; label: string } {
    if (status === 'running') return { class: 'badge-success', label: 'running' };
    if (status === 'provisioning') return { class: 'badge-warning', label: 'provisioning' };
    if (status === 'degraded') return { class: 'badge-warning', label: 'degraded' };
    if (status === 'failed') return { class: 'badge-error', label: 'failed' };
    return { class: 'badge-ghost', label: status };
  }

  function kindBadgeClass(kind: string): string {
    switch (kind) {
      case 'database':  return 'badge-info';
      case 'cache':     return 'badge-accent';
      case 'streaming': return 'badge-secondary';
      case 'analytics': return 'badge-primary';
      case 'storage':   return 'badge-warning';
      default:          return 'badge-ghost';
    }
  }

  function fmtInstalledAt(ts: string): string {
    if (!ts) return '—';
    return ts.slice(0, 19).replace('T', ' ');
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      Plugin catalogue · {instances.length} instance{instances.length === 1 ? '' : 's'} installed across {catalogue.length} available plugin{catalogue.length === 1 ? '' : 's'}.
      Click a catalogue card to install a fresh instance.
    </p>
  </div>
</div>

{#if err}
  <div class="alert alert-error mt-4">{err}</div>
{/if}

<div class="mt-4 flex gap-4">
  <!-- LEFT : catalogue cards ---------------------------------------->
  <section class="flex w-1/2 min-w-0 flex-col gap-3">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Catalogue</h3>
      {#if loading}<span class="loading loading-spinner loading-xs"></span>{/if}
      <span class="ml-auto text-xs text-base-content/50">{filteredCatalogue.length} of {catalogue.length}</span>
    </div>

    <div class="flex items-center gap-2">
      <select class="select select-sm select-bordered" bind:value={kindFilter}>
        <option value="all">All kinds · {catalogue.length}</option>
        {#each allKinds as k (k)}
          <option value={k}>{k} · {kindCounts.get(k) ?? 0}</option>
        {/each}
      </select>
      <label class="input input-sm input-bordered flex flex-1 items-center gap-2">
        <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
        </svg>
        <input type="search" class="grow" placeholder="Filter plugins…" bind:value={query} />
      </label>
    </div>

    <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
      {#each filteredCatalogue as entry (entry.name)}
        {@const count = instanceCount(entry.name)}
        <button class="card card-compact bg-base-100 text-left shadow border border-base-300 hover:border-primary transition-colors"
          onclick={() => openInstall(entry)}
          title={`Install a new ${entry.name} instance`}>
          <div class="card-body">
            <div class="flex items-center gap-2">
              <span class="card-title text-sm font-semibold truncate">{entry.name}</span>
              <span class="badge badge-xs {kindBadgeClass(entry.kind)}">{entry.kind}</span>
              {#if count > 0}
                <span class="badge badge-xs badge-success ml-auto" title={`${count} instance${count === 1 ? '' : 's'} installed`}>{count}</span>
              {/if}
            </div>
            <p class="text-xs text-base-content/60 line-clamp-3">{entry.description}</p>
            <div class="text-[10px] text-base-content/50">
              {entry.inputs?.length ?? 0} input{(entry.inputs?.length ?? 0) === 1 ? '' : 's'} required
            </div>
          </div>
        </button>
      {:else}
        <div class="col-span-full rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm text-base-content/50">
          {catalogue.length === 0 ? 'Catalogue is empty.' : 'No plugins match the filter.'}
        </div>
      {/each}
    </div>
  </section>

  <!-- RIGHT : installed instances ----------------------------------->
  <section class="flex w-1/2 min-w-0 flex-col gap-3">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Installed instances</h3>
      <span class="ml-auto text-xs text-base-content/50">{instances.length} total</span>
    </div>

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each instances as inst (inst.instance_uuid)}
        {@const b = statusBadge(inst.status)}
        <li class="border-b border-base-200 last:border-b-0">
          <div class="flex flex-col items-start gap-1 py-2">
            <div class="flex w-full items-center gap-2">
              <span class="font-medium">{inst.name}</span>
              <span class="badge badge-xs {b.class}">{b.label}</span>
              <span class="ml-auto font-mono text-[10px] text-base-content/50">{inst.instance_uuid}</span>
            </div>
            <div class="text-[10px] text-base-content/60">
              project <span class="font-mono">{inst.project}</span>
              · installed {fmtInstalledAt(inst.installed_at)}
              {#if inst.installed_by}· by {inst.installed_by}{/if}
            </div>
            {#if inst.vms && inst.vms.length > 0}
              <div class="flex flex-wrap gap-1">
                {#each inst.vms as vm (vm)}
                  <span class="badge badge-xs badge-outline font-mono">{vm}</span>
                {/each}
              </div>
            {:else}
              <div class="text-[10px] text-base-content/40 italic">no bound VMs yet</div>
            {/if}
          </div>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          No plugin instances installed yet — click a catalogue card on the left.
        </li>
      {/each}
    </ul>
  </section>
</div>

{#if drawerOpen && drawerEntry}
  <PluginInstallDrawer entry={drawerEntry} onClose={() => (drawerOpen = false)} onInstalled={onInstalled} />
{/if}
