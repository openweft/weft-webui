<script lang="ts">
  import { getRows, createBucket, deleteBucket, type Row } from '../api';
  import FileBrowser from './FileBrowser.svelte';

  let buckets = $state<Row[]>([]);
  let selected = $state<string>('');
  let bucketError = $state('');

  async function loadBuckets(keep = selected) {
    buckets = await getRows('buckets');
    const names = buckets.map((b) => String(b.name));
    selected = names.includes(keep) ? keep : (names[0] ?? '');
  }
  $effect(() => {
    loadBuckets();
  });

  let bucketDialog: HTMLDialogElement;
  let newName = $state('');
  let creating = $state(false);

  async function submitBucket(e: SubmitEvent) {
    e.preventDefault();
    bucketError = '';
    creating = true;
    try {
      await createBucket(newName.trim());
      bucketDialog.close();
      const created = newName.trim();
      newName = '';
      await loadBuckets(created);
    } catch (err) {
      bucketError = String(err);
    } finally {
      creating = false;
    }
  }

  async function removeBucket(name: string) {
    if (!confirm(`Delete bucket "${name}" and all its objects?`)) return;
    await deleteBucket(name);
    await loadBuckets(name === selected ? '' : selected);
  }
</script>

<div>
  <h2 class="text-2xl font-bold">Object Storage</h2>
  <p class="text-sm text-base-content/60">S3 buckets &amp; objects</p>
</div>

<div class="mt-4 flex gap-4">
  <aside class="w-60 shrink-0">
    <div class="mb-2 flex items-center justify-between">
      <span class="text-xs font-semibold uppercase tracking-wide text-base-content/60">Buckets</span>
      <button class="btn btn-xs btn-primary" onclick={() => bucketDialog.showModal()}>+ New</button>
    </div>
    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each buckets as b (b.name)}
        <li>
          <button class:menu-active={selected === b.name} onclick={() => (selected = String(b.name))}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M3 7h18v12H3zM3 7l2-3h6l2 3" stroke-linejoin="round" />
            </svg>
            <span class="truncate">{b.name}</span>
            <span class="badge badge-xs badge-ghost ml-auto">{b.objects}</span>
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
        Create a bucket to get started.
      </div>
    {:else}
      <div class="mb-2 flex justify-end">
        <button class="btn btn-xs btn-ghost text-error" onclick={() => removeBucket(selected)}>Delete bucket</button>
      </div>
      {#key selected}
        <FileBrowser kind="buckets" container={selected} />
      {/key}
    {/if}
  </section>
</div>

<dialog class="modal" bind:this={bucketDialog}>
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
        <button type="button" class="btn btn-sm btn-ghost" onclick={() => bucketDialog.close()}>Cancel</button>
        <button type="submit" class="btn btn-sm btn-primary" disabled={creating}>
          {#if creating}<span class="loading loading-spinner loading-xs"></span>{/if}
          Create
        </button>
      </div>
    </form>
  </div>
  <form method="dialog" class="modal-backdrop"><button>close</button></form>
</dialog>
