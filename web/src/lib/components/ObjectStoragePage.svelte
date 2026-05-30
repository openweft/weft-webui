<script lang="ts">
  import { getRows, getMe, createBucket, deleteBucket, type Me, type Row } from '../api';
  import FileBrowser from './FileBrowser.svelte';

  let buckets = $state<Row[]>([]);
  let selected = $state<string>('');
  let me = $state<Me | null>(null);
  let deleteError = $state('');

  // Same gate as SharesPage : tenant admins (cluster admins included)
  // see the "+ New bucket" button. Server enforces, this just hides
  // the affordance that would 403.
  let canCreate = $derived(!!me && (me.cluster_admin || me.tenant_admin));

  async function loadBuckets(keep = selected) {
    buckets = await getRows('buckets');
    const names = buckets.map((b) => String(b.name));
    selected = names.includes(keep) ? keep : (names[0] ?? '');
  }
  $effect(() => { loadBuckets(); });
  $effect(() => { getMe().then((u) => (me = u)).catch(() => { /* api.ts handled it */ }); });

  let current = $derived(buckets.find((b) => String(b.name) === selected));

  let createOpen = $state(false);
  let bucketDialog: HTMLDialogElement;
  let newName = $state('');
  let creating = $state(false);
  let bucketError = $state('');

  $effect(() => {
    if (createOpen) bucketDialog?.showModal();
    else bucketDialog?.close();
  });

  async function submitBucket(e: SubmitEvent) {
    e.preventDefault();
    bucketError = '';
    creating = true;
    try {
      await createBucket(newName.trim());
      const created = newName.trim();
      newName = '';
      createOpen = false;
      await loadBuckets(created);
    } catch (err) {
      bucketError = String(err);
    } finally {
      creating = false;
    }
  }

  async function delSelected() {
    if (!current) return;
    if (!confirm(`Delete bucket "${current.name}" and all its objects?`)) return;
    deleteError = '';
    try {
      await deleteBucket(String(current.name));
      await loadBuckets(String(current.name) === selected ? '' : selected);
    } catch (e) { deleteError = String(e); }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Object Storage</h2>
    <p class="text-sm text-base-content/60">S3 buckets &amp; objects</p>
  </div>
  {#if canCreate}
    <button class="ml-auto btn btn-sm btn-primary gap-1" onclick={() => (createOpen = true)}>
      <span class="text-base leading-none">+</span> New bucket
    </button>
  {/if}
</div>

{#if deleteError}
  <div class="mt-2 alert alert-error text-sm">{deleteError}</div>
{/if}

<div class="mt-4 flex gap-4">
  <aside class="w-60 shrink-0">
    <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-base-content/60">Buckets</div>
    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each buckets as b (b.name)}
        <li>
          <button class:menu-active={selected === b.name} onclick={() => (selected = String(b.name))}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M3 7h18v12H3zM3 7l2-3h6l2 3" stroke-linejoin="round" />
            </svg>
            <div class="min-w-0">
              <div class="truncate">{b.name}</div>
              <div class="text-[10px] text-base-content/50">s3 · {b.objects} objects</div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">No buckets yet.</li>
      {/each}
    </ul>
  </aside>

  <section class="min-w-0 flex-1">
    {#if !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a bucket{canCreate ? ' or create one to get started' : ''}.
      </div>
    {:else}
      {#if current}
        <div class="mb-2 flex flex-wrap items-center gap-2 text-sm text-base-content/60">
          <span class="badge badge-sm badge-ghost">s3</span>
          {#if current.project}<span class="badge badge-sm badge-ghost">project {current.project}</span>{/if}
          <span>{current.objects} objects</span>
          <span>· {current.size}</span>
          {#if current.created}<span class="text-base-content/50">· created {current.created}</span>{/if}
          {#if canCreate}
            <button class="ml-auto btn btn-xs btn-ghost text-error" onclick={delSelected}>
              Delete
            </button>
          {/if}
        </div>
      {/if}
      {#key selected}
        <FileBrowser kind="buckets" container={selected} />
      {/key}
    {/if}
  </section>
</div>

<dialog class="modal" bind:this={bucketDialog} onclose={() => (createOpen = false)}>
  <div class="modal-box max-w-md">
    <h3 class="text-lg font-bold">New bucket</h3>
    <form class="mt-4 flex flex-col gap-3" onsubmit={submitBucket}>
      <label class="form-control">
        <span class="label-text mb-1 text-xs">Bucket name</span>
        <input class="input input-sm input-bordered" placeholder="my-bucket" bind:value={newName} />
        <span class="mt-1 text-xs text-base-content/50">3–63 chars · lowercase letters, digits, hyphens</span>
      </label>
      {#if bucketError}<div class="alert alert-error py-2 text-sm">{bucketError}</div>{/if}
      <div class="modal-action mt-1">
        <button type="button" class="btn btn-sm btn-ghost" onclick={() => (createOpen = false)}>Cancel</button>
        <button type="submit" class="btn btn-sm btn-primary" disabled={creating}>
          {#if creating}<span class="loading loading-spinner loading-xs"></span>{/if}
          Create
        </button>
      </div>
    </form>
  </div>
  <form method="dialog" class="modal-backdrop"><button>close</button></form>
</dialog>
