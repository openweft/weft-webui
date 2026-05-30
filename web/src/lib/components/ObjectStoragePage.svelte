<script lang="ts">
  import {
    getRows,
    listObjects,
    getObject,
    createBucket,
    deleteBucket,
    uploadObjects,
    type Row,
    type ObjectListing,
    type ObjectDetail,
  } from '../api';

  // ---- buckets ----
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

  // ---- object browser ----
  let prefix = $state('');
  let listing = $state<ObjectListing | null>(null);
  let listLoading = $state(false);

  // Reload listing whenever the bucket or prefix changes.
  $effect(() => {
    const bucket = selected;
    const p = prefix;
    if (!bucket) {
      listing = null;
      return;
    }
    listLoading = true;
    listObjects(bucket, p)
      .then((l) => (listing = l))
      .catch(() => (listing = null))
      .finally(() => (listLoading = false));
  });

  // Reset to the bucket root when switching buckets.
  function selectBucket(name: string) {
    selected = name;
    prefix = '';
    viewer = null;
  }

  // Breadcrumb segments from the current prefix.
  let crumbs = $derived(
    prefix
      .split('/')
      .filter(Boolean)
      .map((seg, i, arr) => ({ seg, path: arr.slice(0, i + 1).join('/') + '/' })),
  );

  // ---- file viewer ----
  let viewer = $state<ObjectDetail | null>(null);
  let viewerLoading = $state(false);

  async function openObject(key: string) {
    viewerLoading = true;
    try {
      viewer = await getObject(selected, key);
    } finally {
      viewerLoading = false;
    }
  }

  // ---- new bucket modal ----
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
      newName = '';
      await loadBuckets(newName.trim());
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

  // ---- upload object ----
  let uploading = $state(false);
  async function onUpload(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    if (!input.files || input.files.length === 0) return;
    uploading = true;
    try {
      const fd = new FormData();
      fd.set('prefix', prefix);
      for (const f of input.files) fd.append('file', f);
      await uploadObjects(selected, fd);
      input.value = '';
      // refresh listing + bucket counts
      listing = await listObjects(selected, prefix);
      await loadBuckets();
    } finally {
      uploading = false;
    }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Object Storage</h2>
    <p class="text-sm text-base-content/60">S3 buckets &amp; objects</p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- bucket panel -->
  <aside class="w-60 shrink-0">
    <div class="mb-2 flex items-center justify-between">
      <span class="text-xs font-semibold uppercase tracking-wide text-base-content/60">Buckets</span>
      <button class="btn btn-xs btn-primary" onclick={() => bucketDialog.showModal()}>+ New</button>
    </div>
    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each buckets as b (b.name)}
        <li>
          <button class:menu-active={selected === b.name} onclick={() => selectBucket(String(b.name))}>
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

  <!-- browser -->
  <section class="min-w-0 flex-1">
    {#if !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Create a bucket to get started.
      </div>
    {:else}
      <div class="mb-2 flex items-center gap-2">
        <!-- breadcrumb -->
        <div class="breadcrumbs text-sm">
          <ul>
            <li><button class="link-hover" onclick={() => (prefix = '')}>{selected}</button></li>
            {#each crumbs as c (c.path)}
              <li><button class="link-hover" onclick={() => (prefix = c.path)}>{c.seg}</button></li>
            {/each}
          </ul>
        </div>
        <div class="ml-auto flex items-center gap-2">
          <label class="btn btn-sm btn-primary gap-1">
            {#if uploading}<span class="loading loading-spinner loading-xs"></span>{/if}
            Upload
            <input type="file" multiple class="hidden" onchange={onUpload} />
          </label>
          <div class="dropdown dropdown-end">
            <div tabindex="0" role="button" class="btn btn-sm btn-ghost">⋯</div>
            <ul class="menu dropdown-content z-10 w-40 rounded-box bg-base-100 p-1 shadow">
              <li><button class="text-error" onclick={() => removeBucket(selected)}>Delete bucket</button></li>
            </ul>
          </div>
        </div>
      </div>

      <div class="overflow-x-auto rounded-box border border-base-300 bg-base-100">
        <table class="table table-sm">
          <thead>
            <tr><th>Name</th><th>Type</th><th class="text-right">Size</th><th>Modified</th></tr>
          </thead>
          <tbody>
            {#if listLoading}
              <tr><td colspan="4" class="py-8 text-center"><span class="loading loading-spinner"></span></td></tr>
            {:else if listing}
              {#each listing.folders as f (f)}
                <tr class="hover cursor-pointer" onclick={() => (prefix = prefix + f)}>
                  <td class="flex items-center gap-2 font-medium">
                    <svg viewBox="0 0 24 24" class="h-4 w-4 text-warning" fill="none" stroke="currentColor" stroke-width="1.7">
                      <path d="M3 7h6l2 2h10v9H3z" stroke-linejoin="round" />
                    </svg>
                    {f}
                  </td>
                  <td class="text-base-content/50">folder</td>
                  <td></td><td></td>
                </tr>
              {/each}
              {#each listing.objects as o (o.key)}
                <tr class="hover cursor-pointer" onclick={() => openObject(o.key)}>
                  <td class="flex items-center gap-2">
                    <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="1.7">
                      <path d="M6 3h8l4 4v14H6zM14 3v4h4" stroke-linejoin="round" />
                    </svg>
                    {o.name}
                  </td>
                  <td class="font-mono text-xs text-base-content/60">{o.contentType}</td>
                  <td class="text-right tabular-nums">{o.sizeHuman}</td>
                  <td class="text-base-content/70">{o.modified}</td>
                </tr>
              {/each}
              {#if listing.folders.length === 0 && listing.objects.length === 0}
                <tr><td colspan="4" class="py-8 text-center text-base-content/50">Empty.</td></tr>
              {/if}
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<!-- file viewer -->
{#if viewer || viewerLoading}
  <dialog class="modal modal-open">
    <div class="modal-box max-w-3xl">
      {#if viewerLoading}
        <div class="flex justify-center py-10"><span class="loading loading-spinner loading-lg"></span></div>
      {:else if viewer}
        <div class="flex items-start gap-3">
          <div class="min-w-0">
            <h3 class="truncate font-mono text-sm font-bold">{viewer.key}</h3>
            <div class="mt-1 flex flex-wrap gap-2 text-xs text-base-content/60">
              <span class="badge badge-sm badge-ghost">{viewer.contentType}</span>
              <span>{viewer.sizeHuman}</span>
              <span>· {viewer.modified}</span>
            </div>
          </div>
          <button class="btn btn-sm btn-ghost btn-circle ml-auto" onclick={() => (viewer = null)}>✕</button>
        </div>

        <div class="mt-4">
          {#if viewer.previewable}
            <pre class="max-h-96 overflow-auto rounded-box bg-base-200 p-4 text-xs leading-relaxed"><code>{viewer.content}</code></pre>
          {:else}
            <div class="rounded-box border border-dashed border-base-300 p-10 text-center text-base-content/50">
              No inline preview for <span class="font-mono">{viewer.contentType}</span>.
            </div>
          {/if}
        </div>

        <div class="modal-action">
          <button class="btn btn-sm">Download</button>
          <button class="btn btn-sm btn-ghost" onclick={() => (viewer = null)}>Close</button>
        </div>
      {/if}
    </div>
    <button class="modal-backdrop" aria-label="close" onclick={() => (viewer = null)}></button>
  </dialog>
{/if}

<!-- new bucket modal -->
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
