<script lang="ts">
  // Right-side drawer that opens when a Security Group row is clicked.
  // Shows the SG's metadata + an editable rules table. Save POSTs the
  // whole list atomically (PUT /api/security-groups/{uuid}/rules) ;
  // the operator can also add / remove rows before saving.
  //
  // Live-first read : /api/security-groups/{uuid}/rules calls
  // wclient.GetSecurityGroup ; falls back to the mock security-rules
  // resource filtered by group name when the daemon hasn't
  // implemented it yet (most of the time today). Save is live-only
  // — mock-mode surfaces a 503 inline.
  import { getSecurityGroupRules, setSecurityGroupRules, type SecurityRule, type Row } from '../api';

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
  let description = $derived(typeof row.description === 'string' ? row.description : '');
  let project = $derived(typeof row.project === 'string' ? row.project : '—');

  let rules = $state<SecurityRule[]>([]);
  let originalRulesJSON = $state(''); // for dirty detection
  let loading = $state(true);
  let loadErr = $state('');
  let saveErr = $state('');
  let saveBusy = $state(false);

  function blankRule(): SecurityRule {
    return { direction: 'ingress', protocol: 'tcp', port_min: 0, port_max: 0, remote_cidr: '0.0.0.0/0', remote_group_uuid: '' };
  }

  async function refresh() {
    loading = true; loadErr = '';
    try {
      const rs = await getSecurityGroupRules(uuid);
      rules = rs;
      originalRulesJSON = JSON.stringify(rs);
    } catch (e) { loadErr = String(e); }
    finally { loading = false; }
  }

  $effect(() => { uuid; refresh(); });

  function addRule()    { rules = [...rules, blankRule()]; }
  function removeRule(i: number) { rules = rules.filter((_, idx) => idx !== i); }
  function update(i: number, patch: Partial<SecurityRule>) {
    rules = rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r));
  }

  let dirty = $derived(JSON.stringify(rules) !== originalRulesJSON);

  async function save() {
    saveBusy = true; saveErr = '';
    try {
      await setSecurityGroupRules(uuid, rules);
      originalRulesJSON = JSON.stringify(rules);
      onChanged();
    } catch (e) { saveErr = String(e); }
    finally { saveBusy = false; }
  }
</script>

<!-- Backdrop -->
<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-3xl flex-col bg-base-100 shadow-2xl">
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div>
      <div class="flex items-baseline gap-2">
        <h2 class="text-lg font-bold">{name}</h2>
        <span class="badge badge-sm badge-ghost">security group</span>
      </div>
      <p class="text-xs text-base-content/60">
        project {project} · {description || 'no description'}
      </p>
      <p class="font-mono text-[10px] text-base-content/40">{uuid}</p>
    </div>
    <div class="ml-auto flex items-center gap-1">
      <button class="btn btn-ghost btn-xs" title="Reload" onclick={refresh}>↻</button>
      <button class="btn btn-ghost btn-xs" aria-label="Close" onclick={onClose}>✕</button>
    </div>
  </header>

  <div class="flex-1 overflow-y-auto px-5 py-4">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold">Rules</h3>
      <button class="ml-auto btn btn-xs btn-ghost" onclick={addRule}>+ rule</button>
    </div>

    {#if loading}
      <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
    {:else if loadErr}
      <div class="mt-2 alert alert-error text-sm">{loadErr}</div>
    {:else if rules.length === 0}
      <p class="mt-3 text-sm text-base-content/50">No rules — the group denies on all directions.</p>
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

    {#if saveErr}
      <div class="mt-3 alert alert-error text-sm">{saveErr}</div>
    {/if}
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
      Save rules
    </button>
  </footer>
</aside>
