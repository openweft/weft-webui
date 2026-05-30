<script lang="ts">
  // Create-DNSRecord modal. Loads the zones list and presents them as
  // a picker so the user doesn't paste UUIDs ; type is a fixed
  // dropdown matching common RR types. Source defaults to `static`
  // (operator-managed) ; `auto` records are minted by weft-network's
  // reconciler, never by hand from this modal.
  import { createDNSRecord, getRows, type Row } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let zoneUUID = $state('');
  let leaf = $state('');
  let type = $state<'A' | 'AAAA' | 'CNAME' | 'TXT' | 'SRV' | 'NS' | 'MX'>('A');
  let value = $state('');
  let ttl = $state(60);

  let zones = $state<Row[]>([]);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      getRows('dns-zones').then((rs) => {
        zones = rs;
        if (!zoneUUID && rs[0]) zoneUUID = String(rs[0].uuid);
      });
    } else { dialog?.close(); }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!zoneUUID) { error = 'pick a zone'; return; }
    if (!type || !value.trim()) { error = 'type and value are required'; return; }
    busy = true;
    try {
      await createDNSRecord({
        ZoneUUID: zoneUUID,
        Name: leaf.trim() || '@',
        Type: type,
        Value: value.trim(),
        TTL: ttl,
      });
      onCreated();
      reset();
      open = false;
    } catch (err) { error = String(err); }
    finally { busy = false; }
  }

  function reset() { leaf = ''; value = ''; ttl = 60; type = 'A'; error = ''; }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-2xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">New DNS record</h3>
    <p class="text-sm text-base-content/60">
      Operator-managed record (source = static). Records auto-reconciled
      by weft-network from VMs / LBs are created elsewhere.
    </p>

    <label class="form-control mt-4">
      <span class="label-text text-xs">Zone</span>
      <select class="select select-sm select-bordered" bind:value={zoneUUID}>
        {#each zones as z (z.uuid)}
          <option value={z.uuid}>{z.name}</option>
        {/each}
      </select>
    </label>

    <div class="mt-3 grid gap-3 sm:grid-cols-[2fr_1fr_1fr]">
      <label class="form-control">
        <span class="label-text text-xs">Name (leaf, "@" for apex)</span>
        <input class="input input-sm input-bordered font-mono" placeholder="web" bind:value={leaf} />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Type</span>
        <select class="select select-sm select-bordered" bind:value={type}>
          <option value="A">A</option>
          <option value="AAAA">AAAA</option>
          <option value="CNAME">CNAME</option>
          <option value="SRV">SRV</option>
          <option value="TXT">TXT</option>
          <option value="NS">NS</option>
          <option value="MX">MX</option>
        </select>
      </label>
      <label class="form-control">
        <span class="label-text text-xs">TTL (s)</span>
        <input type="number" min="0" class="input input-sm input-bordered tabular-nums" bind:value={ttl} />
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Value</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder={type === 'SRV' ? '0 33 7443 weft-a.weft.internal.' : '10.10.0.21'}
        bind:value={value} required />
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
