<script lang="ts">
  import { onMount } from 'svelte';
  import { route } from './lib/router';
  import { getResources, type ResourceMeta } from './lib/api';
  import Sidebar from './lib/components/Sidebar.svelte';
  import Topbar from './lib/components/Topbar.svelte';
  import ResourcePage from './lib/components/ResourcePage.svelte';
  import ImagesPage from './lib/components/ImagesPage.svelte';
  import ObjectStoragePage from './lib/components/ObjectStoragePage.svelte';
  import Overview from './lib/components/Overview.svelte';

  let resources = $state<ResourceMeta[]>([]);
  let loaded = $state(false);
  let error = $state('');

  onMount(async () => {
    try {
      resources = await getResources();
    } catch (e) {
      error = String(e);
    } finally {
      loaded = true;
    }
  });

  let byId = $derived(new Map(resources.map((r) => [r.id, r])));

  // Sections in first-seen order, each with its resources.
  let grouped = $derived.by(() => {
    const order: string[] = [];
    const m = new Map<string, ResourceMeta[]>();
    for (const r of resources) {
      if (!m.has(r.section)) {
        m.set(r.section, []);
        order.push(r.section);
      }
      m.get(r.section)!.push(r);
    }
    return order.map((section) => ({ section, items: m.get(section)! }));
  });

  let active = $derived(byId.has($route) ? $route : '');
  let pageTitle = $derived(active === '' ? 'Overview' : (byId.get(active)?.label ?? ''));
</script>

<div class="flex h-full overflow-hidden">
  <Sidebar {grouped} {active} />

  <div class="flex min-w-0 flex-1 flex-col">
    <Topbar title={pageTitle} />

    <main class="flex-1 overflow-y-auto bg-base-200 p-6">
      {#if !loaded}
        <div class="flex justify-center py-24"><span class="loading loading-spinner loading-lg"></span></div>
      {:else if error}
        <div class="alert alert-error">Failed to load resources: {error}</div>
      {:else if active === ''}
        <Overview {grouped} />
      {:else if active === 'images'}
        {#key active}
          <ImagesPage meta={byId.get(active)!} />
        {/key}
      {:else if active === 'buckets'}
        <ObjectStoragePage />
      {:else}
        {#key active}
          <ResourcePage meta={byId.get(active)!} />
        {/key}
      {/if}
    </main>
  </div>
</div>
