<script lang="ts">
  // Create-router modal. `kind` decides what the rest of the form
  // asks for : a "peer" router takes two tenant networks (WireGuard
  // peers them) ; an "egress" router takes a list of networks and an
  // external descriptor (AS number / upstream peer for the BGP
  // session).
  //
  // Live-only — Routers belong to weft-network. Mock mode surfaces
  // 503 inline.
  import { createRouter, getRows, type Row } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let kind = $state<'peer' | 'egress'>('peer');
  let backend = $state('wireguard');
  let external = $state('');
  let selectedNets = $state<string[]>([]);

  let networks = $state<Row[]>([]);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      getRows('networks').then((rs) => (networks = rs)).catch(() => { /* empty list ok */ });
    } else {
      dialog?.close();
    }
  });

  // Track kind → default backend.
  $effect(() => {
    backend = kind === 'peer' ? 'wireguard' : 'vyos';
  });

  function toggleNet(n: string) {
    selectedNets = selectedNets.includes(n)
      ? selectedNets.filter((x) => x !== n)
      : [...selectedNets, n];
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!name.trim()) { error = 'name is required'; return; }
    if (kind === 'peer' && selectedNets.length < 2) {
      error = 'a peer router needs at least two networks';
      return;
    }
    busy = true;
    try {
      await createRouter({
        name: name.trim(), kind, backend,
        networks: selectedNets, external: external.trim(),
      });
      onCreated();
      reset();
      open = false;
    } catch (err) { error = String(err); }
    finally { busy = false; }
  }

  function reset() {
    name = ''; external = ''; selectedNets = [];
    kind = 'peer'; backend = 'wireguard'; error = '';
  }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">New router</h3>
    <p class="text-sm text-base-content/60">
      Peer two tenant networks together (WireGuard) or expose a network
      to the public internet via BGP egress (VyOS / FRR).
    </p>

    <div class="mt-4 grid gap-3 sm:grid-cols-[2fr_1fr_1fr]">
      <label class="form-control">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered" placeholder="peer-alpha-beta" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Kind</span>
        <select class="select select-sm select-bordered" bind:value={kind}>
          <option value="peer">peer</option>
          <option value="egress">egress</option>
        </select>
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Backend</span>
        <select class="select select-sm select-bordered" bind:value={backend}>
          <option value="wireguard">wireguard</option>
          <option value="vyos">vyos</option>
          <option value="frr">frr</option>
        </select>
      </label>
    </div>

    <div class="mt-3">
      <span class="label-text text-xs">
        Networks
        {#if kind === 'peer'}<span class="text-base-content/40">(pick at least 2)</span>{/if}
      </span>
      <div class="mt-1 flex flex-wrap gap-2">
        {#each networks as n (n.name)}
          <label class="cursor-pointer rounded-box border px-2 py-1 text-xs"
            class:border-primary={selectedNets.includes(String(n.name))}
            class:border-base-300={!selectedNets.includes(String(n.name))}>
            <input type="checkbox" class="hidden"
              checked={selectedNets.includes(String(n.name))}
              onchange={() => toggleNet(String(n.name))} />
            {n.name} · {n.cidr}
          </label>
        {/each}
      </div>
    </div>

    {#if kind === 'egress'}
      <label class="form-control mt-3">
        <span class="label-text text-xs">External (e.g. AS number, upstream peer)</span>
        <input class="input input-sm input-bordered font-mono" placeholder="AS65010" bind:value={external} />
      </label>
    {/if}

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
