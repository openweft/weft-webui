<script lang="ts">
  // Map a floating IP to a microVM or load-balancer VIP. The row
  // dropdown opens this modal pre-filled with the FIP uuid + address ;
  // the operator picks a target kind + name + optional anti-DDoS PPS cap.
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
  let rateLimitPps = $state(0);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (fip) {
      dialog?.showModal();
      kind = 'vm';
      target = '';
      rateLimitPps = 0;
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
      await mapFloatingIP(fip.uuid, kind, target.trim(), rateLimitPps);
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

    <div class="mt-4">
      <div class="flex items-center justify-between text-xs">
        <span class="font-medium">Inbound rate limit</span>
        <span class="font-mono text-base-content/70">
          {rateLimitPps === 0 ? 'unlimited' : `${rateLimitPps.toLocaleString()} pps`}
        </span>
      </div>
      <input type="range" min="0" max="100000" step="1000"
        class="range range-xs range-primary mt-1"
        bind:value={rateLimitPps} />
      <div class="mt-1 flex justify-between text-[0.65rem] text-base-content/50">
        <span>0 (none)</span>
        <span>10k</span>
        <span>50k</span>
        <span>100k</span>
      </div>
      <p class="mt-2 text-xs text-base-content/60">
        Anti-DDoS cap installed as an nftables limit on inbound
        packets to this floating IP. 0 = no limit.
      </p>
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
