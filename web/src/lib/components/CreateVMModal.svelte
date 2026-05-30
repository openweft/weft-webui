<script lang="ts">
  // Create-microVM modal — restructured around the user's data model :
  //
  //   microVM = image + flavor + scheduling policy
  //           + private network
  //           + (optional) public ingress { none | floating IP | LB }
  //
  // CPU / RAM / disk are NOT independent fields — they're properties
  // of the flavor and shown read-only once one is picked.
  // SSH keys are NOT here either — they're a runtime concern, pushed
  // via the drawer's "SSH keys" tab (which the in-guest weft-vm-agent
  // applies idempotently). This keeps the create surface small and
  // honest : "what kind, where, behind which entry point".
  import {
    createVM, getMe, getFlavors, getRowsPage,
    type Row, type VMIngressKind,
  } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  // Inputs.
  let name = $state('');
  let image = $state('alpine:3.21');
  // Selected flavor : bound as the name (string) so the native <select>
  // doesn't need object identity. `flavor` is derived after `flavors`
  // is declared (see below) so the rest of the form keeps its
  // object-shape view.
  let flavorName = $state('');
  let schedulingRule = $state('');
  let network = $state('');
  let ingressKind = $state<VMIngressKind>('none');
  let ingressFIP = $state('');     // FIP uuid
  let ingressLB = $state('');      // LB uuid

  // Loaded once when the modal opens.
  let project = $state('');
  let flavors = $state<Row[]>([]);
  let rules = $state<Row[]>([]);
  let networks = $state<Row[]>([]);
  let fips = $state<Row[]>([]);
  let lbs = $state<Row[]>([]);

  let error = $state('');
  let warnings = $state<string[]>([]);
  let busy = $state(false);

  $effect(() => {
    if (!open) { dialog?.close(); return; }
    dialog?.showModal();
    error = ''; warnings = [];
    getMe().then((u) => (project = u.project));
    getFlavors().then((rs) => (flavors = rs)).catch(() => { /* ok */ });
    // Companion catalogues for the optional fields. Each is best-effort —
    // a missing controller (no weft-network) just leaves the dropdown
    // empty, which is honest about what's wireable in this deployment.
    getRowsPage('scheduling-rules', { limit: 200 })
      .then((p) => (rules = p.rows)).catch(() => { /* ok */ });
    getRowsPage('networks', { limit: 200 })
      .then((p) => (networks = p.rows)).catch(() => { /* ok */ });
    getRowsPage('floating-ips', { limit: 200 })
      .then((p) => (fips = p.rows)).catch(() => { /* ok */ });
    getRowsPage('loadbalancers', { limit: 200 })
      .then((p) => (lbs = p.rows)).catch(() => { /* ok */ });
  });

  // Free FIPs : unmapped ones in the project scope. The map column
  // emits an empty string when nothing's mapped.
  let freeFIPs = $derived.by<Row[]>(() => {
    return fips.filter((f) => String(f.mapped_to ?? '') === '');
  });

  // Group flavors by the GPU/no-GPU axis so the <select> scales past
  // a handful of entries. Native <optgroup> means no extra widget code
  // and works at 7 or 700 flavors. The grouping is derived (not seeded)
  // so a future "ai-mig" or "spot" tier shows up as soon as the
  // catalogue carries it.
  let cpuFlavors = $derived(flavors.filter((f) => !f.gpu));
  let gpuFlavors = $derived(flavors.filter((f) => !!f.gpu));

  // Picked flavor as an object — drives the detail panel + the submit
  // payload. Declared here (after `flavors`) to avoid the use-before-
  // declaration that bit the first try.
  let flavor = $derived<Row | null>(
    flavors.find((f) => String(f.name) === flavorName) ?? null,
  );

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = ''; warnings = [];
    if (!project) {
      error = 'no project in scope — pick one in the Topbar before creating a microVM';
      return;
    }
    if (!name.trim() || !image.trim()) {
      error = 'name and image are required';
      return;
    }
    if (!flavor) {
      error = 'pick a flavor — cpu / ram / disk come from it';
      return;
    }
    if (ingressKind === 'floating_ip' && !ingressFIP) {
      error = 'ingress = floating IP : pick one from the dropdown (or allocate one first from the Floating IPs page)';
      return;
    }
    if (ingressKind === 'loadbalancer' && !ingressLB) {
      error = 'ingress = load balancer : pick one from the dropdown';
      return;
    }
    busy = true;
    try {
      const res = await createVM({
        Name: name.trim(),
        Image: image.trim(),
        Flavor: String(flavor.name),
        SchedulingRule: schedulingRule || undefined,
        Network: network || undefined,
        IngressKind: ingressKind,
        IngressFloatingIP: ingressFIP || undefined,
        IngressLoadBalancer: ingressLB || undefined,
      }) as { name: string; project: string; warnings?: string[] };
      warnings = res.warnings ?? [];
      onCreated();
      // Surface warnings if any — otherwise close immediately.
      if (warnings.length === 0) {
        reset();
        open = false;
      }
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }

  function reset() {
    name = ''; image = 'alpine:3.21';
    flavorName = '';
    schedulingRule = ''; network = '';
    ingressKind = 'none'; ingressFIP = ''; ingressLB = '';
    error = ''; warnings = [];
  }

  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-2xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">New microVM</h3>
    <p class="text-sm text-base-content/60">
      In project <span class="font-mono">{project || '—'}</span>.
      A microVM is an <em>image</em> launched at a <em>flavor</em>, placed
      by a <em>scheduling policy</em>, attached to a private <em>network</em>,
      optionally exposed via a floating IP or a load balancer.
    </p>

    <!-- Image + name -->
    <div class="mt-4 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered" placeholder="web-2" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Image (OCI / disk)</span>
        <input class="input input-sm input-bordered" placeholder="alpine:3.21" bind:value={image} required />
      </label>
    </div>

    <!-- Flavor : combo + detail panel. Native <select> + <optgroup>
         scales past the 7-tile demo to the hundreds-of-flavors case
         a real catalogue grows to. The right panel shows the picked
         flavor's compute envelope so the operator confirms before
         submit. -->
    <div class="mt-4">
      <span class="label-text text-xs">Flavor <span class="text-error">*</span></span>
      {#if flavors.length === 0}
        <p class="mt-1 text-xs text-base-content/50">No flavor catalogue loaded.</p>
      {:else}
        <div class="mt-1 grid gap-3 sm:grid-cols-[1fr_1.2fr]">
          <select
            class="select select-sm select-bordered"
            bind:value={flavorName}
            size={Math.min(8, flavors.length + (cpuFlavors.length > 0 && gpuFlavors.length > 0 ? 2 : 1))}
          >
            <option value="" disabled>— pick a flavor —</option>
            {#if cpuFlavors.length > 0 && gpuFlavors.length > 0}
              <optgroup label="CPU">
                {#each cpuFlavors as f (f.name)}
                  <option value={String(f.name)}>{f.name}  ·  {f.vcpu} vCPU  ·  {f.ram}</option>
                {/each}
              </optgroup>
              <optgroup label="GPU">
                {#each gpuFlavors as f (f.name)}
                  <option value={String(f.name)}>{f.name}  ·  {f.vcpu} vCPU  ·  {f.ram}  ·  {f.gpu}</option>
                {/each}
              </optgroup>
            {:else}
              {#each flavors as f (f.name)}
                <option value={String(f.name)}>
                  {f.name}  ·  {f.vcpu} vCPU  ·  {f.ram}{f.gpu ? '  ·  ' + f.gpu : ''}
                </option>
              {/each}
            {/if}
          </select>

          <div class="rounded-box border border-base-300 bg-base-200/40 p-3 text-sm">
            {#if flavor}
              <div class="font-mono font-semibold">{flavor.name}</div>
              <dl class="mt-2 grid grid-cols-[6rem_1fr] gap-y-1 text-xs">
                <dt class="text-base-content/60">vCPU</dt>
                <dd class="tabular-nums">{flavor.vcpu}</dd>
                <dt class="text-base-content/60">Memory</dt>
                <dd class="tabular-nums">{flavor.ram}</dd>
                <dt class="text-base-content/60">Ephemeral</dt>
                <dd class="tabular-nums">{flavor.ephemeral_gb} GB</dd>
                <dt class="text-base-content/60">GPU</dt>
                <dd>{flavor.gpu || '—'}</dd>
              </dl>
              <p class="mt-2 text-xs text-base-content/50">
                Fixed by the flavor. To run something off-envelope, edit
                the catalogue (admin) rather than the VM here.
              </p>
            {:else}
              <p class="text-xs text-base-content/50">
                Pick a flavor on the left to see its compute envelope.
              </p>
            {/if}
          </div>
        </div>
      {/if}
    </div>

    <!-- Scheduling policy + private network -->
    <div class="mt-4 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">Scheduling policy</span>
        <select class="select select-sm select-bordered" bind:value={schedulingRule}>
          <option value="">(none — scheduler picks any host)</option>
          {#each rules as r (r.uuid ?? r.name)}
            <option value={String(r.name)}>
              {r.name} · {r.count ?? '0/0'} · {r.placement ?? 'any'}{r.selector ? ' · sel: ' + r.selector : ''}
            </option>
          {/each}
        </select>
        <span class="mt-1 text-xs text-base-content/50">
          ready/desired · placement · selector — the rule's selector
          must match the VM's labels for this VM to count toward it.
        </span>
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Private network</span>
        <select class="select select-sm select-bordered" bind:value={network}>
          <option value="">(project default)</option>
          {#each networks as n (n.uuid ?? n.name)}
            <option value={String(n.name)}>{n.name} — {n.cidr ?? ''}</option>
          {/each}
        </select>
      </label>
    </div>

    <!-- Public ingress -->
    <fieldset class="mt-4 rounded-box border border-base-300 p-3">
      <legend class="px-1 text-xs text-base-content/60">Public ingress</legend>
      <div class="flex flex-wrap gap-3 text-sm">
        <label class="label cursor-pointer gap-1">
          <input type="radio" class="radio radio-sm" value="none" bind:group={ingressKind} />
          <span>None (private only)</span>
        </label>
        <label class="label cursor-pointer gap-1">
          <input type="radio" class="radio radio-sm" value="floating_ip" bind:group={ingressKind} />
          <span>Floating IP — direct</span>
        </label>
        <label class="label cursor-pointer gap-1">
          <input type="radio" class="radio radio-sm" value="loadbalancer" bind:group={ingressKind} />
          <span>Load balancer — carries the FIP</span>
        </label>
      </div>

      {#if ingressKind === 'floating_ip'}
        <label class="form-control mt-2">
          <span class="label-text text-xs">Pick a free Floating IP</span>
          <select class="select select-sm select-bordered" bind:value={ingressFIP}>
            <option value="">— select —</option>
            {#each freeFIPs as f (f.uuid)}
              <option value={String(f.uuid)}>{f.address} ({f.network})</option>
            {/each}
          </select>
          {#if freeFIPs.length === 0}
            <span class="mt-1 text-xs text-warning">
              No free Floating IP in this project. Allocate one from the
              Floating IPs page first ; the modal can't allocate here yet.
            </span>
          {/if}
        </label>
      {:else if ingressKind === 'loadbalancer'}
        <label class="form-control mt-2">
          <span class="label-text text-xs">Pick the load balancer</span>
          <select class="select select-sm select-bordered" bind:value={ingressLB}>
            <option value="">— select —</option>
            {#each lbs as lb (lb.uuid)}
              <option value={String(lb.uuid)}>{lb.name} — {lb.mode}:{lb.port}</option>
            {/each}
          </select>
          <span class="mt-1 text-xs text-base-content/50">
            The VM is appended to the LB's backend pool. The LB itself
            already carries (or will carry) the Floating IP.
          </span>
        </label>
      {/if}
    </fieldset>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}
    {#if warnings.length > 0}
      <div class="mt-3 alert alert-warning py-2 text-sm">
        <div>
          <div class="font-semibold">The VM was created. The following post-create steps reported issues :</div>
          <ul class="mt-1 list-inside list-disc">
            {#each warnings as w (w)}<li>{w}</li>{/each}
          </ul>
          <button type="button" class="btn btn-xs btn-ghost mt-2" onclick={() => { reset(); open = false; }}>
            Dismiss
          </button>
        </div>
      </div>
    {/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={cancel}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Create
      </button>
    </div>
  </form>
</dialog>
