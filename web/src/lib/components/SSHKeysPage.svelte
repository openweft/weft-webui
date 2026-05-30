<script lang="ts">
  // SSHKeysPage — table of named SSH keys + right-side drawer for
  // detail / edit. Replaces the previous master-detail-in-page
  // layout (didn't scale past a dozen entries — the user flagged it).
  //
  // Row click opens SSHKeyDrawer. "+ New key" opens the same drawer
  // in create-mode. "Import" opens the gh/gl/forgejo modal. Admin
  // affordances are gated on canEdit (cluster_admin || tenant_admin)
  // ; non-admins see the table + drawer in read-only mode.
  import {
    listSSHKeyCatalogue, importSSHKeys, getMe,
    type SSHKeyEntry, type Me, type ImportSSHKeysResult,
  } from '../api';
  import SSHKeyDrawer from './SSHKeyDrawer.svelte';

  let keys = $state<SSHKeyEntry[]>([]);
  let listErr = $state('');
  let listBusy = $state(false);

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));

  // Drawer state : either an existing entry is selected or we're
  // creating a new one. Mutually exclusive.
  let selected = $state<SSHKeyEntry | null>(null);
  let creating = $state(false);

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

  function openRow(k: SSHKeyEntry) {
    creating = false;
    selected = k;
  }
  function startNew() {
    selected = null;
    creating = true;
  }
  function closeDrawer() {
    selected = null;
    creating = false;
  }
  function onSaved(saved: SSHKeyEntry) {
    creating = false;
    selected = saved;
    refresh();
  }
  function onDeleted(name: string) {
    selected = null;
    creating = false;
    refresh();
  }

  // ---- Import modal state ----
  let importOpen = $state(false);
  let importProvider = $state<'github' | 'gitlab' | 'forgejo'>('github');
  let importAccount = $state('');
  let importForgejoBase = $state('');
  let importBusy = $state(false);
  let importErr = $state('');
  let importResult = $state<ImportSSHKeysResult | null>(null);

  async function runImport() {
    if (!importAccount.trim()) { importErr = 'account is required'; return; }
    if (importProvider === 'forgejo' && !importForgejoBase.trim()) {
      importErr = 'forgejo base URL is required (e.g. https://codeberg.org)';
      return;
    }
    importBusy = true; importErr = ''; importResult = null;
    try {
      const res = await importSSHKeys({
        provider: importProvider,
        account: importAccount.trim(),
        forgejo_base: importProvider === 'forgejo' ? importForgejoBase.trim() : undefined,
      });
      importResult = res;
      refresh();
    } catch (e) {
      importErr = String(e);
    } finally {
      importBusy = false;
    }
  }
  function resetImport() {
    importProvider = 'github'; importAccount = ''; importForgejoBase = '';
    importErr = ''; importResult = null;
  }
  function closeImport() {
    importOpen = false;
    resetImport();
  }
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
      <button class="btn btn-sm btn-ghost gap-1" onclick={() => (importOpen = true)}>
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.7">
          <path d="M12 3v12m0 0l-4-4m4 4l4-4M5 21h14" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        Import
      </button>
      <button class="btn btn-sm btn-primary gap-1" onclick={startNew}>
        <span class="text-base leading-none">+</span> New key
      </button>
    {/if}
  </div>
</div>

{#if listErr}
  <div class="mt-2 alert alert-error text-sm">{listErr}</div>
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
          <tr class="hover cursor-pointer" onclick={() => openRow(k)}>
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

{#if selected || creating}
  <SSHKeyDrawer
    entry={selected}
    {creating}
    {canEdit}
    onClose={closeDrawer}
    {onSaved}
    {onDeleted}
  />
{/if}

<!-- Import modal : fetches <provider>/<account>.keys server-side. -->
{#if importOpen}
  <dialog class="modal modal-open">
    <div class="modal-box max-w-lg">
      <h3 class="text-lg font-bold">Import SSH keys</h3>
      <p class="text-sm text-base-content/60">
        Fetches the public <code>.keys</code> file from the chosen
        forge account. Duplicates (same fingerprint) are skipped ;
        new entries are named
        <code>&lt;provider&gt;:&lt;account&gt;/&lt;index&gt;</code> so a
        future refresh flow can find + replace them.
      </p>

      <label class="form-control mt-4">
        <span class="label-text text-xs">Provider</span>
        <select class="select select-sm select-bordered" bind:value={importProvider}>
          <option value="github">GitHub — github.com/&lt;account&gt;.keys</option>
          <option value="gitlab">GitLab — gitlab.com/&lt;account&gt;.keys</option>
          <option value="forgejo">Forgejo / Gitea — &lt;base&gt;/&lt;account&gt;.keys</option>
        </select>
      </label>

      <label class="form-control mt-3">
        <span class="label-text text-xs">Account</span>
        <input class="input input-sm input-bordered font-mono" placeholder="alice"
          bind:value={importAccount} />
      </label>

      {#if importProvider === 'forgejo'}
        <label class="form-control mt-3">
          <span class="label-text text-xs">Forgejo / Gitea base URL</span>
          <input class="input input-sm input-bordered font-mono"
            placeholder="https://codeberg.org"
            bind:value={importForgejoBase} />
        </label>
      {/if}

      {#if importErr}
        <div class="mt-3 alert alert-error py-2 text-sm">{importErr}</div>
      {/if}
      {#if importResult}
        <div class="mt-3 alert alert-success py-2 text-sm">
          <div>
            Added {importResult.added} ·
            Skipped {importResult.skipped_existing} (duplicate fingerprint) ·
            Total seen {importResult.total_seen}
            {#if importResult.names.length > 0}
              <div class="mt-1 font-mono text-xs">
                {importResult.names.join(', ')}
              </div>
            {/if}
          </div>
        </div>
      {/if}

      <div class="modal-action">
        <button class="btn btn-sm btn-ghost" onclick={closeImport}>Close</button>
        <button class="btn btn-sm btn-primary"
          disabled={importBusy || !importAccount.trim()}
          onclick={runImport}>
          {#if importBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Fetch + import
        </button>
      </div>
    </div>
    <button class="modal-backdrop" aria-label="close" onclick={closeImport}></button>
  </dialog>
{/if}
