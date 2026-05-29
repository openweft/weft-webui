<script lang="ts">
  // SSHKeyDrawer — right-side slide-in editor for one catalogue entry.
  // Used by SSHKeysPage when a table row is clicked (or "+ New key"
  // creates a draft). Same shape as MicroVMDrawer / SecurityGroupDrawer
  // / LoadBalancerDrawer : fixed inset-y-0 right-0, max-w-2xl,
  // shadow-2xl, click-outside-to-close backdrop.
  //
  // Three modes :
  //   - read-only  : the operator doesn't have tenant_admin /
  //                  cluster_admin — Save / Delete hidden, fields
  //                  disabled.
  //   - editing    : an existing entry is selected and canEdit is
  //                  true. Save enabled when dirty.
  //   - creating   : `creating` prop is true ; Name field editable,
  //                  Save creates the entry.
  import { setSSHKeyCatalogue, deleteSSHKeyCatalogue, type SSHKeyEntry } from '../api';

  let {
    entry,
    creating = false,
    canEdit,
    onClose,
    onSaved,
    onDeleted,
  }: {
    entry: SSHKeyEntry | null;
    creating?: boolean;
    canEdit: boolean;
    onClose: () => void;
    onSaved: (e: SSHKeyEntry) => void;
    onDeleted: (name: string) => void;
  } = $props();

  // Edit buffer — separate from the prop so Cancel is clean.
  let editName = $state('');
  let editDesc = $state('');
  let editPublicKey = $state('');
  let busy = $state(false);
  let err = $state('');

  // Sync the buffer to the entry / create-mode whenever the props
  // shift. $effect fires once per (entry, creating) change.
  $effect(() => {
    err = '';
    if (creating) {
      editName = '';
      editDesc = '';
      editPublicKey = '';
    } else if (entry) {
      editName = entry.name;
      editDesc = entry.description;
      editPublicKey = entry.public_key;
    }
  });

  let dirty = $derived.by(() => {
    if (creating) return editName.trim().length > 0 && editPublicKey.trim().length > 0;
    if (!entry) return false;
    return editName !== entry.name
        || editDesc !== entry.description
        || editPublicKey !== entry.public_key;
  });

  async function save() {
    if (!editName.trim()) { err = 'name is required'; return; }
    if (!editPublicKey.trim()) { err = 'public key is required'; return; }
    busy = true; err = '';
    try {
      const saved = await setSSHKeyCatalogue({
        name: editName.trim(),
        description: editDesc,
        public_key: editPublicKey,
      });
      onSaved(saved);
    } catch (e) {
      err = String(e);
    } finally {
      busy = false;
    }
  }

  async function del() {
    if (!entry || creating) return;
    if (!confirm(`Delete key "${entry.name}" ? VMs that reference it by name will lose access on the next sshkeys publish ; existing connections aren't dropped.`)) return;
    busy = true; err = '';
    try {
      await deleteSSHKeyCatalogue(entry.name);
      onDeleted(entry.name);
    } catch (e) {
      err = String(e);
      busy = false;
    }
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
</script>

<!-- Backdrop : click closes. Same pattern as MicroVMDrawer. -->
<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col bg-base-100 shadow-2xl">
  <!-- Header -->
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div class="min-w-0">
      <h2 class="text-lg font-bold truncate">
        {#if creating}
          New SSH key
        {:else if entry}
          {entry.name}
        {:else}
          —
        {/if}
      </h2>
      {#if entry && !creating}
        <p class="text-xs text-base-content/60 flex items-center gap-2">
          <span class="badge badge-xs {sourceBadge(entry.source)}">{entry.source}</span>
          {#if entry.source_account}<span class="font-mono">{entry.source_account}</span>{/if}
          <span class="font-mono truncate">{entry.fingerprint}</span>
        </p>
      {/if}
    </div>
    <button class="ml-auto btn btn-ghost btn-xs" aria-label="Close" onclick={onClose}>✕</button>
  </header>

  <!-- Body -->
  <div class="flex-1 overflow-y-auto px-5 py-4">
    <label class="form-control">
      <span class="label-text text-xs">Name</span>
      <input
        class="input input-sm input-bordered font-mono"
        placeholder="alice-laptop"
        disabled={!canEdit || !creating}
        bind:value={editName}
      />
      {#if !creating && entry}
        <span class="mt-1 text-xs text-base-content/40">
          Renaming not supported ; delete + recreate if you need a new name.
        </span>
      {/if}
    </label>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Description</span>
      <input
        class="input input-sm input-bordered"
        placeholder="surfaced in the per-VM drawer's picker"
        disabled={!canEdit}
        bind:value={editDesc}
      />
    </label>

    <label class="form-control mt-3">
      <span class="label-text text-xs flex items-baseline gap-2">
        Public key
        <span class="text-base-content/40">
          "&lt;type&gt; &lt;base64&gt; [comment]" — fingerprint computed server-side
        </span>
      </span>
      <textarea
        class="textarea textarea-sm textarea-bordered font-mono text-xs"
        rows="5"
        spellcheck="false"
        disabled={!canEdit}
        bind:value={editPublicKey}
        placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... user@host"
      ></textarea>
    </label>

    {#if entry && !creating}
      <div class="mt-4 grid grid-cols-[6rem_1fr] gap-y-1 text-xs">
        <span class="text-base-content/40">Fingerprint</span>
        <span class="font-mono break-all">{entry.fingerprint}</span>
        <span class="text-base-content/40">Source</span>
        <span>
          <span class="badge badge-xs {sourceBadge(entry.source)}">{entry.source}</span>
          {#if entry.source_account}<span class="font-mono ml-1">{entry.source_account}</span>{/if}
        </span>
        <span class="text-base-content/40">Last edit</span>
        <span>{entry.updated_at} by {entry.updated_by || '—'}</span>
      </div>
    {/if}

    {#if err}
      <div class="mt-3 alert alert-error py-2 text-sm">{err}</div>
    {/if}
  </div>

  <!-- Footer -->
  <div class="flex shrink-0 items-center gap-2 border-t border-base-300 px-5 py-3">
    {#if canEdit && entry && !creating}
      <button class="btn btn-sm btn-ghost text-error" disabled={busy} onclick={del}>
        Delete
      </button>
    {/if}
    <div class="ml-auto flex items-center gap-2">
      <button class="btn btn-sm btn-ghost" onclick={onClose}>Close</button>
      {#if canEdit}
        <button class="btn btn-sm btn-primary" disabled={!dirty || busy} onclick={save}>
          {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Save
        </button>
      {/if}
    </div>
  </div>
</aside>
