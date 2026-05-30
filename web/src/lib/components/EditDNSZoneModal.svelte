<script lang="ts">
  // Edit-DNSZone modal. Pre-populated from the selected zone row.
  // Mock-friendly PUT /api/dns-zones/{uuid} ; live wiring TBD.
  import { updateDNSZone, type Row } from '../api';

  let {
    open = $bindable(false),
    zone,
    onSaved,
  }: {
    open: boolean;
    zone: Row | null;
    onSaved: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let role = $state<'primary' | 'secondary' | 'forward'>('primary');
  let ttl = $state(60);
  let backend = $state('');
  let pushTarget = $state('');
  let enabled = $state(true);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      if (zone) {
        name = String(zone.name ?? '');
        role = (zone.role === 'secondary' || zone.role === 'forward') ? zone.role : 'primary';
        ttl = Number(zone.ttl_default ?? 60);
        backend = String(zone.backend ?? '');
        pushTarget = String(zone.push_target ?? '');
        enabled = zone.enabled !== false;
        error = '';
      }
      dialog?.showModal();
    } else {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!zone) return;
    error = '';
    if (!name.trim()) { error = 'name is required'; return; }
    busy = true;
    try {
      await updateDNSZone(String(zone.uuid), {
        name: name.trim(),
        role,
        ttl_default: ttl,
        backend: backend.trim(),
        push_target: pushTarget.trim(),
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
    <h3 class="text-lg font-bold">Edit DNS zone</h3>
    <p class="text-sm text-base-content/60">
      Rename, change role, TTL default, backend, or the external push target.
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
        <input type="number" min="0" max="86400"
          class="input input-sm input-bordered tabular-nums" bind:value={ttl} />
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Backend</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="coredns" bind:value={backend} />
    </label>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Push target (RFC-2136, optional)</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="ns1.example (bind9, tsig acme-key)"
        bind:value={pushTarget} />
      <span class="mt-1 text-xs text-base-content/50">
        Leave empty for an internal-only zone.
      </span>
    </label>

    <label class="mt-3 flex items-center gap-2 text-sm">
      <input type="checkbox" class="toggle toggle-sm" bind:checked={enabled} />
      Enabled
      <span class="text-xs text-base-content/50">— disabled zones stay catalogued but stop being served.</span>
    </label>

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
