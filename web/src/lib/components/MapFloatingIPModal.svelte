<script lang="ts">
  // Map a floating IP to a microVM or load-balancer VIP. The row
  // dropdown opens this modal pre-filled with the FIP uuid + address ;
  // the operator picks a target kind + name.
  import { mapFloatingIP } from '../api';

  let {
    fip,
    onClose,
    onMapped,
  }: {
    fip: { uuid: string; address: string } | null;
    onClose: () => void;
    onMapped: () => void;
  } = $props();

  let dialog: HTMLDialogElement;
  let kind = $state<'vm' | 'lb'>('vm');
  let target = $state('');
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (fip) {
      dialog?.showModal();
      kind = 'vm';
      target = '';
      error = '';
    } else {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!fip) return;
    if (!target.trim()) {
      error = 'target name is required';
      return;
    }
    busy = true;
    error = '';
    try {
      await mapFloatingIP(fip.uuid, kind, target.trim());
      onMapped();
      onClose();
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }
</script>

<dialog class="modal" bind:this={dialog} onclose={onClose}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submit}>
    <h3 class="text-lg font-bold">Map floating IP</h3>
    {#if fip}
      <p class="text-sm text-base-content/60">
        Wire <span class="font-mono">{fip.address}</span> to a microVM
        or load-balancer VIP. weft-network programs the NAT / Caddy
        listener accordingly.
      </p>
    {/if}

    <div class="mt-4 grid grid-cols-[6rem_1fr] items-center gap-3">
      <span class="text-xs font-medium">Target kind</span>
      <select class="select select-sm select-bordered" bind:value={kind}>
        <option value="vm">microVM</option>
        <option value="lb">load balancer</option>
      </select>

      <span class="text-xs font-medium">Target name</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder={kind === 'vm' ? 'web-1' : 'web-prod'}
        bind:value={target} required />
    </div>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={onClose}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Map
      </button>
    </div>
  </form>
</dialog>
