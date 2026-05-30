<script lang="ts">
  import { getRows, uploadImage, type ResourceMeta, type Row } from '../api';
  import ResourceTable from './ResourceTable.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  let rows = $state<Row[]>([]);
  let loading = $state(true);
  let error = $state('');
  let query = $state('');

  function refresh() {
    loading = true;
    getRows('images')
      .then((r) => (rows = r))
      .catch((e) => (error = String(e)))
      .finally(() => (loading = false));
  }
  $effect(refresh);

  let filtered = $derived(
    query.trim() === ''
      ? rows
      : rows.filter((r) =>
          Object.values(r).some((v) => String(v).toLowerCase().includes(query.toLowerCase())),
        ),
  );

  // ---- upload modal ----
  const ALL_ARCHES = ['amd64', 'arm64', 'riscv64', 'loongarch64'];
  const REGISTRIES = ['zot.dc-a', 'zot.dc-b', 'zot.dc-c'];

  let dialog: HTMLDialogElement;
  let kind = $state<'container' | 'raw'>('container');
  let registry = $state(REGISTRIES[0]);
  let repository = $state('');
  let tag = $state('latest');
  let arches = $state<string[]>(['amd64', 'arm64']);
  let files = $state<FileList | null>(null);
  let submitting = $state(false);
  let formError = $state('');

  function openUpload() {
    formError = '';
    dialog.showModal();
  }

  function toggleArch(a: string) {
    arches = arches.includes(a) ? arches.filter((x) => x !== a) : [...arches, a];
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    formError = '';
    if (!repository.trim()) return (formError = 'Repository is required.');
    if (!tag.trim()) return (formError = 'Tag is required.');
    if (arches.length === 0) return (formError = 'Select at least one architecture.');

    const fd = new FormData();
    fd.set('type', kind);
    fd.set('registry', registry);
    fd.set('repository', repository.trim());
    fd.set('tag', tag.trim());
    for (const a of arches) fd.append('arch', a);
    if (files) for (const f of files) fd.append('file', f);

    submitting = true;
    try {
      await uploadImage(fd);
      dialog.close();
      repository = '';
      tag = 'latest';
      files = null;
      refresh();
    } catch (err) {
      formError = String(err);
    } finally {
      submitting = false;
    }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      OCI artifacts across the registries · {filtered.length} of {rows.length}
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter…" bind:value={query} />
    </label>
    <button class="btn btn-sm btn-primary gap-1" onclick={openUpload}>
      <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 16V4M7 9l5-5 5 5M5 20h14" />
      </svg>
      Push artifact
    </button>
  </div>
</div>

<div class="mt-4">
  {#if loading}
    <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
  {:else if error}
    <div class="alert alert-error">{error}</div>
  {:else}
    <ResourceTable columns={meta.columns} rows={filtered} />
  {/if}
</div>

<dialog class="modal" bind:this={dialog}>
  <div class="modal-box max-w-lg">
    <h3 class="text-lg font-bold">Push OCI artifact</h3>
    <p class="mt-1 text-sm text-base-content/60">
      Push a container image, a raw multi-arch disk, or any OCI-wrapped artifact (chart, model, blob) to a registry.
    </p>

    <form class="mt-4 flex flex-col gap-4" onsubmit={submit}>
      <!-- kind -->
      <div class="flex gap-2">
        <label class="flex flex-1 cursor-pointer items-center gap-2 rounded-box border border-base-300 p-3"
          class:border-primary={kind === 'container'}>
          <input type="radio" name="kind" class="radio radio-sm radio-primary" value="container"
            checked={kind === 'container'} onchange={() => (kind = 'container')} />
          <div>
            <div class="font-medium">Container</div>
            <div class="text-xs text-base-content/60">OCI image / manifest list</div>
          </div>
        </label>
        <label class="flex flex-1 cursor-pointer items-center gap-2 rounded-box border border-base-300 p-3"
          class:border-primary={kind === 'raw'}>
          <input type="radio" name="kind" class="radio radio-sm radio-primary" value="raw"
            checked={kind === 'raw'} onchange={() => (kind = 'raw')} />
          <div>
            <div class="font-medium">Raw disk</div>
            <div class="text-xs text-base-content/60">boot-from-OCI, per arch</div>
          </div>
        </label>
      </div>

      <div class="grid grid-cols-3 gap-3">
        <label class="form-control col-span-2">
          <span class="label-text mb-1 text-xs">Repository</span>
          <input class="input input-sm input-bordered" placeholder="team-alpha/web" bind:value={repository} />
        </label>
        <label class="form-control">
          <span class="label-text mb-1 text-xs">Tag</span>
          <input class="input input-sm input-bordered" bind:value={tag} />
        </label>
      </div>

      <label class="form-control">
        <span class="label-text mb-1 text-xs">Registry</span>
        <select class="select select-sm select-bordered" bind:value={registry}>
          {#each REGISTRIES as reg (reg)}<option value={reg}>{reg}</option>{/each}
        </select>
      </label>

      <div>
        <span class="label-text text-xs">Architectures</span>
        <div class="mt-1 flex flex-wrap gap-3">
          {#each ALL_ARCHES as a (a)}
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="checkbox checkbox-sm checkbox-primary"
                checked={arches.includes(a)} onchange={() => toggleArch(a)} />
              <span class="text-sm">{a}</span>
            </label>
          {/each}
        </div>
        {#if kind === 'raw'}
          <p class="mt-1 text-xs text-base-content/50">One raw image per selected architecture.</p>
        {/if}
      </div>

      <label class="form-control">
        <span class="label-text mb-1 text-xs">Files</span>
        <input type="file" multiple class="file-input file-input-sm file-input-bordered"
          onchange={(e) => (files = (e.currentTarget as HTMLInputElement).files)} />
      </label>

      {#if formError}<div class="alert alert-error py-2 text-sm">{formError}</div>{/if}

      <div class="modal-action mt-2">
        <button type="button" class="btn btn-sm btn-ghost" onclick={() => dialog.close()}>Cancel</button>
        <button type="submit" class="btn btn-sm btn-primary" disabled={submitting}>
          {#if submitting}<span class="loading loading-spinner loading-xs"></span>{/if}
          Push to {registry}
        </button>
      </div>
    </form>
  </div>
  <form method="dialog" class="modal-backdrop"><button>close</button></form>
</dialog>
