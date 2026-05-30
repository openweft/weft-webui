<script lang="ts">
  // Allocate-floating-IP modal. Project comes from the session scope.
  //
  // The network picker reads /api/resources/networks once on open and
  // surfaces every network the user can see — typically the edge
  // network for public ingress, but a tenant might also expose
  // additional public pools. Mock-mode returns 503 (the address would
  // be meaningless without a real pool to draw from).
  import { allocateFloatingIP, getMe, getRows, type Row } from '../api';
  import Combobox from './Combobox.svelte';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let network = $state('edge');
  let networks = $state<Row[]>([]);
  let project = $state('');
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      getMe().then((u) => (project = u.project));
      getRows('networks').then((rs) => {
        networks = rs;
        if (!rs.find((r) => r.name === network) && rs[0]) {
          network = String(rs[0].name);
        }
      });
    } else {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!project) {
      error = 'no project in scope — pick one in the Topbar';
      return;
    }
    if (!network) {
      error = 'network is required';
      return;
    }
    busy = true;
    try {
      await allocateFloatingIP({ Network: network });
      onCreated();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }
  function cancel() { open = false; error = ''; }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submit}>
    <h3 class="text-lg font-bold">Allocate floating IP</h3>
    <p class="text-sm text-base-content/60">
      Reserve the next available address on the chosen network for
      project <span class="font-mono">{project || '—'}</span>.
      Maps to a VM or load-balancer via the row action after allocation.
    </p>

    <div class="form-control mt-4">
      <span class="label-text text-xs">Network</span>
      <Combobox
        items={networks}
        bind:value={network}
        getId={(n) => String(n.name)}
        getLabel={(n) => String(n.name)}
        getSub={(n) => String(n.cidr ?? '')}
        placeholder="Type to filter networks…"
      />
    </div>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={cancel}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Allocate
      </button>
    </div>
  </form>
</dialog>
