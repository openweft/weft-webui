<script lang="ts">
  // ObjectStoragePage — master-detail aligned on DNSPage / SharesPage :
  //   left pane  : filter + N/Edit-policy/Delete + bucket list
  //   right pane : metadata badges + FileBrowser for the selected bucket
  //
  // The Edit slot opens the bucket-policy modal (the only mutable
  // surface buckets currently expose beyond create/delete). When a
  // proper bucket-metadata layer lands (versioning, lifecycle, …),
  // this becomes a tabbed modal.
  import { getRows, getMe, createBucket, deleteBucket, type Me, type Row } from '../api';
  import FileBrowser from './FileBrowser.svelte';
  import BucketPolicyModal from './BucketPolicyModal.svelte';

  let buckets = $state<Row[]>([]);
  let selected = $state<string>('');
  let me = $state<Me | null>(null);
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
  let policyOpen = $state(false);
  let bucketDialog: HTMLDialogElement;
  let newName = $state('');
  let creating = $state(false);
  let bucketError = $state('');
  let actionBusy = $state(false);
  let actionErr = $state('');
  let query = $state('');

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
    actionErr = ''; actionBusy = true;
    try {
      await deleteBucket(String(current.name));
      await loadBuckets(String(current.name) === selected ? '' : selected);
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return buckets;
    return buckets.filter((b) => String(b.name).toLowerCase().includes(q));
  });
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Object Storage</h2>
    <p class="text-sm text-base-content/60">
      S3 buckets &amp; objects — pick a bucket to browse its contents.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Master : buckets list -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Buckets</h3>
      <span class="ml-auto text-xs text-base-content/50">{filtered.length} of {buckets.length}</span>
    </div>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter buckets…" bind:value={query} />
    </label>

    {#if canCreate}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-sm btn-primary gap-1" onclick={() => (createOpen = true)}
          title="Create a new bucket">
          <span class="text-base leading-none">+</span> New
        </button>
        <button class="btn btn-sm btn-warning gap-1"
          disabled={!current || actionBusy}
          onclick={() => (policyOpen = true)}
          title={current ? `Edit policy for "${current.name}"` : 'Select a bucket to edit'}>
          Edit policy
        </button>
        <button class="btn btn-sm btn-error gap-1"
          disabled={!current || actionBusy}
          onclick={delSelected}
          title={current ? `Delete "${current.name}"` : 'Select a bucket to delete'}>
          {#if actionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Delete
        </button>
      </div>
    {/if}

    {#if actionErr}<div class="alert alert-error py-2 text-sm">{actionErr}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filtered as b (b.name)}
        <li>
          <button class:menu-active={selected === b.name} onclick={() => (selected = String(b.name))}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M3 7h18v12H3zM3 7l2-3h6l2 3" stroke-linejoin="round" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium">{b.name}</div>
              <div class="text-[10px] text-base-content/50">
                s3 · {b.objects} objects · {b.size}
              </div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {buckets.length === 0 ? 'No buckets yet.' : 'No buckets match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <!-- Detail : object browser -->
  <section class="min-w-0 flex-1 flex flex-col gap-2">
    {#if !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a bucket{canCreate ? ' or create one to get started' : ''}.
      </div>
    {:else if current}
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">
            Objects in <span class="font-mono normal-case text-base-content">{current.name}</span>
          </h3>
          <p class="text-xs text-base-content/50">
            <span class="badge badge-xs badge-ghost">s3</span>
            {#if current.project}project {current.project} · {/if}
            {current.objects} objects · {current.size}
            {#if current.created}· created {current.created}{/if}
          </p>
        </div>
      </div>
      {#key selected}
        <FileBrowser kind="buckets" container={selected} />
      {/key}
    {/if}
  </section>
</div>

{#if selected}
  <BucketPolicyModal bucket={selected} bind:open={policyOpen} />
{/if}

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
