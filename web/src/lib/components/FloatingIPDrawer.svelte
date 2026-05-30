<script lang="ts">
  // FloatingIPDrawer — specialized drawer for floating-ips. Mirrors
  // EditableDrawer's name + description editor but adds Map / Unmap
  // buttons in the header (the FIP's distinctive verbs : a FIP is
  // useful only once mapped to a VM or LB). Replaces the row-dropdown
  // Map / Unmap that used to live in the ResourceTable Actions
  // column.
  //
  // The Map flow opens the existing MapFloatingIPModal owned by
  // ResourcePage ; the drawer bubbles "user wants to map" via
  // onMapRequest. Unmap fires inline since it doesn't need a target.
  import {
    getEditableMetadata, setEditableMetadata,
    unmapFloatingIP,
    type EditableMetadata, type Row,
  } from '../api';

  let {
    row,
    onClose,
    onChanged,
    onMapRequest,
  }: {
    row: Row;
    onClose: () => void;
    onChanged: () => void;
    onMapRequest: (uuid: string, address: string) => void;
  } = $props();

  let uuid = $derived(String(row.uuid));
  let address = $derived(String(row.address ?? '—'));
  let mappedTo = $derived(typeof row.mapped_to === 'string' ? row.mapped_to : '');
  let mapped = $derived(mappedTo !== '');
  let network = $derived(typeof row.network === 'string' ? row.network : '—');
  let status = $derived(String(row.status ?? '—'));

  let metadata = $state<EditableMetadata | null>(null);
  let editDescription = $state('');
  let loading = $state(true);
  let loadErr = $state('');
  let saveBusy = $state(false);
  let saveErr = $state('');
  let saveOk = $state(false);

  let mapBusy = $state(false);
  let mapErr = $state('');

  async function refresh() {
    loading = true; loadErr = '';
    try {
      const m = await getEditableMetadata('floating-ips', uuid);
      metadata = m;
      editDescription = m.description ?? '';
    } catch (e) {
      loadErr = String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => { uuid; refresh(); });

  let descriptionDirty = $derived(metadata !== null && editDescription !== (metadata?.description ?? ''));

  async function saveDescription() {
    if (!descriptionDirty) return;
    saveBusy = true; saveErr = ''; saveOk = false;
    try {
      await setEditableMetadata('floating-ips', uuid, editDescription);
      saveOk = true;
      onChanged();
    } catch (e) {
      saveErr = String(e);
    } finally {
      saveBusy = false;
    }
  }

  async function doUnmap() {
    if (!mapped) return;
    if (!confirm(`Unmap ${address} from ${mappedTo} ?`)) return;
    mapBusy = true; mapErr = '';
    try {
      await unmapFloatingIP(uuid);
      onChanged();
    } catch (e) {
      mapErr = String(e);
    } finally {
      mapBusy = false;
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
      <h2 class="text-lg font-bold font-mono">{address}</h2>
      <p class="text-xs text-base-content/60">
        {network} · status {status}
        · {mapped ? `mapped to ${mappedTo}` : 'unmapped'}
      </p>
    </div>
    <div class="ml-auto flex items-center gap-2">
      {#if mapped}
        <button class="btn btn-sm btn-warning gap-1" disabled={mapBusy} onclick={doUnmap}
          title={`Unmap ${address} from ${mappedTo}`}>
          {#if mapBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Unmap
        </button>
      {:else}
        <button class="btn btn-sm btn-primary gap-1" disabled={mapBusy}
          onclick={() => onMapRequest(uuid, address)}
          title={`Map ${address} to a VM or load balancer`}>
          Map to…
        </button>
      {/if}
      <button class="btn btn-sm btn-ghost" aria-label="Close" onclick={onClose}>✕</button>
    </div>
  </header>

  <div class="min-h-0 flex-1 overflow-y-auto p-5">
    {#if loadErr}<div class="alert alert-error py-2 text-sm">{loadErr}</div>{/if}
    {#if mapErr}<div class="alert alert-error py-2 text-sm">{mapErr}</div>{/if}

    {#if loading}
      <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
    {:else}
      <div class="grid gap-3">
        <label class="form-control">
          <span class="label-text mb-1 text-xs">Description</span>
          <textarea class="textarea textarea-sm textarea-bordered" rows="5"
            placeholder="What this floating IP is for, ownership, intent…"
            bind:value={editDescription}></textarea>
        </label>

        <dl class="mt-2 grid grid-cols-2 gap-2 text-xs">
          <div><dt class="text-base-content/50">Address</dt><dd class="font-mono">{address}</dd></div>
          <div><dt class="text-base-content/50">Network</dt><dd class="font-mono">{network}</dd></div>
          <div><dt class="text-base-content/50">Mapped to</dt><dd class="font-mono">{mappedTo || '—'}</dd></div>
          <div><dt class="text-base-content/50">Status</dt><dd class="font-mono">{status}</dd></div>
          {#if metadata?.updated_by}
            <div class="col-span-2"><dt class="text-base-content/50">Last edit</dt>
              <dd class="font-mono">{fmtUpdated(metadata.updated_at)} · {metadata.updated_by}</dd></div>
          {/if}
        </dl>

        {#if saveErr}<div class="alert alert-error py-2 text-sm">{saveErr}</div>{/if}
        {#if saveOk}<div class="alert alert-success py-2 text-sm">Saved.</div>{/if}

        <div class="mt-2 flex">
          <button class="ml-auto btn btn-sm btn-primary"
            disabled={!descriptionDirty || saveBusy}
            onclick={saveDescription}>
            {#if saveBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
            Save
          </button>
        </div>
      </div>
    {/if}
  </div>
</aside>
