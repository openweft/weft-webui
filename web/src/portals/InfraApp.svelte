<script lang="ts">
  // InfraApp is the shell mounted on the WireGuard-mesh-only :8089
  // listener. Full surface : every page the binary knows about. Same
  // import set as the legacy App.svelte ; kept as a separate shell so
  // the user + tenant bundles stay free of the infra-only pages.
  import { onMount } from 'svelte';
  import { route } from '../lib/router';
  import { getResources, type ResourceMeta } from '../lib/api';
  import Sidebar from '../lib/components/Sidebar.svelte';
  import Topbar from '../lib/components/Topbar.svelte';
  import ResourcePage from '../lib/components/ResourcePage.svelte';
  import RegistryPage from '../lib/components/RegistryPage.svelte';
  import DNSPage from '../lib/components/DNSPage.svelte';
  import SecurityPage from '../lib/components/SecurityPage.svelte';
  import NetworksPage from '../lib/components/NetworksPage.svelte';
  import TenantsPage from '../lib/components/TenantsPage.svelte';
  import ObjectStoragePage from '../lib/components/ObjectStoragePage.svelte';
  import SharesPage from '../lib/components/SharesPage.svelte';
  import ScriptsPage from '../lib/components/ScriptsPage.svelte';
  import SSHKeysPage from '../lib/components/SSHKeysPage.svelte';
  import NetworkTopology from '../lib/components/NetworkTopology.svelte';
  import PluginsPage from '../lib/components/PluginsPage.svelte';
  import FederationPage from '../lib/components/FederationPage.svelte';
  import InventoryMapPage from '../lib/components/InventoryMapPage.svelte';
  import GroupsTreePage from '../lib/components/GroupsTreePage.svelte';
  import InventoryTreePage from '../lib/components/InventoryTreePage.svelte';
  import AuditLogPage from '../lib/components/AuditLogPage.svelte';
  import DiagnosesPage from '../lib/components/DiagnosesPage.svelte';
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

  const SPECIAL = new Set(['activity', 'federation', 'diagnoses']);
  let active = $derived(byId.has($route) || SPECIAL.has($route) ? $route : '');
  let pageTitle = $derived.by(() => {
    if (active === '') return 'Overview';
    if (active === 'activity') return 'Activity';
    if (active === 'federation') return 'Federation';
    if (active === 'diagnoses') return 'Diagnoses';
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
      {:else if active === 'federation'}
        {#key active}<FederationPage />{/key}
      {:else if active === 'diagnoses'}
        {#key active}<DiagnosesPage />{/key}
      {:else if active === 'registries'}
        {#key active}<RegistryPage meta={byId.get(active)!} />{/key}
      {:else if active === 'dns'}
        {#key active}<DNSPage meta={byId.get(active)!} />{/key}
      {:else if active === 'security-groups'}
        {#key active}<SecurityPage meta={byId.get(active)!} />{/key}
      {:else if active === 'networks'}
        {#key active}<NetworksPage meta={byId.get(active)!} />{/key}
      {:else if active === 'tenants'}
        {#key active}<TenantsPage meta={byId.get(active)!} />{/key}
      {:else if active === 'buckets'}
        <ObjectStoragePage />
      {:else if active === 'shares'}
        <SharesPage />
      {:else if active === 'scripts'}
        <ScriptsPage />
      {:else if active === 'ssh-keys'}
        <SSHKeysPage />
      {:else if active === 'topology'}
        <NetworkTopology />
      {:else if active === 'plugins'}
        {#key active}<PluginsPage meta={byId.get(active)!} onChange={refreshResources} />{/key}
      {:else if active === 'inventory-map'}
        {#key active}<InventoryMapPage meta={byId.get(active)!} />{/key}
      {:else if active === 'groups'}
        {#key active}<GroupsTreePage meta={byId.get(active)!} />{/key}
      {:else if active === 'inventory-tree'}
        {#key active}<InventoryTreePage meta={byId.get(active)!} />{/key}
      {:else if active === 'audit-log'}
        {#key active}<AuditLogPage meta={byId.get(active)!} />{/key}
      {:else}
        {#key active}<ResourcePage meta={byId.get(active)!} />{/key}
      {/if}
    </main>
  </div>
</div>

<FailoverBanner />
<EventToasts />
<SearchPalette />
