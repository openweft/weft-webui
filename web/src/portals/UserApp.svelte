<script lang="ts">
  // UserApp is the shell mounted on the public :8080 listener.
  // STATICALLY imports only the pages a regular user needs.
  // Tree-shaking strips everything else from the user bundle.
  //
  // Allowed pages : Overview, Activity, ResourcePage (generic),
  //                 ObjectStorage, Shares, SSHKeys, Networks (read-only),
  //                 Tenants (own membership view).
  // Forbidden : Federation, Plugins, Inventory*, AuditLog, NetworkTopology,
  //             DNS, SecurityGroups, Registry, Scripts (admin catalogue).
  //
  // Mirrors the server-side scope filter — endpoints behind those
  // pages aren't even registered on :8080 (see internal/server/api.go).
  import { onMount } from 'svelte';
  import { route } from '../lib/router';
  import { getResources, type ResourceMeta } from '../lib/api';
  import Sidebar from '../lib/components/Sidebar.svelte';
  import Topbar from '../lib/components/Topbar.svelte';
  import ResourcePage from '../lib/components/ResourcePage.svelte';
  import NetworksPage from '../lib/components/NetworksPage.svelte';
  import TenantsPage from '../lib/components/TenantsPage.svelte';
  import ObjectStoragePage from '../lib/components/ObjectStoragePage.svelte';
  import SharesPage from '../lib/components/SharesPage.svelte';
  import SSHKeysPage from '../lib/components/SSHKeysPage.svelte';
  import Overview from '../lib/components/Overview.svelte';
  import ActivityPage from '../lib/components/ActivityPage.svelte';
  import EventToasts from '../lib/components/EventToasts.svelte';
  import FailoverBanner from '../lib/components/FailoverBanner.svelte';
  import SearchPalette from '../lib/components/SearchPalette.svelte';

  let resources = $state<ResourceMeta[]>([]);
  let loaded = $state(false);
  let error = $state('');

  async function refreshResources() {
    try {
      resources = await getResources();
      error = '';
    } catch (e) {
      error = String(e);
    } finally {
      loaded = true;
    }
  }
  onMount(refreshResources);

  let byId = $derived(new Map(resources.map((r) => [r.id, r])));

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

  const SPECIAL = new Set(['activity']);
  let active = $derived(byId.has($route) || SPECIAL.has($route) ? $route : '');
  let pageTitle = $derived.by(() => {
    if (active === '') return 'Overview';
    if (active === 'activity') return 'Activity';
    return byId.get(active)?.label ?? '';
  });
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
      {:else if active === 'activity'}
        <ActivityPage />
      {:else if active === 'networks'}
        {#key active}<NetworksPage meta={byId.get(active)!} />{/key}
      {:else if active === 'tenants'}
        {#key active}<TenantsPage meta={byId.get(active)!} />{/key}
      {:else if active === 'buckets'}
        <ObjectStoragePage />
      {:else if active === 'shares'}
        <SharesPage />
      {:else if active === 'ssh-keys'}
        <SSHKeysPage />
      {:else}
        {#key active}<ResourcePage meta={byId.get(active)!} />{/key}
      {/if}
    </main>
  </div>
</div>

<FailoverBanner />
<EventToasts />
<SearchPalette />
