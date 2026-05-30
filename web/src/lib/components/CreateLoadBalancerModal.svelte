<script lang="ts">
  // Create-LoadBalancer modal. Project comes from the session scope ;
  // the backend list is picked from the project's microVMs so the
  // operator doesn't have to type names — typos here would
  // 502-cascade through weft-network's reconciler.
  import { createLoadBalancer, getMe, getRows, type Row } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let mode = $state<'L4' | 'L7'>('L7');
  let port = $state(443);
  let az = $state<'multi' | 'DC-A' | 'DC-B' | 'DC-C'>('multi');
  let backends = $state<string[]>([]);

  let project = $state('');
  let candidates = $state<Row[]>([]);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      getMe().then((u) => (project = u.project));
      getRows('microvms').then((rs) => (candidates = rs)).catch(() => { /* ok */ });
    } else { dialog?.close(); }
  });

  function toggle(name: string) {
    backends = backends.includes(name) ? backends.filter((x) => x !== name) : [...backends, name];
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!project) { error = 'no project in scope — pick one in the Topbar'; return; }
    if (!name.trim() || port <= 0) { error = 'name and a positive port are required'; return; }
    busy = true;
    try {
      await createLoadBalancer({
        name: name.trim(), mode, port,
        backends, az,
      });
      onCreated();
      reset();
      open = false;
    } catch (err) { error = String(err); }
    finally { busy = false; }
  }

  function reset() {
    name = ''; mode = 'L7'; port = 443; az = 'multi'; backends = []; error = '';
  }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">New load balancer</h3>
    <p class="text-sm text-base-content/60">
      In project <span class="font-mono">{project || '—'}</span>.
      Caddy data plane embedded in each host's weft-agent, programmed by weft-network.
    </p>

    <div class="mt-4 grid gap-3 sm:grid-cols-[2fr_1fr_1fr_1fr]">
      <label class="form-control">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered" placeholder="web-prod" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Mode</span>
        <select class="select select-sm select-bordered" bind:value={mode}>
          <option value="L7">L7</option>
          <option value="L4">L4</option>
        </select>
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Port</span>
        <input type="number" min="1" max="65535" class="input input-sm input-bordered tabular-nums" bind:value={port} />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">AZ</span>
        <select class="select select-sm select-bordered" bind:value={az}>
          <option value="multi">multi</option>
          <option value="DC-A">DC-A</option>
          <option value="DC-B">DC-B</option>
          <option value="DC-C">DC-C</option>
        </select>
      </label>
    </div>

    <div class="mt-4">
      <span class="label-text text-xs">Backends (pick the microVMs to fan traffic to)</span>
      <div class="mt-1 flex flex-wrap gap-2">
        {#if candidates.length === 0}
          <span class="text-xs text-base-content/50">No microVMs visible in this scope.</span>
        {:else}
          {#each candidates as v (v.name)}
            <label class="cursor-pointer rounded-box border px-2 py-1 text-xs"
              class:border-primary={backends.includes(String(v.name))}
              class:border-base-300={!backends.includes(String(v.name))}>
              <input type="checkbox" class="hidden"
                checked={backends.includes(String(v.name))}
                onchange={() => toggle(String(v.name))} />
              {v.name} · {v.ip ?? '—'}
            </label>
          {/each}
        {/if}
      </div>
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
