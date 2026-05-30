<script lang="ts">
  // Network create / edit modal. Two tabs : General (rename +
  // description) and DNS (resolvers list). Replaces the standalone
  // NetworkDrawer now that NetworksPage owns the master-detail
  // layout and edits flow through a modal like DNSPage / SecurityPage.
  import {
    getNetworkMetadata, setNetworkMetadata, renameNetworkByKey,
    type NetworkMetadata, type Row,
  } from '../api';

  let {
    open = $bindable(false),
    network,
    onSaved,
  }: {
    open: boolean;
    network: Row | null;
    onSaved: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  type Tab = 'general' | 'dns';
  let tab = $state<Tab>('general');

  let originalName = $state('');
  let editName = $state('');
  let editDescription = $state('');
  let editDNS = $state<string[]>([]);
  let metadata = $state<NetworkMetadata | null>(null);
  let loading = $state(true);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open && network) {
      tab = 'general';
      originalName = String(network.name ?? '');
      editName = originalName;
      loading = true;
      error = '';
      getNetworkMetadata(originalName)
        .then((m) => {
          metadata = m;
          editDescription = m.description ?? '';
          editDNS = [...(m.dns_servers ?? [])];
        })
        .catch((e) => (error = String(e)))
        .finally(() => (loading = false));
      dialog?.showModal();
    } else if (!open) {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!network) return;
    error = '';
    if (!editName.trim()) { error = 'name is required'; return; }
    busy = true;
    try {
      let currentKey = originalName;
      if (editName.trim() !== originalName) {
        await renameNetworkByKey(currentKey, editName.trim());
        currentKey = editName.trim();
      }
      await setNetworkMetadata(currentKey, {
        description: editDescription,
        dns_servers: editDNS.map((s) => s.trim()).filter((s) => s !== ''),
      });
      onSaved();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }

  function addDNS()              { editDNS = [...editDNS, '']; }
  function removeDNS(i: number)  { editDNS = editDNS.filter((_, idx) => idx !== i); }
  function updateDNS(i: number, v: string) { editDNS = editDNS.map((x, idx) => idx === i ? v : x); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-2xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">Edit network</h3>

    <div role="tablist" class="tabs tabs-border mt-3">
      <button type="button" role="tab" class="tab" class:tab-active={tab === 'general'}
        onclick={() => (tab = 'general')}>General</button>
      <button type="button" role="tab" class="tab" class:tab-active={tab === 'dns'}
        onclick={() => (tab = 'dns')}>DNS</button>
    </div>

    {#if loading}
      <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
    {:else if tab === 'general'}
      <label class="form-control mt-4">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered font-mono"
          bind:value={editName} required />
        <span class="mt-1 text-xs text-base-content/50">
          Attached resources reference the network by uuid ; this is the dashboard label.
        </span>
      </label>

      <label class="form-control mt-3">
        <span class="label-text text-xs">Description</span>
        <textarea class="textarea textarea-sm textarea-bordered" rows="4"
          placeholder="What this network is for, ownership, isolation notes…"
          bind:value={editDescription}></textarea>
      </label>

      {#if network}
        <dl class="mt-3 grid grid-cols-2 gap-2 text-xs">
          <div><dt class="text-base-content/50">CIDR</dt><dd class="font-mono">{network.cidr}</dd></div>
          <div><dt class="text-base-content/50">Type</dt><dd class="font-mono">{network.type}</dd></div>
          <div><dt class="text-base-content/50">Gateway</dt><dd class="font-mono">{network.gateway}</dd></div>
          <div><dt class="text-base-content/50">Created</dt><dd class="font-mono">{network.created}</dd></div>
        </dl>
      {/if}
    {:else}
      <!-- DNS tab -->
      <p class="mt-4 text-xs text-base-content/60">
        DNS resolvers handed to instances on this network via cloud-init / DHCP.
        Empty list = inherit the cluster default.
      </p>

      <div class="mt-3 rounded-box border border-base-300 bg-base-100">
        <table class="table table-sm">
          <thead><tr><th class="w-10">#</th><th>Resolver (IP)</th><th class="w-0"></th></tr></thead>
          <tbody>
            {#if editDNS.length === 0}
              <tr><td colspan="3" class="py-4 text-center text-base-content/50">
                No resolvers — click "+ Add resolver" below.
              </td></tr>
            {:else}
              {#each editDNS as v, i (i)}
                <tr>
                  <td class="text-base-content/50">{i + 1}</td>
                  <td>
                    <input class="input input-xs input-bordered w-full font-mono"
                      placeholder="10.0.0.53" value={v}
                      oninput={(e) => updateDNS(i, (e.currentTarget as HTMLInputElement).value)} />
                  </td>
                  <td>
                    <button type="button" class="btn btn-ghost btn-xs text-error"
                      onclick={() => removeDNS(i)} aria-label="Remove">✕</button>
                  </td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>

      <button type="button" class="btn btn-sm btn-ghost gap-1 mt-2" onclick={addDNS}>
        <span class="text-base leading-none">+</span> Add resolver
      </button>
    {/if}

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => (open = false)}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Save
      </button>
    </div>
  </form>
</dialog>
