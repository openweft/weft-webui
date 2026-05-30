<script lang="ts">
  // Create-security-group modal. Project from session scope.
  //
  // The rules editor is a small inline table — direction, protocol,
  // port range, remote CIDR. The proto also accepts a remote_group_uuid
  // (peer the rule to another SG) ; we expose the CIDR-form here for
  // first cut, the group-form once the UI surfaces SG selection.
  import { createSecurityGroup, getMe, type SecurityRule } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let description = $state('');
  let rules = $state<SecurityRule[]>([
    { direction: 'ingress', protocol: 'tcp', port_min: 22, port_max: 22, remote_cidr: '0.0.0.0/0', remote_group_uuid: '' },
  ]);

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

  function addRule() {
    rules = [
      ...rules,
      { direction: 'ingress', protocol: 'tcp', port_min: 0, port_max: 0, remote_cidr: '0.0.0.0/0', remote_group_uuid: '' },
    ];
  }
  function removeRule(i: number) {
    rules = rules.filter((_, idx) => idx !== i);
  }
  // Mutating one field of one rule : Svelte 5 needs a fresh reference
  // for $state arrays of plain objects ; spread to trigger reactivity.
  function update(i: number, patch: Partial<SecurityRule>) {
    rules = rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r));
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!project) {
      error = 'no project in scope — pick one in the Topbar';
      return;
    }
    if (!name.trim()) {
      error = 'name is required';
      return;
    }
    busy = true;
    try {
      await createSecurityGroup({
        name: name.trim(),
        description: description.trim(),
        rules: rules.map((r) => ({
          ...r,
          // Coerce protocol="any" to "" if the daemon treats it as such (some do).
          // The wire shape uses the string verbatim ; we leave it to the agent.
        })),
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
    name = ''; description = '';
    rules = [{ direction: 'ingress', protocol: 'tcp', port_min: 22, port_max: 22, remote_cidr: '0.0.0.0/0', remote_group_uuid: '' }];
    error = '';
  }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-3xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">New security group</h3>
    <p class="text-sm text-base-content/60">
      In project <span class="font-mono">{project || '—'}</span>.
      Rules are evaluated per direction ; an empty rule list means
      "default deny on the chosen direction".
    </p>

    <div class="mt-4 grid gap-3 sm:grid-cols-[1fr_2fr]">
      <label class="form-control">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered" placeholder="web" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Description (optional)</span>
        <input class="input input-sm input-bordered" placeholder="HTTP/HTTPS ingress" bind:value={description} />
      </label>
    </div>

    <div class="mt-4 rounded-box border border-base-300 p-3">
      <div class="flex items-center">
        <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/70">Rules</h4>
        <button type="button" class="ml-auto btn btn-xs btn-ghost" onclick={addRule}>+ rule</button>
      </div>
      {#if rules.length === 0}
        <p class="mt-2 text-xs text-base-content/50">No rules — the group will deny on all directions.</p>
      {:else}
        <div class="mt-2 grid grid-cols-[6rem_5rem_4rem_4rem_1fr_2rem] items-center gap-2 text-xs font-medium text-base-content/60">
          <span>Direction</span>
          <span>Protocol</span>
          <span>Port min</span>
          <span>Port max</span>
          <span>Remote CIDR</span>
          <span></span>
        </div>
        {#each rules as r, i (i)}
          <div class="mt-1 grid grid-cols-[6rem_5rem_4rem_4rem_1fr_2rem] items-center gap-2">
            <select class="select select-xs select-bordered"
              value={r.direction}
              onchange={(e) => update(i, { direction: e.currentTarget.value as 'ingress'|'egress' })}>
              <option value="ingress">ingress</option>
              <option value="egress">egress</option>
            </select>
            <select class="select select-xs select-bordered"
              value={r.protocol}
              onchange={(e) => update(i, { protocol: e.currentTarget.value as SecurityRule['protocol'] })}>
              <option value="tcp">tcp</option>
              <option value="udp">udp</option>
              <option value="icmp">icmp</option>
              <option value="any">any</option>
            </select>
            <input type="number" min="0" class="input input-xs input-bordered tabular-nums"
              value={r.port_min}
              oninput={(e) => update(i, { port_min: Number(e.currentTarget.value) })} />
            <input type="number" min="0" class="input input-xs input-bordered tabular-nums"
              value={r.port_max}
              oninput={(e) => update(i, { port_max: Number(e.currentTarget.value) })} />
            <input class="input input-xs input-bordered font-mono"
              placeholder="0.0.0.0/0"
              value={r.remote_cidr}
              oninput={(e) => update(i, { remote_cidr: e.currentTarget.value })} />
            <button type="button" class="btn btn-xs btn-ghost text-error"
              onclick={() => removeRule(i)} title="Remove rule">✕</button>
          </div>
        {/each}
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
