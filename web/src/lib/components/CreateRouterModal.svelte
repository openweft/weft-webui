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
  // Egress-only knobs : the CIDRs the router announces upstream
  // (BGP /32 for FIPs is automatic via floatingipnat ; this is
  // for the operator-typed announce set, e.g. tenant-owned space).
  let prefixesInput = $state(''); // comma-separated, parsed on submit
  // Replicas drives HA : > 1 spawns N weft-router microVMs that
  // all advertise the same prefixes ; upstream ECMP balances.
  let replicas = $state(1);

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

  // Track kind → default backend. Egress defaults to gobgp now
  // (Go-native, HA-capable via Replicas) ; VyOS / FRR remain as
  // escape-hatch picks for tenants who need multi-protocol routing.
  $effect(() => {
    backend = kind === 'peer' ? 'wireguard' : 'gobgp';
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
    if (kind === 'egress' && replicas < 1) {
      error = 'replicas must be ≥ 1';
      return;
    }
    if (kind === 'egress' && replicas > 10) {
      error = 'replicas capped at 10 by the orchestrator';
      return;
    }
    busy = true;
    try {
      const prefixes = kind === 'egress'
        ? prefixesInput.split(',').map((s) => s.trim()).filter(Boolean)
        : undefined;
      await createRouter({
        name: name.trim(), kind, backend,
        networks: selectedNets, external: external.trim(),
        prefixes,
        replicas: kind === 'egress' ? replicas : undefined,
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
    prefixesInput = ''; replicas = 1;
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
          <option value="gobgp">gobgp</option>
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
        <span class="label-text text-xs">External (AS number or "ASN:peer-ip")</span>
        <input class="input input-sm input-bordered font-mono"
          placeholder="65512:198.51.100.1" bind:value={external} />
      </label>

      <label class="form-control mt-3">
        <span class="label-text text-xs">Prefixes (CIDRs to announce, comma-separated)</span>
        <input class="input input-sm input-bordered font-mono"
          placeholder="203.0.113.0/24, 2001:db8::/48" bind:value={prefixesInput} />
        <span class="label-text-alt mt-1 text-base-content/50">
          Per-FIP /32 + /128 are added automatically by floatingipnat ;
          this list is for operator-typed announce ranges (tenant-owned space).
        </span>
      </label>

      <label class="form-control mt-3">
        <span class="label-text text-xs">
          Replicas (HA)
          {#if replicas > 1}<span class="badge badge-success badge-xs ml-1">active-active</span>{/if}
        </span>
        <div class="flex items-center gap-2">
          <input type="range" min="1" max="10" step="1"
            class="range range-sm range-primary flex-1"
            bind:value={replicas} />
          <span class="font-mono text-sm w-8 text-right">{replicas}</span>
        </div>
        <span class="label-text-alt mt-1 text-base-content/50">
          1 = single weft-router microVM (no HA). 2-3 spreads across DCs ;
          all replicas advertise the same prefixes ; upstream BGP
          multipath / ECMP balances inbound traffic.
        </span>
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
