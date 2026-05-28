<script lang="ts">
  // Create-DNSZone modal. `role` picks authoritative / secondary /
  // forward. push_target is the external BIND descriptor for
  // RFC-2136 NS updates ; leave empty for internal-only zones.
  import { createDNSZone } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let role = $state<'primary' | 'secondary' | 'forward'>('primary');
  let ttl = $state(60);
  let pushTarget = $state('');

  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) dialog?.showModal();
    else dialog?.close();
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!name.trim()) { error = 'name is required'; return; }
    busy = true;
    try {
      await createDNSZone({
        Name: name.trim(), Role: role,
        TTLDefault: ttl, PushTarget: pushTarget.trim(),
      });
      onCreated();
      reset();
      open = false;
    } catch (err) { error = String(err); }
    finally { busy = false; }
  }

  function reset() { name = ''; role = 'primary'; ttl = 60; pushTarget = ''; error = ''; }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-lg" onsubmit={submit}>
    <h3 class="text-lg font-bold">New DNS zone</h3>
    <p class="text-sm text-base-content/60">
      Served by the per-DC CoreDNS microVMs. A push target writes the
      zone to an external BIND via RFC-2136 NS updates.
    </p>

    <label class="form-control mt-4">
      <span class="label-text text-xs">Zone name</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="acme.weft.internal" bind:value={name} required />
    </label>

    <div class="mt-3 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">Role</span>
        <select class="select select-sm select-bordered" bind:value={role}>
          <option value="primary">primary (authoritative)</option>
          <option value="secondary">secondary</option>
          <option value="forward">forward</option>
        </select>
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Default TTL (s)</span>
        <input type="number" min="0" class="input input-sm input-bordered tabular-nums" bind:value={ttl} />
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Push target (optional, RFC-2136)</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder='ns1.acme.example (bind9, tsig acme-key)'
        bind:value={pushTarget} />
    </label>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={cancel}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Create
      </button>
    </div>
  </form>
</dialog>
