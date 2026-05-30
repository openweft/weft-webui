<script lang="ts">
  // Create-network modal. Project from session scope.
  //
  // Type picker covers the three weft network kinds : `nat` (default
  // egress with NAT towards the host), `overlay` (private tenant
  // mesh), `wireguard` (mgmt overlay between weft agents).
  import { createNetwork, getMe } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let cidr = $state('10.30.0.0/16');
  let gateway = $state('');
  let type = $state<'nat' | 'overlay' | 'wireguard'>('overlay');
  let dnsServers = $state(''); // comma-separated input → []string

  let project = $state('');
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      getMe().then((u) => (project = u.project));
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
    if (!name.trim() || !cidr.trim()) {
      error = 'name and CIDR are required';
      return;
    }
    busy = true;
    try {
      await createNetwork({
        name: name.trim(),
        cidr: cidr.trim(),
        gateway: gateway.trim(),
        type,
        dns_servers: dnsServers.split(',').map((s) => s.trim()).filter(Boolean),
      });
      onCreated();
      reset();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }

  function reset() {
    name = ''; cidr = '10.30.0.0/16'; gateway = '';
    type = 'overlay'; dnsServers = ''; error = '';
  }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-lg" onsubmit={submit}>
    <h3 class="text-lg font-bold">New network</h3>
    <p class="text-sm text-base-content/60">
      In project <span class="font-mono">{project || '—'}</span>.
    </p>

    <label class="form-control mt-4">
      <span class="label-text text-xs">Name</span>
      <input class="input input-sm input-bordered" placeholder="tenant-net-3" bind:value={name} required />
    </label>

    <div class="mt-3 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">CIDR</span>
        <input class="input input-sm input-bordered font-mono" placeholder="10.30.0.0/16" bind:value={cidr} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Gateway (optional)</span>
        <input class="input input-sm input-bordered font-mono" placeholder="10.30.0.1" bind:value={gateway} />
      </label>
    </div>

    <div class="mt-3">
      <span class="label-text text-xs">Type</span>
      <div class="mt-1 grid grid-cols-3 gap-2">
        {#each ['nat', 'overlay', 'wireguard'] as t (t)}
          <label class="cursor-pointer rounded-box border p-2 text-center text-sm"
            class:border-primary={type === t}
            class:border-base-300={type !== t}>
            <input type="radio" name="type" class="hidden" value={t}
              checked={type === t} onchange={() => (type = t as typeof type)} />
            {t}
          </label>
        {/each}
      </div>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">DNS servers (comma-separated)</span>
      <input class="input input-sm input-bordered font-mono" placeholder="10.0.0.53, 1.1.1.1" bind:value={dnsServers} />
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
