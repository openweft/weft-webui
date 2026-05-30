<script lang="ts">
  // Generic drawer for resources whose editable surface is just
  // { name (rename) + description }. Used by Routers, Floating IPs,
  // and Scheduling Rules. Heavier resources (volumes, networks,
  // microvms) carry their own typed drawers.
  import {
    getEditableMetadata, setEditableMetadata, renameEditableRow,
    type EditableMetadata, type Row,
  } from '../api';

  type EditableResource = 'routers' | 'floating-ips' | 'scheduling-rules';

  let {
    resource,
    row,
    title,
    subtitle,
    onClose,
    onChanged,
  }: {
    resource: EditableResource;
    row: Row;
    title: string;
    subtitle: string;
    onClose: () => void;
    onChanged: () => void;
  } = $props();

  // Whether the row has a renamable `name` field. Floating IPs are
  // identified by address ; the Name input is hidden when no name is
  // present so the drawer reduces to a description-only editor.
  let hasName = $derived(typeof row.name === 'string' && row.name !== '');
  let key = $derived(String(row.name ?? row.uuid ?? ''));
  let editName = $state('');
  let editDescription = $state('');
  let metadata = $state<EditableMetadata | null>(null);
  let loading = $state(true);
  let loadErr = $state('');
  let saveBusy = $state(false);
  let saveErr = $state('');
  let saveOk = $state(false);

  async function refresh() {
    loading = true; loadErr = '';
    try {
      const m = await getEditableMetadata(resource, key);
      metadata = m;
      editDescription = m.description ?? '';
    } catch (e) {
      loadErr = String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    key; // dep
    editName = String(row.name ?? '');
    refresh();
  });

  let nameDirty = $derived(hasName && editName !== String(row.name ?? ''));
  let descriptionDirty = $derived(metadata !== null && editDescription !== (metadata?.description ?? ''));

  async function save() {
    if (!nameDirty && !descriptionDirty) return;
    saveBusy = true; saveErr = ''; saveOk = false;
    try {
      let currentKey = key;
      if (nameDirty) {
        const newName = editName.trim();
        if (!newName) throw new Error('name is required');
        await renameEditableRow(resource, currentKey, newName);
        currentKey = newName;
      }
      if (descriptionDirty || nameDirty) {
        await setEditableMetadata(resource, currentKey, editDescription);
      }
      saveOk = true;
      onChanged();
    } catch (e) {
      saveErr = String(e);
    } finally {
      saveBusy = false;
    }
  }

  function fmtUpdated(ts: string): string {
    if (!ts) return '—';
    return ts.slice(0, 19).replace('T', ' ');
  }
</script>

<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-3xl flex-col bg-base-100 shadow-2xl">
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div>
      <h2 class="text-lg font-bold">{title}</h2>
      <p class="text-xs text-base-content/60">{subtitle}</p>
    </div>
    <button class="ml-auto btn btn-sm btn-ghost" aria-label="Close" onclick={onClose}>✕</button>
  </header>

  <div class="min-h-0 flex-1 overflow-y-auto p-5">
    {#if loadErr}
      <div class="alert alert-error py-2 text-sm">{loadErr}</div>
    {/if}

    {#if loading}
      <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
    {:else}
      <div class="grid gap-3">
        {#if hasName}
          <label class="form-control">
            <span class="label-text mb-1 text-xs">Name</span>
            <input class="input input-sm input-bordered font-mono" bind:value={editName} />
            <span class="mt-1 text-xs text-base-content/50">
              Attached resources reference this by uuid ; the name is the dashboard label.
            </span>
          </label>
        {/if}

        <label class="form-control">
          <span class="label-text mb-1 text-xs">Description</span>
          <textarea class="textarea textarea-sm textarea-bordered" rows="5"
            placeholder="Free-form notes, ownership, intent…"
            bind:value={editDescription}></textarea>
        </label>

        {#if metadata?.updated_by}
          <div class="text-xs text-base-content/50">
            Last edit : <span class="font-mono">{fmtUpdated(metadata.updated_at)}</span>
            · <span class="font-mono">{metadata.updated_by}</span>
          </div>
        {/if}

        {#if saveErr}<div class="alert alert-error py-2 text-sm">{saveErr}</div>{/if}
        {#if saveOk}<div class="alert alert-success py-2 text-sm">Saved.</div>{/if}

        <div class="mt-2 flex">
          <button class="ml-auto btn btn-sm btn-primary"
            disabled={(!nameDirty && !descriptionDirty) || saveBusy}
            onclick={save}>
            {#if saveBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
            Save
          </button>
        </div>
      </div>
    {/if}
  </div>
</aside>
