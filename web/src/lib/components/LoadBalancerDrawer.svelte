<script lang="ts">
  // LoadBalancer detail drawer. Opens when an LB row is clicked. Shows
  // the metadata up top + an editable backend list pulled from the
  // microVMs of the same project. Save sends the whole list atomically
  // (PUT /api/loadbalancers/{uuid}/backends), mirroring the SG rules
  // editor's "atomic replace + dirty-tracking" pattern.
  //
  // The row already carries `backends` as a comma-joined string ; we
  // split it back into an array on open, snapshot for dirty detection,
  // and let the operator toggle membership from the microVM picker.
  import { setLoadBalancerBackends, getRows, type Row } from '../api';

  let {
    row,
    onClose,
    onChanged,
  }: {
    row: Row;
    onClose: () => void;
    onChanged: () => void;
  } = $props();

  let uuid = $derived(String(row.uuid));
  let name = $derived(String(row.name));
  let mode = $derived(String(row.mode ?? ''));
  let address = $derived(String(row.address ?? ''));
  let port = $derived(String(row.port ?? ''));
  let project = $derived(String(row.project ?? ''));
  let az = $derived(String(row.az ?? ''));
  let controller = $derived(String(row.controller ?? ''));
  let status = $derived(String(row.status ?? ''));

  // Initial backends = parse the row's CSV. The picker mutates this.
  function parseBackends(v: unknown): string[] {
    const s = String(v ?? '').trim();
    if (s === '') return [];
    return s.split(',').map((x) => x.trim()).filter(Boolean);
  }

  // Initial backend list + snapshot used for dirty-tracking. Set on
  // first effect run so the linter doesn't flag the row read ;
  // ResourcePage remounts the drawer between selections, so we
  // re-init every time row changes by design.
  let backends = $state<string[]>([]);
  let snapshot = $state('');
  $effect(() => {
    const init = parseBackends(row.backends);
    backends = init;
    snapshot = JSON.stringify(init);
  });

  let candidates = $state<Row[]>([]);
  let loadErr = $state('');
  let saveErr = $state('');
  let saveBusy = $state(false);

  async function loadCandidates() {
    loadErr = '';
    try {
      const rs = await getRows('microvms');
      // Narrow to the LB's project so an unrelated project's VMs
      // don't end up as candidates.
      candidates = rs.filter((v) => !project || String(v.project ?? '') === project);
    } catch (e) { loadErr = String(e); }
  }
  $effect(() => { uuid; loadCandidates(); });

  function toggle(n: string) {
    backends = backends.includes(n) ? backends.filter((x) => x !== n) : [...backends, n];
  }

  let dirty = $derived(JSON.stringify(backends) !== snapshot);

  async function save() {
    saveBusy = true; saveErr = '';
    try {
      await setLoadBalancerBackends(uuid, backends);
      snapshot = JSON.stringify(backends);
      onChanged();
    } catch (e) { saveErr = String(e); }
    finally { saveBusy = false; }
  }

  function statusClass(v: string): string {
    switch (v.toLowerCase()) {
      case 'active': return 'badge-success';
      case 'provisioning': return 'badge-warning';
      case 'failed': return 'badge-error';
      default: return 'badge-ghost';
    }
  }
</script>

<!-- Backdrop -->
<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-3xl flex-col bg-base-100 shadow-2xl">
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div>
      <div class="flex items-baseline gap-2">
        <h2 class="text-lg font-bold">{name}</h2>
        <span class="badge badge-sm badge-ghost">{mode || 'load balancer'}</span>
        <span class="badge badge-sm {statusClass(status)}">{status || '—'}</span>
      </div>
      <p class="text-xs text-base-content/60">
        <span class="font-mono">{address}:{port}</span> · AZ {az || '—'} · project {project || '—'}
      </p>
      <p class="text-[10px] text-base-content/40">
        controller {controller || '—'} · <span class="font-mono">{uuid}</span>
      </p>
    </div>
    <div class="ml-auto flex items-center gap-1">
      <button class="btn btn-ghost btn-xs" title="Reload" onclick={loadCandidates}>↻</button>
      <button class="btn btn-ghost btn-xs" aria-label="Close" onclick={onClose}>✕</button>
    </div>
  </header>

  <div class="flex-1 overflow-y-auto px-5 py-4">
    <h3 class="text-sm font-semibold">Backends</h3>
    <p class="text-xs text-base-content/60">
      microVMs receiving traffic from this LB. The reconciler diffs
      against the live xDS state and pushes only the deltas to Envoy.
    </p>

    {#if backends.length > 0}
      <div class="mt-2 flex flex-wrap gap-1">
        {#each backends as b (b)}
          <span class="badge badge-primary gap-1">
            {b}
            <button class="opacity-70 hover:opacity-100" onclick={() => toggle(b)}>✕</button>
          </span>
        {/each}
      </div>
    {:else}
      <p class="mt-2 text-sm text-base-content/50">
        No backends — the LB will return 503 / refuse connections on
        every incoming request.
      </p>
    {/if}

    <h3 class="mt-6 text-sm font-semibold">Available microVMs</h3>
    {#if loadErr}
      <div class="mt-2 alert alert-error text-sm">{loadErr}</div>
    {:else if candidates.length === 0}
      <p class="mt-2 text-sm text-base-content/50">
        No microVMs in this project — create one before wiring it up.
      </p>
    {:else}
      <div class="mt-2 flex flex-wrap gap-2">
        {#each candidates as v (v.name)}
          <label class="cursor-pointer rounded-box border px-2 py-1 text-xs"
            class:border-primary={backends.includes(String(v.name))}
            class:border-base-300={!backends.includes(String(v.name))}>
            <input type="checkbox" class="hidden"
              checked={backends.includes(String(v.name))}
              onchange={() => toggle(String(v.name))} />
            {v.name}
            <span class="text-base-content/40">·</span>
            <span class="font-mono">{v.ip ?? '—'}</span>
          </label>
        {/each}
      </div>
    {/if}

    {#if saveErr}<div class="mt-3 alert alert-error text-sm">{saveErr}</div>{/if}
  </div>

  <footer class="flex shrink-0 items-center gap-2 border-t border-base-300 px-5 py-3">
    {#if dirty}
      <span class="text-xs text-warning">unsaved changes</span>
    {:else}
      <span class="text-xs text-base-content/40">no changes</span>
    {/if}
    <button class="ml-auto btn btn-sm btn-ghost" onclick={onClose}>Close</button>
    <button class="btn btn-sm btn-primary" disabled={!dirty || saveBusy} onclick={save}>
      {#if saveBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
      Save backends
    </button>
  </footer>
</aside>
