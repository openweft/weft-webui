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
    type Row, type VMIngressKind, type VMProvisioningSourceKind,
  } from '../api';
  import Combobox from './Combobox.svelte';

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

  // First-boot provisioning : pull a payload + run a sh script. Stored
  // on the VM as guest-readable weft.boot/* properties by the server ;
  // the in-guest weft-vm-agent reads them on first boot, performs
  // git clone / oras pull + extract, then runs the script through
  // mvdan.cc/sh/v3 (POSIX sh in Go, no /bin/sh required).
  let provSource = $state<VMProvisioningSourceKind>('none');
  let provURL = $state('');
  let provRef = $state('');
  let provScript = $state('');

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

  // Picked flavor as an object — drives the detail panel + the submit
  // payload. The Combobox handles grouping (CPU/GPU) internally via
  // its getGroup prop, so no derived split is needed here.
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
      const hasProvisioning =
        provSource !== 'none' || provScript.trim() !== '';
      const res = await createVM({
        Name: name.trim(),
        Image: image.trim(),
        Flavor: String(flavor.name),
        SchedulingRule: schedulingRule || undefined,
        Network: network || undefined,
        IngressKind: ingressKind,
        IngressFloatingIP: ingressFIP || undefined,
        IngressLoadBalancer: ingressLB || undefined,
        Provisioning: hasProvisioning ? {
          source_kind: provSource,
          source_url: provURL.trim(),
          source_ref: provRef.trim(),
          script: provScript,
        } : undefined,
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
    provSource = 'none'; provURL = ''; provRef = ''; provScript = '';
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
          <Combobox
            items={flavors}
            bind:value={flavorName}
            getId={(f) => String(f.name)}
            getLabel={(f) => String(f.name)}
            getSub={(f) => `${f.vcpu} vCPU · ${f.ram} · ${f.ephemeral_gb} GB${f.gpu ? ' · ' + f.gpu : ''}`}
            getGroup={(f) => (f.gpu ? 'GPU' : 'CPU')}
            placeholder="Type to filter flavors…"
          />

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
      <div class="form-control">
        <span class="label-text text-xs">Scheduling policy</span>
        <Combobox
          items={rules}
          bind:value={schedulingRule}
          getId={(r) => String(r.name)}
          getLabel={(r) => String(r.name)}
          getSub={(r) =>
            `${r.count ?? '0/0'} · ${r.placement ?? 'any'}` +
            (r.selector ? ` · sel: ${r.selector}` : '')
          }
          placeholder="Type to filter rules… (empty = no constraint)"
        />
        <span class="mt-1 text-xs text-base-content/50">
          ready/desired · placement · selector — the rule's selector
          must match the VM's labels for this VM to count toward it.
        </span>
      </div>
      <div class="form-control">
        <span class="label-text text-xs">Private network</span>
        <Combobox
          items={networks}
          bind:value={network}
          getId={(n) => String(n.name)}
          getLabel={(n) => String(n.name)}
          getSub={(n) => String(n.cidr ?? '')}
          placeholder="Type to filter networks… (empty = project default)"
        />
      </div>
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
        <div class="form-control mt-2">
          <span class="label-text text-xs">Pick a free Floating IP</span>
          <Combobox
            items={freeFIPs}
            bind:value={ingressFIP}
            getId={(f) => String(f.uuid)}
            getLabel={(f) => String(f.address)}
            getSub={(f) => `network ${f.network}`}
            placeholder="Type to filter free FIPs…"
            disabled={freeFIPs.length === 0}
          />
          {#if freeFIPs.length === 0}
            <span class="mt-1 text-xs text-warning">
              No free Floating IP in this project. Allocate one from the
              Floating IPs page first ; the modal can't allocate here yet.
            </span>
          {/if}
        </div>
      {:else if ingressKind === 'loadbalancer'}
        <div class="form-control mt-2">
          <span class="label-text text-xs">Pick the load balancer</span>
          <Combobox
            items={lbs}
            bind:value={ingressLB}
            getId={(lb) => String(lb.uuid)}
            getLabel={(lb) => String(lb.name)}
            getSub={(lb) => `${lb.mode}:${lb.port}`}
            placeholder="Type to filter load balancers…"
          />
          <span class="mt-1 text-xs text-base-content/50">
            The VM is appended to the LB's backend pool. The LB itself
            already carries (or will carry) the Floating IP.
          </span>
        </div>
      {/if}
    </fieldset>

    <!-- First-boot provisioning -->
    <fieldset class="mt-4 rounded-box border border-base-300 p-3">
      <legend class="px-1 text-xs text-base-content/60">First-boot provisioning</legend>
      <p class="mb-2 text-xs text-base-content/60">
        Optional. Pulled + run by the in-guest <code>weft-vm-agent</code> on
        first boot, via <code>mvdan.cc/sh/v3</code> (POSIX sh in Go — no
        <code>/bin/sh</code> needed). Stored as guest-readable
        <code>weft.boot/*</code> properties ; visible on the drawer's
        Properties tab after create.
      </p>
      <div class="flex flex-wrap gap-3 text-sm">
        <label class="label cursor-pointer gap-1">
          <input type="radio" class="radio radio-sm" value="none" bind:group={provSource} />
          <span>No payload (script-only or none)</span>
        </label>
        <label class="label cursor-pointer gap-1">
          <input type="radio" class="radio radio-sm" value="git" bind:group={provSource} />
          <span>Git repo</span>
        </label>
        <label class="label cursor-pointer gap-1">
          <input type="radio" class="radio radio-sm" value="oci" bind:group={provSource} />
          <span>OCI artifact (+ extract)</span>
        </label>
      </div>

      {#if provSource !== 'none'}
        <div class="mt-2 grid gap-2 sm:grid-cols-[2fr_1fr]">
          <label class="form-control">
            <span class="label-text text-xs">
              {provSource === 'git' ? 'Git URL' : 'OCI reference'}
            </span>
            <input class="input input-xs input-bordered font-mono"
              placeholder={provSource === 'git'
                ? 'https://github.com/team/payload.git'
                : 'ghcr.io/team/payload:v1.2.3'}
              bind:value={provURL} />
          </label>
          <label class="form-control">
            <span class="label-text text-xs">
              {provSource === 'git' ? 'Branch / tag / SHA' : 'Digest / tag'}
            </span>
            <input class="input input-xs input-bordered font-mono"
              placeholder={provSource === 'git' ? 'main' : 'sha256:…'}
              bind:value={provRef} />
          </label>
        </div>
      {/if}

      <label class="form-control mt-2">
        <span class="label-text text-xs">Script (sh)</span>
        <textarea class="textarea textarea-sm textarea-bordered font-mono text-xs"
          rows="4"
          placeholder={'#!/bin/sh\nset -eu\ncd payload\n./setup.sh'}
          bind:value={provScript}></textarea>
        <span class="mt-1 text-xs text-base-content/50">
          Runs in the payload's CWD post-pull. Exit non-zero marks the
          provisioning as failed on the VM's Timings stream ; the VM
          stays up so you can debug.
        </span>
      </label>
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
