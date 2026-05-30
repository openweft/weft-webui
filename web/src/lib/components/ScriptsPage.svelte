<script lang="ts">
  // ScriptsPage — CRUD for the provisioning-script catalogue. Same
  // master-detail layout as SharesPage / ObjectStoragePage : sidebar
  // list on the left, editor on the right.
  //
  // Admin-only by virtue of the resource registry's ScopeAdmin tag ;
  // the route is only mounted when the sidebar carries the "scripts"
  // entry (admin port). Defensive : the save / delete buttons are
  // gated on `me.cluster_admin || me.tenant_admin` too, matching the
  // server's POST/DELETE admin-port-only routing.
  import {
    listScripts, getScript, setScript, deleteScript, getMe,
    type Script, type Me,
  } from '../api';

  let scripts = $state<Script[]>([]);
  let selected = $state<string>('');
  let current = $state<Script | null>(null);
  let listErr = $state('');
  let listBusy = $state(false);

  // Edit buffer — separate from current so we can detect dirty state
  // (any field differs) and only enable Save when there's a change.
  let editName = $state('');
  let editDesc = $state('');
  let editBody = $state('');
  let editBusy = $state(false);
  let editErr = $state('');

  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));

  // "New" mode : the form is editable but no script is selected ; on
  // save the name field becomes the catalogue key.
  let creating = $state(false);

  // Quick substring filter on name / description — matches the
  // pattern used by SSHKeysPage.
  let query = $state('');

  // Header-level action state — busy flag drives the spinner inside
  // the Delete button while the RPC is in flight.
  let actionBusy = $state(false);

  function refresh(keepName = selected) {
    listBusy = true;
    listErr = '';
    listScripts()
      .then((ss) => {
        scripts = ss;
        if (creating) return;
        // Re-select : if the previously-active script is still there,
        // keep it. Otherwise fall back to the first one.
        const names = ss.map((s) => s.name);
        const next = names.includes(keepName) ? keepName : (names[0] ?? '');
        selectScript(next);
      })
      .catch((e) => (listErr = String(e)))
      .finally(() => (listBusy = false));
  }
  $effect(refresh);
  $effect(() => { getMe().then((u) => (me = u)).catch(() => { /* api.ts handled */ }); });

  function selectScript(name: string) {
    selected = name;
    creating = false;
    editErr = '';
    if (!name) { current = null; editName = ''; editDesc = ''; editBody = ''; return; }
    getScript(name).then((s) => {
      current = s;
      editName = s.name;
      editDesc = s.description;
      editBody = s.body;
    }).catch((e) => (editErr = String(e)));
  }

  function startNew() {
    selected = '';
    current = null;
    creating = true;
    editName = '';
    editDesc = '';
    editBody = '#!/bin/sh\nset -eu\n\n# payload is in $PWD (weft-vm-agent cd into it)\n';
    editErr = '';
  }

  // Dirty = something edited vs the loaded snapshot. New mode is
  // dirty by default once a name is typed.
  let dirty = $derived.by(() => {
    if (creating) return editName.trim().length > 0;
    if (!current) return false;
    return editName !== current.name
        || editDesc !== current.description
        || editBody !== current.body;
  });

  async function save() {
    if (!editName.trim()) { editErr = 'name is required'; return; }
    editBusy = true; editErr = '';
    try {
      const saved = await setScript({
        name: editName.trim(),
        description: editDesc,
        body: editBody,
      });
      creating = false;
      selected = saved.name;
      // Refresh list ; selectScript reload happens via $effect chain.
      const ss = await listScripts();
      scripts = ss;
      current = saved;
    } catch (e) {
      editErr = String(e);
    } finally {
      editBusy = false;
    }
  }

  async function del() {
    if (!selected) return;
    if (!confirm(`Delete script "${selected}" ? VMs already provisioned with this script aren't affected ; new VMs that referenced it will lose their boot script body.`)) return;
    actionBusy = true;
    try {
      await deleteScript(selected);
      // Pick the next one (or empty).
      const ss = await listScripts();
      scripts = ss;
      selectScript(ss[0]?.name ?? '');
    } catch (e) {
      editErr = String(e);
    } finally {
      actionBusy = false;
    }
  }

  // Edit jumps focus into the editor's name input. Triggered from the
  // header button so the keyboard flow matches SSHKeysPage's "select
  // row → click Edit" pattern even though the editor is always
  // visible here.
  let nameInput: HTMLInputElement | undefined = $state();
  function startEdit() {
    if (!selected) return;
    editErr = '';
    nameInput?.focus();
  }

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return scripts;
    return scripts.filter((s) =>
      s.name.toLowerCase().includes(q)
      || (s.description ?? '').toLowerCase().includes(q),
    );
  });
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Scripts</h2>
    <p class="text-sm text-base-content/60">
      Named, reusable provisioning sh bodies. Pick a script on the left to view + edit its body on the right.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Master : scripts list with filter + N/E/D, aligned on the DNS / Security / Networks pattern. -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Catalogue</h3>
      {#if listBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
      <span class="ml-auto text-xs text-base-content/50">{filtered.length} of {scripts.length}</span>
    </div>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter scripts…" bind:value={query} />
    </label>

    {#if canEdit}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-sm btn-primary gap-1" onclick={startNew} title="Create a new script">
          <span class="text-base leading-none">+</span> New
        </button>
        <button class="btn btn-sm btn-warning gap-1"
          disabled={!selected || creating || actionBusy}
          onclick={startEdit}
          title={selected ? `Edit "${selected}"` : 'Select a script to edit'}>
          Edit
        </button>
        <button class="btn btn-sm btn-error gap-1"
          disabled={!selected || creating || actionBusy}
          onclick={del}
          title={selected ? `Delete "${selected}"` : 'Select a script to delete'}>
          {#if actionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Delete
        </button>
      </div>
    {/if}

    {#if listErr}<div class="alert alert-error py-2 text-sm">{listErr}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filtered as s (s.name)}
        <li>
          <button class:menu-active={selected === s.name} onclick={() => selectScript(s.name)}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M4 4h12l4 4v12H4z" stroke-linejoin="round" />
              <path d="M8 12h8M8 16h6" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium">{s.name}</div>
              <div class="text-[10px] text-base-content/50">
                {1 + s.body.split('\n').length - 1} lines · {s.updated_by || '—'}
              </div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {scripts.length === 0 ? 'No scripts. Create one with "+ New".' : 'No scripts match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <section class="min-w-0 flex-1">
    {#if !creating && !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a script on the left, or create a new one.
      </div>
    {:else}
      <div class="rounded-box border border-base-300 bg-base-100 p-4">
        <div class="grid gap-3 sm:grid-cols-[1fr_2fr]">
          <label class="form-control">
            <span class="label-text text-xs">Name</span>
            <input
              class="input input-sm input-bordered font-mono"
              placeholder="setup-nginx"
              disabled={!canEdit || (!creating && !!current)}
              bind:this={nameInput}
              bind:value={editName}
            />
            {#if !creating && current}
              <span class="mt-1 text-xs text-base-content/40">Renaming is not supported ; delete + recreate.</span>
            {/if}
          </label>
          <label class="form-control">
            <span class="label-text text-xs">Description</span>
            <input
              class="input input-sm input-bordered"
              placeholder="one-line summary, surfaced in the picker"
              disabled={!canEdit}
              bind:value={editDesc}
            />
          </label>
        </div>

        <label class="form-control mt-3">
          <span class="label-text text-xs flex items-baseline gap-2">
            Body
            <span class="text-base-content/40">
              POSIX sh ; executed by mvdan.cc/sh/v3 in the payload's CWD
            </span>
          </span>
          <textarea
            class="textarea textarea-sm textarea-bordered font-mono text-xs"
            rows="16"
            spellcheck="false"
            disabled={!canEdit}
            bind:value={editBody}
          ></textarea>
        </label>

        {#if current && !creating}
          <div class="mt-2 text-xs text-base-content/50">
            Last edit : {current.updated_at} by {current.updated_by || '—'}
          </div>
        {/if}

        {#if editErr}
          <div class="mt-3 alert alert-error py-2 text-sm">{editErr}</div>
        {/if}

        <div class="mt-3 flex items-center gap-2">
          <div class="ml-auto flex items-center gap-2">
            {#if creating}
              <button class="btn btn-sm btn-ghost" onclick={() => { creating = false; selectScript(scripts[0]?.name ?? ''); }}>Cancel</button>
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
