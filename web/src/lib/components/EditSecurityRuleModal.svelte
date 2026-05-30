<script lang="ts">
  // EditSecurityRuleModal — used for both "+ New rule" and "Edit
  // selected rule" inside SecurityPage. The parent owns the full
  // rule list and the PUT call ; this modal just collects the
  // patched rule and hands it back via onSave.
  import type { SecurityRule } from '../api';

  let {
    open = $bindable(false),
    rule,
    mode,
    onSave,
  }: {
    open: boolean;
    rule: SecurityRule | null;
    mode: 'create' | 'edit';
    onSave: (r: SecurityRule) => void | Promise<void>;
  } = $props();

  let dialog: HTMLDialogElement;

  let direction = $state<'ingress' | 'egress'>('ingress');
  let protocol = $state<'tcp' | 'udp' | 'icmp' | 'any'>('tcp');
  let portMin = $state(0);
  let portMax = $state(0);
  let portsAny = $state(true);
  let remoteCidr = $state('0.0.0.0/0');
  let remoteGroup = $state('');
  let enabled = $state(true);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      if (mode === 'edit' && rule) {
        direction = rule.direction;
        protocol = rule.protocol;
        portMin = rule.port_min;
        portMax = rule.port_max;
        portsAny = portMin === 0 && portMax === 0;
        remoteCidr = rule.remote_cidr || '';
        remoteGroup = rule.remote_group_uuid || '';
        enabled = rule.enabled !== false; // missing = enabled
      } else {
        direction = 'ingress';
        protocol = 'tcp';
        portMin = 0;
        portMax = 0;
        portsAny = true;
        remoteCidr = '0.0.0.0/0';
        remoteGroup = '';
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
    if (!portsAny) {
      if (portMin < 1 || portMax < 1 || portMin > 65535 || portMax > 65535) {
        error = 'Ports must be 1..65535'; return;
      }
      if (portMin > portMax) { error = 'port_min must be ≤ port_max'; return; }
    }
    if (!remoteCidr.trim() && !remoteGroup.trim()) {
      error = 'Remote CIDR or remote group is required'; return;
    }
    busy = true;
    try {
      await onSave({
        direction,
        protocol,
        port_min: portsAny ? 0 : portMin,
        port_max: portsAny ? 0 : portMax,
        remote_cidr: remoteCidr.trim(),
        remote_group_uuid: remoteGroup.trim(),
        enabled,
      });
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-lg" onsubmit={submit}>
    <h3 class="text-lg font-bold">
      {mode === 'create' ? 'Add rule' : 'Edit rule'}
    </h3>

    <div class="mt-4 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">Direction</span>
        <select class="select select-sm select-bordered" bind:value={direction}>
          <option value="ingress">ingress</option>
          <option value="egress">egress</option>
        </select>
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Protocol</span>
        <select class="select select-sm select-bordered" bind:value={protocol}>
          <option value="tcp">tcp</option>
          <option value="udp">udp</option>
          <option value="icmp">icmp</option>
          <option value="any">any</option>
        </select>
      </label>
    </div>

    <div class="mt-3 grid gap-3 sm:grid-cols-[auto_1fr_1fr] items-end">
      <label class="form-control">
        <span class="label-text text-xs">Ports</span>
        <label class="flex items-center gap-2 text-xs h-8">
          <input type="checkbox" class="checkbox checkbox-xs" bind:checked={portsAny} />
          any
        </label>
      </label>
      <label class="form-control" class:opacity-50={portsAny}>
        <span class="label-text text-xs">port_min</span>
        <input type="number" min="0" max="65535" disabled={portsAny}
          class="input input-sm input-bordered tabular-nums" bind:value={portMin} />
      </label>
      <label class="form-control" class:opacity-50={portsAny}>
        <span class="label-text text-xs">port_max</span>
        <input type="number" min="0" max="65535" disabled={portsAny}
          class="input input-sm input-bordered tabular-nums" bind:value={portMax} />
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Remote CIDR</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="0.0.0.0/0" bind:value={remoteCidr} />
      <span class="mt-1 text-xs text-base-content/50">
        Source for ingress, destination for egress.
      </span>
    </label>

    <label class="form-control mt-3">
      <span class="label-text text-xs">— OR — Remote group (uuid)</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="cross-group reference"
        bind:value={remoteGroup} />
      <span class="mt-1 text-xs text-base-content/50">
        Use for SG-to-SG references (e.g. "web → db"). Either CIDR or remote group, not both.
      </span>
    </label>

    <label class="mt-3 flex items-center gap-2 text-sm">
      <input type="checkbox" class="toggle toggle-sm" bind:checked={enabled} />
      Enabled
      <span class="text-xs text-base-content/50">— disabled rules stay in the list but the data plane skips them.</span>
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
