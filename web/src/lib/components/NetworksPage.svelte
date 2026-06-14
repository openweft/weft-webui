<script lang="ts">
  // NetworksPage — unified master-detail view of networks + their
  // subnets. Same layout as DNSPage / SecurityPage :
  //
  //   Master (left)  : network list with N/E/D for the network.
  //   Detail (right) : subnets of the selected network with N/E/D
  //     for one subnet.
  //
  // Replaces the ResourcePage routing for 'networks' (the old
  // NetworkDrawer flow). Edit on either side opens its own modal.
  import {
    getRowsPage, getMe, deleteNetwork,
    listSubnets, deleteSubnet,
    isEnabled,
    type ResourceMeta, type Row, type Me, type Subnet,
  } from '../api';
  import CreateNetworkModal from './CreateNetworkModal.svelte';
  import EditNetworkModal from './EditNetworkModal.svelte';
  import EditSubnetModal from './EditSubnetModal.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));
  $effect(() => { getMe().then((u) => (me = u)).catch(() => {/* api.ts handled */}); });

  // ---- networks (master) ----
  let networks = $state<Row[]>([]);
  let networksLoading = $state(true);
  let networksErr = $state('');
  let networkQuery = $state('');
  let selectedNetworkKey = $state('');

  function networkKey(n: Row): string { return String(n.name ?? n.uuid ?? ''); }

  function refreshNetworks() {
    networksLoading = true; networksErr = '';
    getRowsPage('networks', { limit: 200 })
      .then((p) => {
        networks = p.rows;
        if (selectedNetworkKey && !p.rows.find((n) => networkKey(n) === selectedNetworkKey)) {
          selectedNetworkKey = p.rows.length > 0 ? networkKey(p.rows[0]) : '';
        } else if (!selectedNetworkKey && p.rows.length > 0) {
          selectedNetworkKey = networkKey(p.rows[0]);
        }
      })
      .catch((e) => (networksErr = String(e)))
      .finally(() => (networksLoading = false));
  }
  $effect(refreshNetworks);

  let filteredNetworks = $derived.by(() => {
    const q = networkQuery.trim().toLowerCase();
    if (!q) return networks;
    return networks.filter((n) =>
      String(n.name).toLowerCase().includes(q)
      || String(n.cidr ?? '').toLowerCase().includes(q)
      || String(n.type ?? '').toLowerCase().includes(q),
    );
  });

  let selectedNetwork = $derived<Row | null>(
    networks.find((n) => networkKey(n) === selectedNetworkKey) ?? null,
  );

  function clickNetwork(n: Row) {
    const id = networkKey(n);
    selectedNetworkKey = selectedNetworkKey === id ? '' : id;
    networkActionErr = '';
  }

  let networkCreateOpen = $state(false);
  let networkEditOpen = $state(false);
  let networkActionBusy = $state(false);
  let networkActionErr = $state('');

  async function delSelectedNetwork() {
    if (!selectedNetwork) return;
    if (!confirm(`Delete network "${selectedNetwork.name}" ? Attached instances will lose connectivity on the next reconcile.`)) return;
    networkActionBusy = true; networkActionErr = '';
    try {
      await deleteNetwork(String(selectedNetwork.uuid));
      selectedNetworkKey = '';
      refreshNetworks();
    } catch (e) {
      networkActionErr = String(e);
    } finally {
      networkActionBusy = false;
    }
  }

  // ---- subnets (detail, scoped to the selected network) ----
  let subnets = $state<Subnet[]>([]);
  let subnetsLoading = $state(false);
  let subnetsErr = $state('');
  let subnetQuery = $state('');
  let selectedSubnetUUID = $state('');
  let subnetActionBusy = $state(false);
  let subnetActionErr = $state('');

  async function refreshSubnets() {
    if (!selectedNetworkKey) { subnets = []; selectedSubnetUUID = ''; return; }
    subnetsLoading = true; subnetsErr = '';
    try {
      subnets = await listSubnets(selectedNetworkKey);
      if (selectedSubnetUUID && !subnets.find((s) => s.uuid === selectedSubnetUUID)) {
        selectedSubnetUUID = '';
      }
    } catch (e) {
      subnetsErr = String(e);
    } finally {
      subnetsLoading = false;
    }
  }
  $effect(() => { selectedNetworkKey; selectedSubnetUUID = ''; refreshSubnets(); });

  let filteredSubnets = $derived.by(() => {
    const q = subnetQuery.trim().toLowerCase();
    if (!q) return subnets;
    return subnets.filter((s) =>
      s.name.toLowerCase().includes(q)
      || s.cidr.toLowerCase().includes(q)
      || (s.gateway ?? '').toLowerCase().includes(q),
    );
  });

  let selectedSubnet = $derived<Subnet | null>(
    subnets.find((s) => s.uuid === selectedSubnetUUID) ?? null,
  );

  function clickSubnet(s: Subnet) {
    selectedSubnetUUID = selectedSubnetUUID === s.uuid ? '' : s.uuid;
    subnetActionErr = '';
  }

  let subnetCreateOpen = $state(false);
  let subnetEditOpen = $state(false);

  async function delSelectedSubnet() {
    if (!selectedSubnet) return;
    if (!confirm(`Delete subnet "${selectedSubnet.name}" (${selectedSubnet.cidr}) ?`)) return;
    subnetActionBusy = true; subnetActionErr = '';
    try {
      await deleteSubnet(selectedNetworkKey, selectedSubnet.uuid);
      selectedSubnetUUID = '';
      refreshSubnets();
    } catch (e) {
      subnetActionErr = String(e);
    } finally {
      subnetActionBusy = false;
    }
  }

  function typeBadge(t: unknown): string {
    switch (String(t).toLowerCase()) {
      case 'wireguard': return 'badge-info';
      case 'overlay':   return 'badge-success';
      case 'underlay':  return 'badge-warning';
      default:          return 'badge-ghost';
    }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      Networks and their subnets · pick a network on the left to edit the subnets attached to it on the right.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Networks master -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Networks</h3>
      {#if networksLoading}<span class="loading loading-spinner loading-xs"></span>{/if}
      <span class="ml-auto text-xs text-base-content/50">{filteredNetworks.length} of {networks.length}</span>
    </div>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter networks…" bind:value={networkQuery} />
    </label>

    {#if canEdit}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-sm btn-primary gap-1" onclick={() => (networkCreateOpen = true)}
          title="Create a new network">
          <span class="text-base leading-none">+</span> New
        </button>
        <button class="btn btn-sm btn-warning gap-1"
          disabled={!selectedNetwork || networkActionBusy}
          onclick={() => (networkEditOpen = true)}
          title={selectedNetwork ? `Edit "${selectedNetwork.name}"` : 'Select a network to edit'}>
          Edit
        </button>
        <button class="btn btn-sm btn-error gap-1"
          disabled={!selectedNetwork || networkActionBusy}
          onclick={delSelectedNetwork}
          title={selectedNetwork ? `Delete "${selectedNetwork.name}"` : 'Select a network to delete'}>
          {#if networkActionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Delete
        </button>
      </div>
    {/if}

    {#if networksErr}<div class="alert alert-error py-2 text-sm">{networksErr}</div>{/if}
    {#if networkActionErr}<div class="alert alert-error py-2 text-sm">{networkActionErr}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filteredNetworks as n (networkKey(n))}
        <li>
          <button class:menu-active={selectedNetworkKey === networkKey(n)}
            onclick={() => clickNetwork(n)}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <circle cx="12" cy="12" r="3" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3M5 5l2 2M17 17l2 2M5 19l2-2M17 7l2-2" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-2">
                <span class="truncate font-medium">{n.name}</span>
                <span class="badge badge-xs {typeBadge(n.type)}">{n.type}</span>
              </div>
              <div class="text-[10px] text-base-content/50 font-mono">
                {n.cidr} · gw {n.gateway}
              </div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {networks.length === 0 ? 'No networks yet.' : 'No networks match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <!-- Subnets detail -->
  <section class="min-w-0 flex-1 flex flex-col gap-2">
    {#if !selectedNetwork}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a network to see its subnets.
      </div>
    {:else}
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">
            Subnets in <span class="font-mono normal-case text-base-content">{selectedNetwork.name}</span>
          </h3>
          <p class="text-xs text-base-content/50">
            {subnets.length} subnets · parent CIDR <span class="font-mono">{selectedNetwork.cidr}</span>
          </p>
        </div>
        <div class="ml-auto flex items-center gap-2">
          <label class="input input-sm input-bordered flex items-center gap-2">
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
            </svg>
            <input type="search" class="grow" placeholder="Filter subnets…" bind:value={subnetQuery} />
          </label>
          {#if canEdit}
            <button class="btn btn-sm btn-primary gap-1" onclick={() => (subnetCreateOpen = true)}
              title="Add a subnet to {selectedNetwork.name}">
              <span class="text-base leading-none">+</span> New
            </button>
            <button class="btn btn-sm btn-warning gap-1"
              disabled={!selectedSubnet || subnetActionBusy}
              onclick={() => (subnetEditOpen = true)}
              title={selectedSubnet ? `Edit "${selectedSubnet.name}"` : 'Select a subnet to edit'}>
              Edit
            </button>
            <button class="btn btn-sm btn-error gap-1"
              disabled={!selectedSubnet || subnetActionBusy}
              onclick={delSelectedSubnet}
              title={selectedSubnet ? `Delete "${selectedSubnet.name}"` : 'Select a subnet to delete'}>
              {#if subnetActionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              Delete
            </button>
          {/if}
        </div>
      </div>

      {#if subnetsErr}<div class="alert alert-error py-2 text-sm">{subnetsErr}</div>{/if}
      {#if subnetActionErr}<div class="alert alert-error py-2 text-sm">{subnetActionErr}</div>{/if}

      {#if String(selectedNetwork.type ?? '') === 'bridged'}
        <div class="rounded-box border border-info/30 bg-info/5 p-3">
          <div class="flex items-center gap-2">
            <span class="badge badge-info badge-sm">DHCPv4</span>
            <span class="text-xs font-semibold uppercase text-base-content/70">Lease server</span>
            <span class="ml-auto text-xs text-base-content/50">Auto-managed by weft-agent on the host bridge</span>
          </div>
          <div class="mt-2 grid gap-2 text-xs sm:grid-cols-2">
            <div>
              <span class="text-base-content/60">CIDR </span>
              <span class="font-mono">{selectedNetwork.cidr ?? '—'}</span>
            </div>
            <div>
              <span class="text-base-content/60">Gateway </span>
              <span class="font-mono">{selectedNetwork.gateway || '—'}</span>
            </div>
            <div>
              <span class="text-base-content/60">DNS servers </span>
              <span class="font-mono">
                {#if Array.isArray(selectedNetwork.dns_servers) && selectedNetwork.dns_servers.length > 0}
                  {(selectedNetwork.dns_servers as string[]).join(', ')}
                {:else}
                  none configured
                {/if}
              </span>
            </div>
            <div>
              <span class="text-base-content/60">Lease range </span>
              <span class="font-mono text-base-content/80">
                derived from CIDR (skip network + gateway + broadcast)
              </span>
            </div>
          </div>
          <p class="mt-2 text-xs text-base-content/50">
            VMs joining this network receive their IP / gateway / DNS
            automatically via DHCPv4 option 1/3/6/54. The
            <span class="font-mono">weft-firstboot</span> agent in the
            guest still consumes static cloud-init data when present.
          </p>
        </div>
      {/if}

      <div class="rounded-box border border-base-300 bg-base-100">
        <table class="table table-sm">
          <thead>
            <tr>
              <th>Name</th>
              <th>CIDR</th>
              <th>Gateway</th>
              <th>Enabled</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {#if subnetsLoading}
              <tr><td colspan="5" class="py-8 text-center">
                <span class="loading loading-spinner"></span>
              </td></tr>
            {:else if filteredSubnets.length === 0}
              <tr><td colspan="5" class="py-8 text-center text-base-content/50">
                {subnets.length === 0
                  ? 'No subnets in this network. Add one with "+ New".'
                  : 'No subnets match the filter.'}
              </td></tr>
            {:else}
              {#each filteredSubnets as s (s.uuid)}
                <tr class="hover cursor-pointer"
                  class:bg-primary={selectedSubnetUUID === s.uuid}
                  class:text-primary-content={selectedSubnetUUID === s.uuid}
                  class:opacity-50={!isEnabled(s)}
                  onclick={() => clickSubnet(s)}>
                  <td class="font-mono">{s.name}</td>
                  <td class="font-mono text-xs">{s.cidr}</td>
                  <td class="font-mono text-xs">{s.gateway || '—'}</td>
                  <td>
                    {#if isEnabled(s)}
                      <span class="badge badge-sm badge-success">on</span>
                    {:else}
                      <span class="badge badge-sm badge-ghost">off</span>
                    {/if}
                  </td>
                  <td class="text-xs text-base-content/70">{(s.updated_at ?? '').slice(0, 10) || '—'}</td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<CreateNetworkModal bind:open={networkCreateOpen} onCreated={refreshNetworks} />
<EditNetworkModal bind:open={networkEditOpen} network={selectedNetwork} onSaved={refreshNetworks} />

{#if selectedNetwork}
  <EditSubnetModal
    bind:open={subnetCreateOpen}
    networkKey={selectedNetworkKey}
    subnet={null}
    mode="create"
    onSaved={refreshSubnets}
  />
  <EditSubnetModal
    bind:open={subnetEditOpen}
    networkKey={selectedNetworkKey}
    subnet={selectedSubnet}
    mode="edit"
    onSaved={refreshSubnets}
  />
{/if}
