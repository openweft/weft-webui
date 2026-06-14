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
  let type = $state<'nat' | 'bridged' | 'isolated' | 'mesh'>('nat');
  let dnsServers = $state(''); // comma-separated input → []string

  // Edge-attachment for floating IPs : default "bgp" matches the
  // existing data-plane behaviour. Switching to "vlan" reveals the
  // VLAN + parent_interface fields and turns the network into an
  // L2-attached one (academic / enterprise where the establishment
  // gives you a VLAN trunk + subnet, no routing protocol).
  let externalMode = $state<'bgp' | 'vlan'>('bgp');
  let vlan = $state<number>(0);
  let parentInterface = $state('');

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
    if (externalMode === 'vlan' && !parentInterface.trim()) {
      error = 'parent_interface is required when external_mode == "vlan"';
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
        external_mode: externalMode,
        vlan: externalMode === 'vlan' ? Number(vlan) || 0 : undefined,
        parent_interface: externalMode === 'vlan' ? parentInterface.trim() : undefined,
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
    type = 'nat'; dnsServers = ''; error = '';
    externalMode = 'bgp'; vlan = 0; parentInterface = '';
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
      <div class="mt-1 grid grid-cols-2 gap-2 sm:grid-cols-4">
        {#each [
          { v: 'nat',      desc: 'Default — host-shared NAT egress' },
          { v: 'bridged',  desc: 'Bridge to a host interface ; DHCP server runs on the bridge' },
          { v: 'isolated', desc: 'VM-to-VM only, no host or external reach' },
          { v: 'mesh',     desc: 'WireGuard mesh between weft-agents' },
        ] as t (t.v)}
          <label class="cursor-pointer rounded-box border p-2 text-center text-sm"
            class:border-primary={type === t.v}
            class:border-base-300={type !== t.v}
            title={t.desc}>
            <input type="radio" name="type" class="hidden" value={t.v}
              checked={type === t.v} onchange={() => (type = t.v as typeof type)} />
            {t.v}
          </label>
        {/each}
      </div>
      {#if type === 'bridged'}
        <p class="mt-2 text-xs text-base-content/60">
          The host runs a DHCPv4 server on the bridge so VMs get
          their IP/gateway/DNS automatically. The lease range is
          derived from the CIDR (skipping the network address,
          gateway, broadcast). DNS servers below populate option
          6 in the DHCP offer.
        </p>
      {/if}
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">DNS servers (comma-separated)</span>
      <input class="input input-sm input-bordered font-mono" placeholder="10.0.0.53, 1.1.1.1" bind:value={dnsServers} />
    </label>

    <!--
      Edge attachment for floating IPs. Default 'bgp' keeps the
      historic behaviour (per-tenant weft-router microVM announces
      /32 via BGP). 'vlan' is the academic / enterprise path where
      the establishment gives a VLAN trunk + subnet and weft-agent
      attaches a macvlan on the host running each VM.
    -->
    <div class="mt-4 rounded-box border border-base-300 p-3">
      <div class="text-xs font-semibold uppercase text-base-content/70">Floating-IP edge attachment</div>
      <div class="mt-2 grid grid-cols-2 gap-2">
        {#each [
          { v: 'bgp',  label: 'BGP /32',  desc: 'Per-tenant weft-router announces' },
          { v: 'vlan', label: 'VLAN L2',  desc: 'Host attaches macvlan + ARP/gARP' },
        ] as m (m.v)}
          <label class="cursor-pointer rounded-box border p-2 text-left text-sm"
            class:border-primary={externalMode === m.v}
            class:border-base-300={externalMode !== m.v}>
            <input type="radio" name="extmode" class="hidden" value={m.v}
              checked={externalMode === m.v}
              onchange={() => (externalMode = m.v as typeof externalMode)} />
            <div class="font-medium">{m.label}</div>
            <div class="text-xs text-base-content/60">{m.desc}</div>
          </label>
        {/each}
      </div>

      {#if externalMode === 'vlan'}
        <div class="mt-3 grid gap-3 sm:grid-cols-2">
          <label class="form-control">
            <span class="label-text text-xs">VLAN (0 = untagged trunk)</span>
            <input type="number" min="0" max="4094"
              class="input input-sm input-bordered font-mono"
              placeholder="100" bind:value={vlan} />
          </label>
          <label class="form-control">
            <span class="label-text text-xs">Parent interface (host NIC)</span>
            <input class="input input-sm input-bordered font-mono"
              placeholder="eth0" bind:value={parentInterface} required />
          </label>
        </div>
      {/if}
    </div>

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
