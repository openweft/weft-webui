<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import {
    getRowsPage, type ResourceMeta, type Row,
    deleteVM, deleteVolume, deleteNetwork, deleteSchedulingRule,
    deleteSecurityGroup, releaseFloatingIP, deleteRouter,
    deleteLoadBalancer, deleteDNSZone, deleteDNSRecord,
  } from '../api';
  import { lastEvents, eventToResource } from '../events';
  import { routeParams, go } from '../router';
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
  import LoadBalancerDrawer from './LoadBalancerDrawer.svelte';
  import VolumeDrawer from './VolumeDrawer.svelte';
  import EditableDrawer from './EditableDrawer.svelte';
  import FloatingIPDrawer from './FloatingIPDrawer.svelte';
  import SchedulingRuleDrawer from './SchedulingRuleDrawer.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  let rows = $state<Row[]>([]);
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state('');
  let query = $state('');
  // Pagination state. nextToken is the opaque cursor handed back by
  // the server ; total is the server-side row count (post-scope-filter)
  // so the header can show "X loaded of Y". One page is 50 rows ;
  // operators bump it via the "Load more" button at the bottom.
  let nextToken = $state('');
  let total = $state(0);
  const pageSize = 50;

  function refresh() {
    const id = meta.id;
    loading = true;
    error = '';
    nextToken = '';
    getRowsPage(id, { limit: pageSize })
      .then((p) => { rows = p.rows; nextToken = p.next; total = p.total; })
      .catch((e) => (error = String(e)))
      .finally(() => (loading = false));
  }

  // Append the next slice. The button hides itself once nextToken is
  // empty (handler also no-ops in that case to be extra defensive).
  async function loadMore() {
    if (!nextToken || loadingMore) return;
    loadingMore = true;
    try {
      const p = await getRowsPage(meta.id, { limit: pageSize, pageToken: nextToken });
      rows = [...rows, ...p.rows];
      nextToken = p.next;
      total = p.total;
    } catch (e) {
      error = String(e);
    } finally {
      loadingMore = false;
    }
  }

  // Auto-refresh on live events that target the currently rendered
  // resource. Debounced 400ms so a burst of events (e.g. start-vm
  // emits 3-4 phase transitions) collapses into one refetch.
  let refreshTimer: ReturnType<typeof setTimeout> | null = null;
  let lastSeen = 0;
  let unsubscribe: () => void;
  function scheduleRefresh() {
    if (refreshTimer) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      refresh();
    }, 400);
  }
  onMount(() => {
    lastSeen = 0; // start fresh per mount
    unsubscribe = lastEvents.subscribe((all) => {
      // New events arrived at index 0 ; everything from 0..(len-lastSeen)
      // is new since our last check.
      const newCount = all.length - lastSeen;
      lastSeen = all.length;
      for (let i = 0; i < newCount; i++) {
        if (eventToResource(all[i].kind) === meta.id) {
          scheduleRefresh();
          return; // one trigger per burst is enough
        }
      }
    });
  });
  onDestroy(() => {
    unsubscribe?.();
    if (refreshTimer) clearTimeout(refreshTimer);
  });

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
  // drawerRow : the row whose drawer is currently open. Decoupled
  // from selectedRow so closing the drawer doesn't deselect — the
  // header Edit / Delete keep operating on the last-clicked row.
  let drawerRow = $state<Row | null>(null);
  // Header-button busy + error states.
  let actionBusy = $state(false);
  let actionErr = $state('');
  // selectableDrawer : resources that have a detail drawer. Click on
  // a row in this set highlights it (selectedRow). The drawer opens
  // exclusively via the header Edit button — no row-click shortcut.
  // networks + security-groups + dns are handled by dedicated custom
  // pages (NetworksPage / SecurityPage / DNSPage) — they don't pass
  // through ResourcePage at all, so they're excluded here.
  const selectableDrawer = [
    'microvms', 'loadbalancers', 'volumes',
    'routers', 'floating-ips', 'scheduling-rules',
  ];
  function handleSelect(row: Row) {
    // Toggle : a second click on the same row deselects, matching
    // SSHKeysPage. Makes the "no selection" state reachable without
    // clicking outside the table.
    if (selectedRow && rowKey(selectedRow) === rowKey(row)) {
      selectedRow = null;
    } else {
      selectedRow = row;
    }
    actionErr = '';
  }
  function rowKey(r: Row): string {
    return String(r.uuid ?? r.name ?? '');
  }
  let selectedKey = $derived(selectedRow ? rowKey(selectedRow) : '');

  // Deep-link : if the hash carries ?detail=<key>, try to open the
  // drawer for the row whose uuid OR name matches. The palette uses
  // this to jump straight from search results into a detail view.
  // Reactive so the same page can re-open a different drawer when
  // the query param changes.
  $effect(() => {
    const key = $routeParams.detail;
    if (!key) {
      drawerRow = null;
      return;
    }
    if (!selectableDrawer.includes(meta.id)) return;
    const hit = rows.find((r) => r.uuid === key || r.name === key);
    if (hit) {
      drawerRow = hit;
      selectedRow = hit;
    }
  });

  function closeDrawer() {
    drawerRow = null;
    // Drop ?detail= from the URL so a refresh / back-button doesn't
    // pop it open again.
    if ($routeParams.detail) go(meta.id);
  }

  // Header Edit button — only meaningful for resources with a drawer
  // (the drawer IS the editor). Re-opens the drawer for the selected
  // row.
  let canEdit = $derived(selectableDrawer.includes(meta.id));
  function startEdit() {
    if (!selectedRow) return;
    if (!canEdit) return;
    drawerRow = selectedRow;
  }

  // Header Delete — dispatches the per-resource delete RPC. Mirrors
  // the per-row dropdown's runAction but lives at the page level so
  // the button can sit next to + New / Edit.
  async function startDelete() {
    if (!selectedRow) return;
    const r = selectedRow;
    const name = String(r.name ?? r.uuid ?? '');
    const uuid = String(r.uuid ?? '');
    let confirmMsg = `Delete ${meta.label.replace(/s$/, '')} "${name}" ?`;
    if (meta.id === 'floating-ips') confirmMsg = `Release ${r.address} ?`;
    if (meta.id === 'dns-zones') confirmMsg += ' Removes every record inside it.';
    if (!confirm(confirmMsg)) return;
    actionBusy = true; actionErr = '';
    try {
      switch (meta.id) {
        case 'microvms':         await deleteVM(name); break;
        case 'volumes':          await deleteVolume(uuid); break;
        case 'networks':         await deleteNetwork(uuid); break;
        case 'scheduling-rules': await deleteSchedulingRule(name); break;
        case 'security-groups':  await deleteSecurityGroup(uuid); break;
        case 'floating-ips':     await releaseFloatingIP(uuid); break;
        case 'routers':          await deleteRouter(uuid); break;
        case 'loadbalancers':    await deleteLoadBalancer(uuid); break;
        case 'dns-zones':        await deleteDNSZone(uuid); break;
        case 'dns-records':      await deleteDNSRecord(uuid); break;
        default:
          actionErr = `Delete not wired for ${meta.id}`;
          return;
      }
      selectedRow = null;
      if (drawerRow && rowKey(drawerRow) === rowKey(r)) drawerRow = null;
      refresh();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
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

  // deleteBlockedReason : per-resource preconditions that gate the
  // header Delete button BEFORE the server gets the request. Today
  // only volumes need it (attached → detach first). Empty string means
  // delete is allowed ; non-empty surfaces as the button's title tooltip.
  // Server-side enforcement stays in place ; this is a UX guard.
  let deleteBlockedReason = $derived.by<string>(() => {
    if (!selectedRow) return '';
    if (meta.id === 'volumes') {
      const attached = typeof selectedRow.attached_to === 'string' ? selectedRow.attached_to : '';
      if (attached) {
        return `Volume is attached to "${attached}". Detach it first (Attach/Detach action) before deleting.`;
      }
    }
    return '';
  });

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
      {filtered.length} of {rows.length}{total > rows.length ? ` (${total} total)` : ''}
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
      <span class="text-base leading-none">+</span> New
    </button>
    {#if canEdit}
      <button class="btn btn-sm btn-warning gap-1"
        disabled={!selectedRow || actionBusy}
        onclick={startEdit}
        title={selectedRow ? `Edit "${selectedRow.name}"` : 'Select a row to edit'}>
        Edit
      </button>
    {/if}
    <button class="btn btn-sm btn-error gap-1"
      disabled={!selectedRow || !canCreate || actionBusy || !!deleteBlockedReason}
      onclick={startDelete}
      title={deleteBlockedReason
        ? deleteBlockedReason
        : selectedRow ? `Delete "${selectedRow.name}"` : 'Select a row to delete'}>
      {#if actionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
      Delete
    </button>
  </div>
</div>

{#if actionErr}
  <div class="mt-2 alert alert-error text-sm">{actionErr}</div>
{/if}

<div class="mt-4">
  {#if loading}
    <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
  {:else if error}
    <div class="alert alert-error">{error}</div>
  {:else}
    <ResourceTable columns={meta.columns} rows={filtered}
      onSelect={handleSelect}
      selectedKey={selectedKey}
      onReload={refresh} />
    {#if nextToken}
      <div class="mt-3 flex items-center justify-center">
        <button
          class="btn btn-sm btn-ghost gap-2"
          disabled={loadingMore}
          onclick={loadMore}
        >
          {#if loadingMore}<span class="loading loading-spinner loading-xs"></span>{/if}
          Load more
          <span class="text-xs text-base-content/50">
            ({rows.length} / {total})
          </span>
        </button>
      </div>
    {/if}
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

{#if meta.id === 'microvms' && drawerRow}
  <MicroVMDrawer
    row={drawerRow}
    onClose={closeDrawer}
    onChanged={refresh}
  />
{:else if meta.id === 'security-groups' && drawerRow}
  <SecurityGroupDrawer
    row={drawerRow}
    onClose={closeDrawer}
    onChanged={refresh}
  />
{:else if meta.id === 'loadbalancers' && drawerRow}
  <LoadBalancerDrawer
    row={drawerRow}
    onClose={closeDrawer}
    onChanged={refresh}
  />
{:else if meta.id === 'volumes' && drawerRow}
  <VolumeDrawer
    row={drawerRow}
    onClose={closeDrawer}
    onChanged={refresh}
  />
{:else if meta.id === 'routers' && drawerRow}
  <EditableDrawer
    resource="routers"
    row={drawerRow}
    title={String(drawerRow.name)}
    subtitle={`${drawerRow.backend ?? '—'} · ${drawerRow.mode ?? '—'} · project ${drawerRow.project ?? '—'}`}
    onClose={closeDrawer}
    onChanged={refresh}
  />
{:else if meta.id === 'floating-ips' && drawerRow}
  <FloatingIPDrawer
    row={drawerRow}
    onClose={closeDrawer}
    onChanged={refresh}
    onMapRequest={(uuid, address) => { mapFipTarget = { uuid, address }; }}
  />
{:else if meta.id === 'scheduling-rules' && drawerRow}
  <SchedulingRuleDrawer
    row={drawerRow}
    onClose={closeDrawer}
    onChanged={refresh}
  />
{/if}
