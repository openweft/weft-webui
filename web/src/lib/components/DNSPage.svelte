<script lang="ts">
  // DNSPage — unified view of DNS zones (left list) + the records of
  // the selected zone (right table). Replaces the two separate
  // "DNS Zones" / "DNS Records" sidebar entries with a single "DNS"
  // entry that bundles them.
  //
  // Selection model mirrors SSHKeysPage : click a zone to highlight,
  // header New / Edit / Delete act on the highlighted zone. Records
  // get their own header buttons, scoped to the selected zone.
  // Drawers open only via the Edit button — no row-click shortcut.
  import {
    getRowsPage, deleteDNSZone, deleteDNSRecord, getMe,
    type ResourceMeta, type Row, type Me,
  } from '../api';
  import CreateDNSZoneModal from './CreateDNSZoneModal.svelte';
  import CreateDNSRecordModal from './CreateDNSRecordModal.svelte';
  import EditDNSZoneModal from './EditDNSZoneModal.svelte';
  import EditDNSRecordModal from './EditDNSRecordModal.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));
  $effect(() => { getMe().then((u) => (me = u)).catch(() => {/* api.ts handled */}); });

  // ---- zones (master) ----
  let zones = $state<Row[]>([]);
  let zonesLoading = $state(true);
  let zonesErr = $state('');
  let zoneQuery = $state('');
  let selectedZoneName = $state('');

  function refreshZones() {
    zonesLoading = true; zonesErr = '';
    getRowsPage('dns-zones', { limit: 200 })
      .then((p) => {
        zones = p.rows;
        // Re-select : if the prior selection is still there, keep it.
        // Otherwise fall back to the first zone.
        if (selectedZoneName && !p.rows.find((z) => z.name === selectedZoneName)) {
          selectedZoneName = String(p.rows[0]?.name ?? '');
        } else if (!selectedZoneName && p.rows.length > 0) {
          selectedZoneName = String(p.rows[0].name);
        }
      })
      .catch((e) => (zonesErr = String(e)))
      .finally(() => (zonesLoading = false));
  }
  $effect(refreshZones);

  let filteredZones = $derived.by(() => {
    const q = zoneQuery.trim().toLowerCase();
    if (!q) return zones;
    return zones.filter((z) =>
      String(z.name).toLowerCase().includes(q)
      || String(z.role ?? '').toLowerCase().includes(q)
      || String(z.backend ?? '').toLowerCase().includes(q),
    );
  });

  let selectedZone = $derived<Row | null>(
    zones.find((z) => z.name === selectedZoneName) ?? null,
  );

  function clickZone(z: Row) {
    selectedZoneName = selectedZoneName === z.name ? '' : String(z.name);
    zoneActionErr = '';
  }

  // ---- zone actions ----
  let zoneCreateOpen = $state(false);
  let zoneEditOpen = $state(false);
  let zoneActionBusy = $state(false);
  let zoneActionErr = $state('');

  async function delSelectedZone() {
    if (!selectedZone) return;
    if (!confirm(`Delete DNS zone "${selectedZone.name}" ? Removes every record inside it.`)) return;
    zoneActionBusy = true; zoneActionErr = '';
    try {
      await deleteDNSZone(String(selectedZone.uuid));
      selectedZoneName = '';
      refreshZones();
      refreshRecords();
    } catch (e) {
      zoneActionErr = String(e);
    } finally {
      zoneActionBusy = false;
    }
  }

  // ---- records (detail, scoped to selectedZone) ----
  let allRecords = $state<Row[]>([]);
  let recordsLoading = $state(true);
  let recordsErr = $state('');
  let recordQuery = $state('');
  let selectedRecordKey = $state('');
  let recordActionBusy = $state(false);
  let recordActionErr = $state('');

  function refreshRecords() {
    recordsLoading = true; recordsErr = '';
    getRowsPage('dns-records', { limit: 1000 })
      .then((p) => (allRecords = p.rows))
      .catch((e) => (recordsErr = String(e)))
      .finally(() => (recordsLoading = false));
  }
  $effect(refreshRecords);

  let zoneRecords = $derived.by(() => {
    if (!selectedZoneName) return [];
    const q = recordQuery.trim().toLowerCase();
    return allRecords.filter((r) => {
      if (r.zone !== selectedZoneName) return false;
      if (!q) return true;
      return Object.values(r).some((v) => String(v).toLowerCase().includes(q));
    });
  });

  function recordKey(r: Row): string {
    return String(r.uuid ?? `${r.zone}/${r.name}/${r.type}/${r.value}`);
  }
  let selectedRecord = $derived<Row | null>(
    zoneRecords.find((r) => recordKey(r) === selectedRecordKey) ?? null,
  );
  function clickRecord(r: Row) {
    const k = recordKey(r);
    selectedRecordKey = selectedRecordKey === k ? '' : k;
    recordActionErr = '';
  }

  // Reset record selection whenever the active zone changes.
  $effect(() => { selectedZoneName; selectedRecordKey = ''; });

  // ---- record actions ----
  let recordCreateOpen = $state(false);
  let recordEditOpen = $state(false);

  async function delSelectedRecord() {
    if (!selectedRecord) return;
    if (!confirm(`Delete record ${selectedRecord.name}.${selectedRecord.zone} ?`)) return;
    recordActionBusy = true; recordActionErr = '';
    try {
      await deleteDNSRecord(String(selectedRecord.uuid));
      selectedRecordKey = '';
      refreshRecords();
    } catch (e) {
      recordActionErr = String(e);
    } finally {
      recordActionBusy = false;
    }
  }

  function statusBadge(v: unknown): string {
    switch (String(v).toLowerCase()) {
      case 'active': return 'badge-success';
      case 'provisioning':
      case 'pushing': return 'badge-warning';
      case 'failed': return 'badge-error';
      default: return 'badge-ghost';
    }
  }
  function sourceBadge(v: unknown): string {
    switch (String(v).toLowerCase()) {
      case 'auto': return 'badge-info';
      case 'static': return 'badge-ghost';
      default: return 'badge-ghost';
    }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      Zones and records · pick a zone on the left to edit its records on the right.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Zones master pane -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Zones</h3>
      {#if zonesLoading}<span class="loading loading-spinner loading-xs"></span>{/if}
      <span class="ml-auto text-xs text-base-content/50">{filteredZones.length} of {zones.length}</span>
    </div>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter zones…" bind:value={zoneQuery} />
    </label>

    {#if canEdit}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-sm btn-primary gap-1" onclick={() => (zoneCreateOpen = true)}
          title="Create a new zone">
          <span class="text-base leading-none">+</span> New
        </button>
        <button class="btn btn-sm btn-warning gap-1"
          disabled={!selectedZone || zoneActionBusy}
          onclick={() => (zoneEditOpen = true)}
          title={selectedZone ? `Edit "${selectedZone.name}"` : 'Select a zone to edit'}>
          Edit
        </button>
        <button class="btn btn-sm btn-error gap-1"
          disabled={!selectedZone || zoneActionBusy}
          onclick={delSelectedZone}
          title={selectedZone ? `Delete "${selectedZone.name}"` : 'Select a zone to delete'}>
          {#if zoneActionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Delete
        </button>
      </div>
    {/if}

    {#if zonesErr}<div class="alert alert-error py-2 text-sm">{zonesErr}</div>{/if}
    {#if zoneActionErr}<div class="alert alert-error py-2 text-sm">{zoneActionErr}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filteredZones as z (z.name)}
        <li>
          <button class:menu-active={selectedZoneName === z.name}
            onclick={() => clickZone(z)}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-2">
                <span class="truncate font-medium">{z.name}</span>
                <span class="badge badge-xs badge-ghost">{z.role}</span>
                {#if z.enabled === false}<span class="badge badge-xs badge-ghost">disabled</span>{/if}
              </div>
              <div class="text-[10px] text-base-content/50">
                {z.backend} · {z.records} records · TTL {z.ttl_default}s
              </div>
            </div>
            {#if z.status}
              <span class="badge badge-xs {statusBadge(z.status)}">{z.status}</span>
            {/if}
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {zones.length === 0 ? 'No zones yet.' : 'No zones match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <!-- Records detail pane -->
  <section class="min-w-0 flex-1 flex flex-col gap-2">
    {#if !selectedZone}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a zone to see its records.
      </div>
    {:else}
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">
            Records in <span class="font-mono normal-case text-base-content">{selectedZone.name}</span>
          </h3>
          <p class="text-xs text-base-content/50">
            {zoneRecords.length} records · push target {selectedZone.push_target || '—'}
            · {selectedZone.push_state || '—'}
          </p>
        </div>
        <div class="ml-auto flex items-center gap-2">
          <label class="input input-sm input-bordered flex items-center gap-2">
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
            </svg>
            <input type="search" class="grow" placeholder="Filter…" bind:value={recordQuery} />
          </label>
          {#if canEdit}
            <button class="btn btn-sm btn-primary gap-1" onclick={() => (recordCreateOpen = true)}
              title="Create a record in {selectedZone.name}">
              <span class="text-base leading-none">+</span> New
            </button>
            <button class="btn btn-sm btn-warning gap-1"
              disabled={!selectedRecord || recordActionBusy}
              onclick={() => (recordEditOpen = true)}
              title={selectedRecord ? `Edit ${selectedRecord.name}.${selectedRecord.zone}` : 'Select a record to edit'}>
              Edit
            </button>
            <button class="btn btn-sm btn-error gap-1"
              disabled={!selectedRecord || recordActionBusy}
              onclick={delSelectedRecord}
              title={selectedRecord ? `Delete ${selectedRecord.name}.${selectedRecord.zone}` : 'Select a record to delete'}>
              {#if recordActionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              Delete
            </button>
          {/if}
        </div>
      </div>

      {#if recordsErr}<div class="alert alert-error py-2 text-sm">{recordsErr}</div>{/if}
      {#if recordActionErr}<div class="alert alert-error py-2 text-sm">{recordActionErr}</div>{/if}

      <div class="rounded-box border border-base-300 bg-base-100">
        <table class="table table-sm">
          <thead>
            <tr>
              <th>Name</th>
              <th>Type</th>
              <th>Value</th>
              <th>TTL</th>
              <th>Source</th>
              <th>Enabled</th>
            </tr>
          </thead>
          <tbody>
            {#if recordsLoading}
              <tr><td colspan="6" class="py-8 text-center">
                <span class="loading loading-spinner"></span>
              </td></tr>
            {:else if zoneRecords.length === 0}
              <tr><td colspan="6" class="py-8 text-center text-base-content/50">
                {allRecords.filter((r) => r.zone === selectedZone?.name).length === 0
                  ? 'No records in this zone. Create one with "+ New".'
                  : 'No records match the filter.'}
              </td></tr>
            {:else}
              {#each zoneRecords as r (recordKey(r))}
                <tr class="hover cursor-pointer"
                  class:bg-primary={selectedRecordKey === recordKey(r)}
                  class:text-primary-content={selectedRecordKey === recordKey(r)}
                  class:opacity-50={r.enabled === false}
                  onclick={() => clickRecord(r)}>
                  <td class="font-mono">{r.name}</td>
                  <td><span class="badge badge-sm badge-ghost">{r.type}</span></td>
                  <td class="font-mono text-xs">{r.value}</td>
                  <td class="text-xs text-base-content/70">{r.ttl}s</td>
                  <td><span class="badge badge-sm {sourceBadge(r.source)}">{r.source}</span></td>
                  <td>
                    {#if r.enabled === false}
                      <span class="badge badge-sm badge-ghost">off</span>
                    {:else}
                      <span class="badge badge-sm badge-success">on</span>
                    {/if}
                  </td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<CreateDNSZoneModal bind:open={zoneCreateOpen} onCreated={refreshZones} />
<EditDNSZoneModal bind:open={zoneEditOpen} zone={selectedZone} onSaved={refreshZones} />
{#if selectedZone}
  <CreateDNSRecordModal bind:open={recordCreateOpen} onCreated={refreshRecords} />
{/if}
<EditDNSRecordModal bind:open={recordEditOpen} record={selectedRecord} onSaved={refreshRecords} />
