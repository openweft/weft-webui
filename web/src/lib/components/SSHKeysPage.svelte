<script lang="ts">
  // SSHKeysPage — CRUD for the named SSH-keys catalogue. Same master-
  // detail layout as ScriptsPage. Admin-only (the route is mounted
  // when the sidebar carries the "ssh-keys" entry, server-side already
  // restricts POST/DELETE to the admin port).
  //
  // The Import flow (GitHub / GitLab / Forgejo) is queued for the next
  // commit. The hint at the top of the right pane documents it so
  // operators know it's coming + understand the manual flow today.
  import {
    listSSHKeyCatalogue, getSSHKeyCatalogue, setSSHKeyCatalogue, deleteSSHKeyCatalogue,
    getMe, type SSHKeyEntry, type Me,
  } from '../api';

  let keys = $state<SSHKeyEntry[]>([]);
  let selected = $state<string>('');
  let current = $state<SSHKeyEntry | null>(null);
  let listErr = $state('');
  let listBusy = $state(false);

  let editName = $state('');
  let editDesc = $state('');
  let editPublicKey = $state('');
  let editBusy = $state(false);
  let editErr = $state('');

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));

  let creating = $state(false);

  function refresh(keepName = selected) {
    listBusy = true;
    listErr = '';
    listSSHKeyCatalogue()
      .then((ks) => {
        keys = ks;
        if (creating) return;
        const names = ks.map((k) => k.name);
        const next = names.includes(keepName) ? keepName : (names[0] ?? '');
        selectKey(next);
      })
      .catch((e) => (listErr = String(e)))
      .finally(() => (listBusy = false));
  }
  $effect(refresh);
  $effect(() => { getMe().then((u) => (me = u)).catch(() => { /* api.ts handled */ }); });

  function selectKey(name: string) {
    selected = name;
    creating = false;
    editErr = '';
    if (!name) { current = null; editName = ''; editDesc = ''; editPublicKey = ''; return; }
    getSSHKeyCatalogue(name).then((k) => {
      current = k;
      editName = k.name;
      editDesc = k.description;
      editPublicKey = k.public_key;
    }).catch((e) => (editErr = String(e)));
  }

  function startNew() {
    selected = '';
    current = null;
    creating = true;
    editName = '';
    editDesc = '';
    editPublicKey = '';
    editErr = '';
  }

  let dirty = $derived.by(() => {
    if (creating) return editName.trim().length > 0 && editPublicKey.trim().length > 0;
    if (!current) return false;
    return editName !== current.name
        || editDesc !== current.description
        || editPublicKey !== current.public_key;
  });

  async function save() {
    if (!editName.trim()) { editErr = 'name is required'; return; }
    if (!editPublicKey.trim()) { editErr = 'public key is required'; return; }
    editBusy = true; editErr = '';
    try {
      const saved = await setSSHKeyCatalogue({
        name: editName.trim(),
        description: editDesc,
        public_key: editPublicKey,
      });
      creating = false;
      selected = saved.name;
      const ks = await listSSHKeyCatalogue();
      keys = ks;
      current = saved;
    } catch (e) {
      editErr = String(e);
    } finally {
      editBusy = false;
    }
  }

  async function del() {
    if (!selected) return;
    if (!confirm(`Delete key "${selected}" ? VMs that reference it by name will lose access on the next sshkeys publish ; existing connections aren't dropped.`)) return;
    try {
      await deleteSSHKeyCatalogue(selected);
      const ks = await listSSHKeyCatalogue();
      keys = ks;
      selectKey(ks[0]?.name ?? '');
    } catch (e) { editErr = String(e); }
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

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">SSH Keys</h2>
    <p class="text-sm text-base-content/60">
      Named SSH public keys. VMs reference them by name from the drawer.
      Imports from GitHub / GitLab / Forgejo arrive in a follow-on commit.
    </p>
  </div>
  {#if canEdit}
    <button class="ml-auto btn btn-sm btn-primary gap-1" onclick={startNew}>
      <span class="text-base leading-none">+</span> New key
    </button>
  {/if}
</div>

{#if listErr}
  <div class="mt-2 alert alert-error text-sm">{listErr}</div>
{/if}

<div class="mt-4 flex gap-4">
  <aside class="w-64 shrink-0">
    <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-base-content/60">
      Catalogue {#if listBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
    </div>
    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each keys as k (k.name)}
        <li>
          <button class:menu-active={selected === k.name} onclick={() => selectKey(k.name)}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 11-7.778 7.778 5.5 5.5 0 017.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" stroke-linejoin="round" />
            </svg>
            <div class="min-w-0">
              <div class="truncate">{k.name}</div>
              <div class="text-[10px] text-base-content/50 flex items-center gap-1">
                <span class="badge badge-xs {sourceBadge(k.source)}">{k.source || 'manual'}</span>
                <span class="font-mono truncate">{k.fingerprint.slice(0, 18)}…</span>
              </div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">No keys. Create one with "+ New key".</li>
      {/each}
    </ul>
  </aside>

  <section class="min-w-0 flex-1">
    {#if !creating && !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Pick a key on the left, or create a new one.
      </div>
    {:else}
      <div class="rounded-box border border-base-300 bg-base-100 p-4">
        <div class="grid gap-3 sm:grid-cols-[1fr_2fr]">
          <label class="form-control">
            <span class="label-text text-xs">Name</span>
            <input
              class="input input-sm input-bordered font-mono"
              placeholder="alice-laptop"
              disabled={!canEdit || (!creating && !!current)}
              bind:value={editName}
            />
            {#if !creating && current}
              <span class="mt-1 text-xs text-base-content/40">Renaming not supported ; delete + recreate.</span>
            {/if}
          </label>
          <label class="form-control">
            <span class="label-text text-xs">Description</span>
            <input
              class="input input-sm input-bordered"
              placeholder="surfaced in the picker + the per-VM drawer"
              disabled={!canEdit}
              bind:value={editDesc}
            />
          </label>
        </div>

        <label class="form-control mt-3">
          <span class="label-text text-xs flex items-baseline gap-2">
            Public key
            <span class="text-base-content/40">
              "&lt;type&gt; &lt;base64&gt; [comment]" — fingerprint computed server-side
            </span>
          </span>
          <textarea
            class="textarea textarea-sm textarea-bordered font-mono text-xs"
            rows="4"
            spellcheck="false"
            disabled={!canEdit}
            bind:value={editPublicKey}
            placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... user@host"
          ></textarea>
        </label>

        {#if current && !creating}
          <div class="mt-2 grid grid-cols-[6rem_1fr] gap-y-1 text-xs text-base-content/60">
            <span class="text-base-content/40">Fingerprint</span>
            <span class="font-mono break-all">{current.fingerprint}</span>
            <span class="text-base-content/40">Source</span>
            <span>
              <span class="badge badge-xs {sourceBadge(current.source)}">{current.source}</span>
              {#if current.source_account}<span class="font-mono ml-1">{current.source_account}</span>{/if}
            </span>
            <span class="text-base-content/40">Last edit</span>
            <span>{current.updated_at} by {current.updated_by || '—'}</span>
          </div>
        {/if}

        {#if editErr}
          <div class="mt-3 alert alert-error py-2 text-sm">{editErr}</div>
        {/if}

        <div class="mt-3 flex items-center gap-2">
          {#if canEdit && !creating && current}
            <button class="btn btn-sm btn-ghost text-error" onclick={del}>Delete</button>
          {/if}
          <div class="ml-auto flex items-center gap-2">
            {#if creating}
              <button class="btn btn-sm btn-ghost" onclick={() => { creating = false; selectKey(keys[0]?.name ?? ''); }}>Cancel</button>
            {/if}
            {#if canEdit}
              <button class="btn btn-sm btn-primary" disabled={!dirty || editBusy} onclick={save}>
                {#if editBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
                Save
              </button>
            {/if}
          </div>
        </div>
      </div>
    {/if}
  </section>
</div>
