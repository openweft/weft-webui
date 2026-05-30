<script lang="ts">
  import {
    browse,
    readEntry,
    uploadEntries,
    type StorageKind,
    type ObjectListing,
    type ObjectDetail,
  } from '../api';

  // Wrap this in {#key container} so a fresh container starts at its root.
  let { kind, container }: { kind: StorageKind; container: string } = $props();

  let prefix = $state('');
  let listing = $state<ObjectListing | null>(null);
  let listLoading = $state(false);
  let viewer = $state<ObjectDetail | null>(null);
  let viewerLoading = $state(false);
  let uploading = $state(false);

  $effect(() => {
    const p = prefix;
    listLoading = true;
    browse(kind, container, p)
      .then((l) => (listing = l))
      .catch(() => (listing = null))
      .finally(() => (listLoading = false));
  });

  let crumbs = $derived(
    prefix
      .split('/')
      .filter(Boolean)
      .map((seg, i, arr) => ({ seg, path: arr.slice(0, i + 1).join('/') + '/' })),
  );

  async function openObject(key: string) {
    viewerLoading = true;
    try {
      viewer = await readEntry(kind, container, key);
    } finally {
      viewerLoading = false;
    }
  }

  async function onUpload(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    if (!input.files || input.files.length === 0) return;
    uploading = true;
    try {
      const fd = new FormData();
      fd.set('prefix', prefix);
      for (const f of input.files) fd.append('file', f);
      await uploadEntries(kind, container, fd);
      input.value = '';
      listing = await browse(kind, container, prefix);
    } finally {
      uploading = false;
    }
  }
</script>

<div class="mb-2 flex items-center gap-2">
  <div class="breadcrumbs text-sm">
    <ul>
      <li><button class="link-hover font-medium" onclick={() => (prefix = '')}>{container}</button></li>
      {#each crumbs as c (c.path)}
        <li><button class="link-hover" onclick={() => (prefix = c.path)}>{c.seg.replace(/\/$/, '')}</button></li>
      {/each}
    </ul>
  </div>
  <label class="btn btn-sm btn-primary ml-auto gap-1">
    {#if uploading}<span class="loading loading-spinner loading-xs"></span>{/if}
    Upload
    <input type="file" multiple class="hidden" onchange={onUpload} />
  </label>
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
              {f.replace(/\/$/, '')}
            </td>
            <td class="text-base-content/50">folder</td><td></td><td></td>
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
              <span>{viewer.sizeHuman}</span><span>· {viewer.modified}</span>
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
