<script lang="ts">
  // Create-scheduling-rule modal. Three dropdowns (az / rack / host)
  // each accept `any`, `same`, `different`, or a free-form specific
  // value (e.g. "DC-C") via the bottom option. The compact placement
  // string is rebuilt server-side from the typed fields.
  //
  // No daemon RPC backs this yet — the rule is stored in webui's
  // in-memory schedulingDB and the scheduler reads cluster.hcl directly.
  // When weft-agent grows CreateSchedulingRule the modal's body shape
  // becomes the wire format unchanged.
  import { createSchedulingRule, getMe } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let selector = $state('app=');
  let count = $state(3);

  // Each axis : kind ∈ {'any','same','different','specific'} +
  // optional `specific` value (typed AZ name etc.).
  let azKind = $state<'any' | 'same' | 'different' | 'specific'>('different');
  let azSpec = $state('');
  let rackKind = $state<'any' | 'same' | 'different' | 'specific'>('different');
  let rackSpec = $state('');
  let hostKind = $state<'any' | 'same' | 'different' | 'specific'>('different');
  let hostSpec = $state('');

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

  function resolve(kind: string, spec: string): string {
    if (kind === 'any') return '';
    if (kind === 'specific') return spec.trim();
    return kind;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!name.trim() || !selector.trim()) {
      error = 'name and selector are required';
      return;
    }
    if (count < 0) {
      error = 'count must be ≥ 0';
      return;
    }
    busy = true;
    try {
      await createSchedulingRule({
        name: name.trim(),
        selector: selector.trim(),
        count,
        az: resolve(azKind, azSpec),
        rack: resolve(rackKind, rackSpec),
        host: resolve(hostKind, hostSpec),
        project: project || undefined, // server falls back to "platform" when missing
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
    name = ''; selector = 'app='; count = 3;
    azKind = 'different'; azSpec = '';
    rackKind = 'different'; rackSpec = '';
    hostKind = 'different'; hostSpec = '';
    error = '';
  }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">New scheduling rule</h3>
    <p class="text-sm text-base-content/60">
      In project <span class="font-mono">{project || 'platform'}</span>.
      The scheduler picks hosts honouring the directives below.
    </p>

    <div class="mt-4 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered" placeholder="my-quorum" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Desired replicas</span>
        <input type="number" min="0" class="input input-sm input-bordered tabular-nums" bind:value={count} />
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Selector (label expression)</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="app=foo, kind=worker" bind:value={selector} required />
    </label>

    <div class="mt-4 rounded-box border border-base-300 p-3">
      <div class="text-xs font-medium text-base-content/70">Placement (AZ ⊃ Rack ⊃ Host)</div>
      {#each [
        { label: 'AZ',   kind: azKind,   setKind: (v: 'any'|'same'|'different'|'specific') => (azKind = v),   spec: azSpec,   setSpec: (v: string) => (azSpec = v),   placeholder: 'DC-C' },
        { label: 'Rack', kind: rackKind, setKind: (v: 'any'|'same'|'different'|'specific') => (rackKind = v), spec: rackSpec, setSpec: (v: string) => (rackSpec = v), placeholder: 'R2' },
        { label: 'Host', kind: hostKind, setKind: (v: 'any'|'same'|'different'|'specific') => (hostKind = v), spec: hostSpec, setSpec: (v: string) => (hostSpec = v), placeholder: 'dc-a-r1-h2' },
      ] as axis (axis.label)}
        <div class="mt-2 grid grid-cols-[5rem_1fr_1fr] items-center gap-2">
          <span class="text-sm font-medium">{axis.label}</span>
          <select class="select select-sm select-bordered"
            value={axis.kind}
            onchange={(e) => axis.setKind(e.currentTarget.value as 'any'|'same'|'different'|'specific')}>
            <option value="any">any</option>
            <option value="same">same</option>
            <option value="different">different</option>
            <option value="specific">specific…</option>
          </select>
          {#if axis.kind === 'specific'}
            <input class="input input-sm input-bordered font-mono"
              placeholder={axis.placeholder}
              value={axis.spec}
              oninput={(e) => axis.setSpec(e.currentTarget.value)} />
          {:else}
            <span class="text-xs text-base-content/40">—</span>
          {/if}
        </div>
      {/each}
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
