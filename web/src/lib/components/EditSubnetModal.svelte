<script lang="ts">
  // Subnet create / edit modal. Used by NetworksPage's right pane.
  // setSubnet handles upsert by uuid (passed for edit) or by name
  // (for create) ; the parent owns the network key + the refresh.
  import { setSubnet, type Subnet } from '../api';

  let {
    open = $bindable(false),
    networkKey,
    subnet,
    mode,
    onSaved,
  }: {
    open: boolean;
    networkKey: string;
    subnet: Subnet | null;
    mode: 'create' | 'edit';
    onSaved: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let cidr = $state('');
  let gateway = $state('');
  let enabled = $state(true);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      if (mode === 'edit' && subnet) {
        name = subnet.name;
        cidr = subnet.cidr;
        gateway = subnet.gateway ?? '';
        enabled = subnet.enabled !== false;
      } else {
        name = '';
        cidr = '';
        gateway = '';
        enabled = true;
      }
      error = '';
      dialog?.showModal();
    } else {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!name.trim()) { error = 'name is required'; return; }
    if (!cidr.trim()) { error = 'cidr is required'; return; }
    busy = true;
    try {
      await setSubnet(networkKey, {
        uuid: mode === 'edit' ? subnet?.uuid : undefined,
        name: name.trim(),
        cidr: cidr.trim(),
        gateway: gateway.trim() || undefined,
        enabled,
      });
      onSaved();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-lg" onsubmit={submit}>
    <h3 class="text-lg font-bold">{mode === 'create' ? 'Add subnet' : 'Edit subnet'}</h3>
    <p class="mt-1 text-sm text-base-content/60">
      In network <span class="font-mono">{networkKey}</span>.
    </p>

    <div class="mt-4 grid gap-3 sm:grid-cols-[1fr_2fr]">
      <label class="form-control">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered font-mono"
          placeholder="web-tier" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">CIDR</span>
        <input class="input input-sm input-bordered font-mono"
          placeholder="10.10.0.0/24" bind:value={cidr} required />
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Gateway (optional)</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="first usable host if empty" bind:value={gateway} />
    </label>

    <label class="mt-3 flex items-center gap-2 text-sm">
      <input type="checkbox" class="toggle toggle-sm" bind:checked={enabled} />
      Enabled
    </label>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => (open = false)}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        {mode === 'create' ? 'Add' : 'Save'}
      </button>
    </div>
  </form>
</dialog>
