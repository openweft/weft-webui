<script lang="ts">
  // Edit-DNSRecord modal. Pre-populated from the selected record row.
  // Mock-friendly PUT /api/dns-records/{uuid}.
  //
  // Zone is shown read-only — moving a record to a different zone is
  // delete+recreate, not a single PUT, so we don't expose it here.
  // Source is also read-only (the reconciler owns "auto" records ;
  // operator edits to those are clobbered on the next reconcile).
  import { updateDNSRecord, type Row } from '../api';

  const RECORD_TYPES = ['A', 'AAAA', 'CNAME', 'TXT', 'SRV', 'NS', 'MX', 'PTR'] as const;
  type RecordType = typeof RECORD_TYPES[number];

  let {
    open = $bindable(false),
    record,
    onSaved,
  }: {
    open: boolean;
    record: Row | null;
    onSaved: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let type = $state<RecordType>('A');
  let value = $state('');
  let ttl = $state(60);
  let enabled = $state(true);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      if (record) {
        name = String(record.name ?? '');
        const t = String(record.type ?? 'A') as RecordType;
        type = (RECORD_TYPES as readonly string[]).includes(t) ? t : 'A';
        value = String(record.value ?? '');
        ttl = Number(record.ttl ?? 60);
        enabled = record.enabled !== false;
        error = '';
      }
      dialog?.showModal();
    } else {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!record) return;
    error = '';
    if (!name.trim()) { error = 'name is required'; return; }
    if (!value.trim()) { error = 'value is required'; return; }
    busy = true;
    try {
      await updateDNSRecord(String(record.uuid), {
        name: name.trim(),
        type,
        value: value.trim(),
        ttl,
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
    <h3 class="text-lg font-bold">Edit DNS record</h3>
    {#if record}
      <p class="text-sm text-base-content/60">
        Inside zone <span class="font-mono">{record.zone}</span>
        {#if record.source === 'auto'}
          · <span class="badge badge-xs badge-warning">auto</span>
          edits to auto-reconciled records are clobbered on the next reconcile.
        {/if}
      </p>
    {/if}

    <div class="mt-4 grid gap-3 sm:grid-cols-[2fr_1fr]">
      <label class="form-control">
        <span class="label-text text-xs">Name (leaf, or @ for apex)</span>
        <input class="input input-sm input-bordered font-mono"
          placeholder="web" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Type</span>
        <select class="select select-sm select-bordered" bind:value={type}>
          {#each RECORD_TYPES as t (t)}<option value={t}>{t}</option>{/each}
        </select>
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Value</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="10.0.0.10" bind:value={value} required />
      <span class="mt-1 text-xs text-base-content/50">
        IP for A/AAAA · target FQDN for CNAME/NS · "priority weight port target" for SRV.
      </span>
    </label>

    <label class="form-control mt-3 max-w-[12rem]">
      <span class="label-text text-xs">TTL (s)</span>
      <input type="number" min="0" max="86400"
        class="input input-sm input-bordered tabular-nums" bind:value={ttl} />
    </label>

    <label class="mt-3 flex items-center gap-2 text-sm">
      <input type="checkbox" class="toggle toggle-sm" bind:checked={enabled} />
      Enabled
      <span class="text-xs text-base-content/50">— disabled records remain in the zone but aren't served.</span>
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
