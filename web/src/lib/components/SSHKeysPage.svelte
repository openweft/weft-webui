<script lang="ts">
  // SSHKeysPage — table of named SSH keys + right-side drawer for
  // detail / edit + three header buttons (New / Edit / Delete) with
  // semantic colors.
  //
  // Two-stage interaction model (per the user's UX direction) :
  //   1. Click a row : selects it (highlights), does NOT open the
  //      drawer. Edit + Delete buttons in the header light up.
  //   2. Header button : New / Edit / Delete acts on the selection.
  //
  // The drawer remains the canonical place to view + edit. Delete
  // can fire from the header without opening the drawer first —
  // confirms inline like every other destructive action in the app.
  // Non-admins see the page in read-only mode (no New / Edit /
  // Delete affordances surfaced).
  import {
    listSSHKeyCatalogue, deleteSSHKeyCatalogue, getMe,
    type SSHKeyEntry, type Me,
  } from '../api';
  import SSHKeyDrawer from './SSHKeyDrawer.svelte';

  let keys = $state<SSHKeyEntry[]>([]);
  let listErr = $state('');
  let listBusy = $state(false);

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));

  // Selection state : the highlighted row's name. Empty = nothing
  // selected (Edit / Delete disabled).
  let selectedName = $state<string>('');
  let selected = $derived<SSHKeyEntry | null>(
    keys.find((k) => k.name === selectedName) ?? null,
  );

  // Drawer state : what's currently shown in the drawer (independent
  // of the selection so closing the drawer doesn't deselect).
  let drawerEntry = $state<SSHKeyEntry | null>(null);
  let creating = $state(false);
  let drawerOpen = $derived(creating || drawerEntry !== null);

  // Action error surfaced in an inline alert (e.g. delete failed).
  let actionErr = $state('');
  let actionBusy = $state(false);

  // Table filter — quick substring match on name / description /
  // fingerprint / source / account. Inline since the table is small
  // and the search shape is uniform.
  let query = $state('');

  function refresh() {
    listBusy = true; listErr = '';
    listSSHKeyCatalogue()
      .then((ks) => (keys = ks))
      .catch((e) => (listErr = String(e)))
      .finally(() => (listBusy = false));
  }
  $effect(refresh);
  $effect(() => { getMe().then((u) => (me = u)).catch(() => { /* api.ts handled */ }); });

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return keys;
    return keys.filter((k) =>
      k.name.toLowerCase().includes(q)
      || (k.description ?? '').toLowerCase().includes(q)
      || k.fingerprint.toLowerCase().includes(q)
      || (k.source ?? '').toLowerCase().includes(q)
      || (k.source_account ?? '').toLowerCase().includes(q),
    );
  });

  // sshTypeOf extracts the "ssh-ed25519" prefix from a stored
  // public_key. Cheaper than re-parsing + decoding ; we just need
  // the first whitespace-separated word.
  function sshTypeOf(line: string): string {
    const i = line.indexOf(' ');
    return i > 0 ? line.slice(0, i) : '';
  }

  function sourceBadge(s: string): string {
    switch (s) {
      case 'github': return 'badge-success';
      case 'gitlab': return 'badge-warning';
      case 'forgejo': return 'badge-info';
      case 'manual': return 'badge-ghost';
      default: return 'badge-ghost';
    }
  }

  function clickRow(k: SSHKeyEntry) {
    // Toggle selection : second click on the same row deselects.
    // Makes the "no selection" state reachable without clicking
    // outside the table.
    selectedName = selectedName === k.name ? '' : k.name;
    actionErr = '';
  }

  function startNew() {
    actionErr = '';
    drawerEntry = null;
    creating = true;
  }

  function startEdit() {
    if (!selected) return;
    actionErr = '';
    creating = false;
    drawerEntry = selected;
  }

  async function startDelete() {
    if (!selected) return;
    if (!confirm(`Delete key "${selected.name}" ? VMs that reference it by name will lose access on the next sshkeys publish ; existing connections aren't dropped.`)) return;
    actionBusy = true; actionErr = '';
    try {
      await deleteSSHKeyCatalogue(selected.name);
      selectedName = '';
      refresh();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }

  function closeDrawer() {
    drawerEntry = null;
    creating = false;
  }
  function onSaved(saved: SSHKeyEntry) {
    creating = false;
    drawerEntry = saved;
    selectedName = saved.name;
    refresh();
  }
  function onDeleted(_name: string) {
    drawerEntry = null;
    creating = false;
    selectedName = '';
    refresh();
  }

  // Import is now a sub-mode inside SSHKeyDrawer (createMode ===
  // 'import'). The page used to host a separate modal — replaced
  // per the user's "unify + New key and Import via the side panel"
  // direction. The drawer's onSaved callback already refreshes the
  // table after a successful import.
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">SSH Keys</h2>
    <p class="text-sm text-base-content/60">
      Named SSH public keys. Click a row for details + edit ; VMs
      reference them by name from the drawer's SSH-keys tab.
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter…" bind:value={query} />
    </label>
    {#if canEdit}
      <button class="btn btn-sm btn-primary gap-1" onclick={startNew} title="Create a new key">
        <span class="text-base leading-none">+</span> New
      </button>
      <button class="btn btn-sm btn-warning gap-1"
        disabled={!selected || actionBusy}
        onclick={startEdit}
        title={selected ? `Edit "${selected.name}"` : 'Select a row to edit'}>
        Edit
      </button>
      <button class="btn btn-sm btn-error gap-1"
        disabled={!selected || actionBusy}
        onclick={startDelete}
        title={selected ? `Delete "${selected.name}"` : 'Select a row to delete'}>
        {#if actionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Delete
      </button>
    {/if}
  </div>
</div>

{#if listErr}
  <div class="mt-2 alert alert-error text-sm">{listErr}</div>
{/if}
{#if actionErr}
  <div class="mt-2 alert alert-error text-sm">{actionErr}</div>
{/if}

<div class="mt-4 overflow-x-auto rounded-box border border-base-300 bg-base-100">
  <table class="table table-sm">
    <thead>
      <tr>
        <th>Name</th>
        <th>Description</th>
        <th>Type</th>
        <th>Fingerprint</th>
        <th>Source</th>
        <th>Updated</th>
      </tr>
    </thead>
    <tbody>
      {#if listBusy}
        <tr><td colspan="6" class="py-8 text-center">
          <span class="loading loading-spinner"></span>
        </td></tr>
      {:else if filtered.length === 0}
        <tr><td colspan="6" class="py-8 text-center text-base-content/50">
          {keys.length === 0
            ? 'No keys yet. Create one with "+ New key" or pull a forge account via Import.'
            : 'No keys match the filter.'}
        </td></tr>
      {:else}
        {#each filtered as k (k.name)}
          <tr class="hover cursor-pointer"
            class:bg-primary={selectedName === k.name}
            class:text-primary-content={selectedName === k.name}
            onclick={() => clickRow(k)}>
            <td class="font-mono">{k.name}</td>
            <td class="max-w-xs truncate">{k.description || '—'}</td>
            <td><span class="badge badge-xs badge-ghost">{sshTypeOf(k.public_key)}</span></td>
            <td class="font-mono text-xs">{k.fingerprint.slice(0, 28)}…</td>
            <td>
              <span class="badge badge-sm {sourceBadge(k.source)}">{k.source || 'manual'}</span>
              {#if k.source_account}<span class="ml-1 font-mono text-xs text-base-content/60">{k.source_account}</span>{/if}
            </td>
            <td class="text-xs text-base-content/70">
              {k.updated_at.slice(0, 10)} <span class="text-base-content/40">· {k.updated_by || '—'}</span>
            </td>
          </tr>
        {/each}
      {/if}
    </tbody>
  </table>
</div>

{#if drawerOpen}
  <SSHKeyDrawer
    entry={drawerEntry}
    {creating}
    {canEdit}
    onClose={closeDrawer}
    {onSaved}
    {onDeleted}
  />
{/if}

<!-- Import is a sub-mode inside SSHKeyDrawer (createMode = 'import')
     since the user direction was to unify "+ New key" + "Import"
     behind a single side-panel entry point. The drawer's tab strip
     surfaces both flows. -->
