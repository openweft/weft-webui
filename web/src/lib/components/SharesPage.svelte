<script lang="ts">
  // SharesPage — master-detail aligned on DNSPage :
  //   left pane : filter + N/E/D + zone-style list of shares
  //   right pane : metadata badges + FileBrowser for the selected share
  //
  // Edit opens a resize/read-only modal — there's no rename surface
  // because the share name is the CubeFS volume identifier (live
  // wiring would coordinate the rename across mounting VMs ; out
  // of scope for the mock).
  import { getRows, getMe, deleteShare, resizeShare, type Me, type Row } from '../api';
  import FileBrowser from './FileBrowser.svelte';
  import CreateShareModal from './CreateShareModal.svelte';

  let shares = $state<Row[]>([]);
  let selected = $state<string>('');
  let me = $state<Me | null>(null);
  let canCreate = $derived(!!me && (me.cluster_admin || me.tenant_admin));

  function refresh() {
    getRows('shares').then((rows) => {
      shares = rows;
      if (rows.length && !rows.find((r) => r.name === selected)) {
        selected = String(rows[0].name);
      } else if (rows.length === 0) {
        selected = '';
      }
    });
  }
  $effect(refresh);
  $effect(() => { getMe().then((u) => (me = u)).catch(() => { /* api.ts handled it */ }); });

  let current = $derived(shares.find((s) => String(s.name) === selected));

  let createOpen = $state(false);
  let actionErr = $state('');
  let actionBusy = $state(false);
  let query = $state('');

  async function delSelected() {
    if (!current) return;
    if (!confirm(`Delete share ${current.name} ? CubeFS volume must be unmounted from every microVM first.`)) return;
    actionErr = ''; actionBusy = true;
    try {
      await deleteShare(String(current.name));
      refresh();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return shares;
    return shares.filter((s) => String(s.name).toLowerCase().includes(q)
      || String(s.backend ?? '').toLowerCase().includes(q));
  });

  // ---- edit (resize) modal ----
  let editDlg: HTMLDialogElement;
  let editSize = $state(0);
  let editReadOnly = $state(false);
  let editErr = $state('');
  let editBusy = $state(false);

  function startEdit() {
    if (!current) return;
    editErr = '';
    editSize = Number(current.size_gb ?? 0);
    editReadOnly = !!current.readonly;
    editDlg.showModal();
  }

  async function submitEdit(e: SubmitEvent) {
    e.preventDefault();
    if (!current) return;
    editErr = '';
    editBusy = true;
    try {
      await resizeShare(String(current.name), editSize, editReadOnly);
      editDlg.close();
      refresh();
    } catch (err) {
      editErr = String(err);
    } finally {
      editBusy = false;
    }
  }

  function statusBadge(v: unknown): string {
    switch (String(v).toLowerCase()) {
      case 'active': return 'badge-success';
      case 'provisioning': return 'badge-warning';
      case 'failed': return 'badge-error';
      default: return 'badge-ghost';
    }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Shares</h2>
    <p class="text-sm text-base-content/60">
      POSIX (RWX) filesystems mounted across workloads — pick a share to browse its contents.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Master : shares list with filter + N/E/D -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Shares</h3>
      <span class="ml-auto text-xs text-base-content/50">{filtered.length} of {shares.length}</span>
    </div>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter shares…" bind:value={query} />
    </label>

    {#if canCreate}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-sm btn-primary gap-1" onclick={() => (createOpen = true)}
          title="Create a new share">
          <span class="text-base leading-none">+</span> New
        </button>
        <button class="btn btn-sm btn-warning gap-1"
          disabled={!current || actionBusy}
          onclick={startEdit}
          title={current ? `Resize / toggle read-only on "${current.name}"` : 'Select a share to edit'}>
          Edit
        </button>
        <button class="btn btn-sm btn-error gap-1"
          disabled={!current || actionBusy}
          onclick={delSelected}
          title={current ? `Delete "${current.name}"` : 'Select a share to delete'}>
          {#if actionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Delete
        </button>
      </div>
    {/if}

    {#if actionErr}<div class="alert alert-error py-2 text-sm">{actionErr}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filtered as s (s.name)}
        <li>
          <button class:menu-active={selected === s.name} onclick={() => (selected = String(s.name))}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M3 7h6l2 2h10v9H3z" stroke-linejoin="round" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-2">
                <span class="truncate font-medium">{s.name}</span>
                {#if s.readonly}<span class="badge badge-xs badge-warning">RO</span>{/if}
              </div>
              <div class="text-[10px] text-base-content/50">
                {s.backend} · {s.size_gb} GB · {s.mounts} mounts
              </div>
            </div>
            {#if s.status}
              <span class="badge badge-xs {statusBadge(s.status)}">{s.status}</span>
            {/if}
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {shares.length === 0 ? 'No shares.' : 'No shares match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <!-- Detail : browser of the selected share -->
  <section class="min-w-0 flex-1 flex flex-col gap-2">
    {#if !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a share to browse its contents.
      </div>
    {:else if current}
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">
            Files in <span class="font-mono normal-case text-base-content">{current.name}</span>
          </h3>
          <p class="text-xs text-base-content/50">
            <span class="badge badge-xs badge-ghost">{current.backend}</span>
            project {current.project ?? '—'} · {current.size_gb} GB · {current.mounts} mounts
            {#if current.readonly}· <span class="badge badge-xs badge-warning">read-only</span>{/if}
          </p>
        </div>
      </div>
      {#key selected}
        <FileBrowser kind="shares" container={selected} />
      {/key}
    {/if}
  </section>
</div>

<CreateShareModal bind:open={createOpen} onCreated={refresh} />

<dialog class="modal" bind:this={editDlg}>
  <div class="modal-box max-w-md">
    <h3 class="text-lg font-bold">Edit share {current?.name ?? ''}</h3>
    <p class="mt-1 text-sm text-base-content/60">
      Grow capacity or toggle the read-only flag. Shrinking is not
      supported — CubeFS owns physical capacity.
    </p>
    <form class="mt-4 flex flex-col gap-3" onsubmit={submitEdit}>
      <label class="form-control">
        <span class="label-text mb-1 text-xs">Size (GiB)</span>
        <input type="number" min="1" class="input input-sm input-bordered"
          bind:value={editSize} />
      </label>
      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" class="toggle toggle-sm" bind:checked={editReadOnly} />
        Read-only
      </label>
      {#if editErr}<div class="alert alert-error py-2 text-sm">{editErr}</div>{/if}
      <div class="modal-action mt-1">
        <button type="button" class="btn btn-sm btn-ghost" onclick={() => editDlg.close()}>Cancel</button>
        <button type="submit" class="btn btn-sm btn-primary" disabled={editBusy}>
          {#if editBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Save
        </button>
      </div>
    </form>
  </div>
  <form method="dialog" class="modal-backdrop"><button>close</button></form>
</dialog>
