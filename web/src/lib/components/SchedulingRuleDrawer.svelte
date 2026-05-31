<script lang="ts">
  // SchedulingRuleDrawer — the "deployment" view : a rule that
  // expands to N replicas, plus the list of microVMs deployed under it.
  // Two tabs :
  //
  //   General — rename + description + read-only summary (count,
  //     selector, placement constraints, status).
  //   microVMs — table of microVMs whose `scheduling_rule` matches
  //     this rule's name. Mirrors the master-detail pattern.
  //
  // Replaces the generic EditableDrawer mount for 'scheduling-rules'
  // in ResourcePage.
  import {
    getEditableMetadata, setEditableMetadata, renameEditableRow,
    listSchedulingRuleMicroVMs, updateSchedulingRule,
    type EditableMetadata, type Row,
  } from '../api';

  let {
    row,
    onClose,
    onChanged,
  }: {
    row: Row;
    onClose: () => void;
    onChanged: () => void;
  } = $props();

  let key = $derived(String(row.name));
  let count = $derived(typeof row.count === 'number' ? row.count : '—');
  let ready = $derived(typeof row.ready === 'number' ? row.ready : '—');
  let selector = $derived(String(row.selector ?? ''));
  let placement = $derived(String(row.placement ?? '—'));
  let project = $derived(String(row.project ?? '—'));
  let status = $derived(String(row.status ?? '—'));

  type Tab = 'general' | 'placement' | 'microvms';
  let tab = $state<Tab>('general');

  // ---- Placement tab ----
  //
  // Mirrors the create modal's axis editor : kind ∈ {any,same,different,specific}
  // + a specific value when kind='specific'. The row carries a pre-formatted
  // `placement` string (e.g. "az=different, host=DC-C"). We parse it back
  // into the three axes on mount so the form starts with current state.
  type AxisKind = 'any' | 'same' | 'different' | 'specific';

  let editCount    = $state<number>(0);
  let editSelector = $state<string>('');
  let azKind       = $state<AxisKind>('any');
  let azSpec       = $state<string>('');
  let rackKind     = $state<AxisKind>('any');
  let rackSpec     = $state<string>('');
  let hostKind     = $state<AxisKind>('any');
  let hostSpec     = $state<string>('');

  let placementBusy = $state(false);
  let placementErr  = $state('');
  let placementOk   = $state(false);

  // Parse "az=different, rack=R2, host=…" back into {az,rack,host}. The
  // server keeps the typed fields on the rule but the row projection only
  // surfaces the compact joined form, so we round-trip-parse here.
  function parsePlacement(p: string): Record<'az' | 'rack' | 'host', string> {
    const out: Record<'az' | 'rack' | 'host', string> = { az: '', rack: '', host: '' };
    if (!p || p === 'any') return out;
    for (const part of p.split(',')) {
      const [k, ...rest] = part.trim().split('=');
      const v = rest.join('=').trim();
      if (k === 'az' || k === 'rack' || k === 'host') out[k] = v;
    }
    return out;
  }
  function valueToKind(v: string): { kind: AxisKind; spec: string } {
    if (!v)                      return { kind: 'any',       spec: '' };
    if (v === 'same')            return { kind: 'same',      spec: '' };
    if (v === 'different')       return { kind: 'different', spec: '' };
    return { kind: 'specific', spec: v };
  }
  function kindToValue(k: AxisKind, spec: string): string {
    if (k === 'any')      return '';
    if (k === 'specific') return spec.trim();
    return k;
  }

  function seedPlacement() {
    editCount    = typeof row.count === 'number'
      ? row.count
      // count is the formatted "X/Y" string from the row projection ;
      // pull the desired side back out so the input edits a real number.
      : Number(String(row.count ?? '').split('/').pop() || 0) || 0;
    editSelector = String(row.selector ?? '');
    const cur = parsePlacement(String(row.placement ?? ''));
    ({ kind: azKind,   spec: azSpec   } = valueToKind(cur.az));
    ({ kind: rackKind, spec: rackSpec } = valueToKind(cur.rack));
    ({ kind: hostKind, spec: hostSpec } = valueToKind(cur.host));
    placementErr = ''; placementOk = false;
  }

  async function savePlacement() {
    placementBusy = true; placementErr = ''; placementOk = false;
    try {
      await updateSchedulingRule(key, {
        count:    editCount,
        selector: editSelector.trim(),
        az:       kindToValue(azKind,   azSpec),
        rack:     kindToValue(rackKind, rackSpec),
        host:     kindToValue(hostKind, hostSpec),
      });
      placementOk = true;
      onChanged();
    } catch (e) {
      placementErr = String(e);
    } finally {
      placementBusy = false;
    }
  }

  // ---- General tab ----
  let editName = $state('');
  let editDescription = $state('');
  let metadata = $state<EditableMetadata | null>(null);
  let metaLoading = $state(true);
  let metaErr = $state('');
  let saveBusy = $state(false);
  let saveErr = $state('');
  let saveOk = $state(false);

  async function refreshMetadata() {
    metaLoading = true; metaErr = '';
    try {
      const m = await getEditableMetadata('scheduling-rules', key);
      metadata = m;
      editDescription = m.description ?? '';
    } catch (e) {
      metaErr = String(e);
    } finally {
      metaLoading = false;
    }
  }

  $effect(() => {
    key; // dep
    editName = String(row.name ?? '');
    seedPlacement();
    refreshMetadata();
    refreshMicroVMs();
  });

  let nameDirty = $derived(editName !== String(row.name ?? ''));
  let descriptionDirty = $derived(metadata !== null && editDescription !== (metadata?.description ?? ''));

  async function saveGeneral() {
    if (!nameDirty && !descriptionDirty) return;
    saveBusy = true; saveErr = ''; saveOk = false;
    try {
      let currentKey = key;
      if (nameDirty) {
        const newName = editName.trim();
        if (!newName) throw new Error('name is required');
        await renameEditableRow('scheduling-rules', currentKey, newName);
        currentKey = newName;
      }
      if (descriptionDirty || nameDirty) {
        await setEditableMetadata('scheduling-rules', currentKey, editDescription);
      }
      saveOk = true;
      onChanged();
    } catch (e) {
      saveErr = String(e);
    } finally {
      saveBusy = false;
    }
  }

  // ---- microVMs tab ----
  let vms = $state<Row[]>([]);
  let vmsLoading = $state(true);
  let vmsErr = $state('');

  async function refreshMicroVMs() {
    vmsLoading = true; vmsErr = '';
    try {
      vms = await listSchedulingRuleMicroVMs(key);
    } catch (e) {
      vmsErr = String(e);
    } finally {
      vmsLoading = false;
    }
  }

  function statusClass(v: unknown): string {
    switch (String(v).toLowerCase()) {
      case 'running': return 'badge-success';
      case 'starting':
      case 'pending': return 'badge-warning';
      case 'failed':
      case 'error': return 'badge-error';
      case 'stopped': return 'badge-ghost';
      default: return 'badge-ghost';
    }
  }
  function fmtUpdated(ts: string): string {
    if (!ts) return '—';
    return ts.slice(0, 19).replace('T', ' ');
  }
</script>

<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-4xl flex-col bg-base-100 shadow-2xl">
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div>
      <h2 class="text-lg font-bold">{key}</h2>
      <p class="text-xs text-base-content/60">
        {ready} / {count} ready · selector <span class="font-mono">{selector || '—'}</span>
        · placement {placement} · project {project}
        · <span class="badge badge-xs {statusClass(status)}">{status}</span>
      </p>
    </div>
    <button class="ml-auto btn btn-sm btn-ghost" aria-label="Close" onclick={onClose}>✕</button>
  </header>

  <div role="tablist" class="tabs tabs-border shrink-0 px-5">
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'general'}
      onclick={() => (tab = 'general')}>General</button>
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'placement'}
      onclick={() => (tab = 'placement')}>Placement</button>
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'microvms'}
      onclick={() => (tab = 'microvms')}>microVMs <span class="badge badge-xs badge-ghost ml-1">{vms.length}</span></button>
  </div>

  <div class="min-h-0 flex-1 overflow-y-auto p-5">
    {#if tab === 'general'}
      {#if metaErr}<div class="alert alert-error py-2 text-sm">{metaErr}</div>{/if}

      {#if metaLoading}
        <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
      {:else}
        <div class="grid gap-3">
          <label class="form-control">
            <span class="label-text mb-1 text-xs">Name</span>
            <input class="input input-sm input-bordered font-mono" bind:value={editName} />
          </label>

          <label class="form-control">
            <span class="label-text mb-1 text-xs">Description</span>
            <textarea class="textarea textarea-sm textarea-bordered" rows="4"
              placeholder="What this deployment is for, ownership, scaling notes…"
              bind:value={editDescription}></textarea>
          </label>

          <dl class="mt-2 grid grid-cols-2 gap-2 text-xs">
            <div><dt class="text-base-content/50">Replicas (ready / count)</dt><dd class="font-mono">{ready} / {count}</dd></div>
            <div><dt class="text-base-content/50">Placement</dt><dd class="font-mono">{placement}</dd></div>
            <div class="col-span-2"><dt class="text-base-content/50">Selector</dt><dd class="font-mono">{selector || '—'}</dd></div>
            {#if metadata?.updated_by}
              <div class="col-span-2"><dt class="text-base-content/50">Last edit</dt>
                <dd class="font-mono">{fmtUpdated(metadata.updated_at)} · {metadata.updated_by}</dd></div>
            {/if}
          </dl>

          {#if saveErr}<div class="alert alert-error py-2 text-sm">{saveErr}</div>{/if}
          {#if saveOk}<div class="alert alert-success py-2 text-sm">Saved.</div>{/if}

          <div class="mt-2 flex">
            <button class="ml-auto btn btn-sm btn-primary"
              disabled={(!nameDirty && !descriptionDirty) || saveBusy}
              onclick={saveGeneral}>
              {#if saveBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              Save
            </button>
          </div>
        </div>
      {/if}

    {:else if tab === 'placement'}
      <!-- Placement tab : PATCH the rule's count / selector / axes.
           Mirrors the create modal so operators learn the form once. -->
      <p class="text-xs text-base-content/60">
        Mutate the rule in place. The scheduler reconciles ready
        replicas asynchronously ; clear an axis back to <em>any</em> to
        relax the constraint without deleting the rule.
      </p>

      <div class="mt-3 grid gap-3 sm:grid-cols-2">
        <label class="form-control">
          <span class="label-text text-xs">Desired replicas</span>
          <input type="number" min="0" class="input input-sm input-bordered tabular-nums"
            bind:value={editCount} />
        </label>
        <label class="form-control">
          <span class="label-text text-xs">Selector</span>
          <input class="input input-sm input-bordered font-mono"
            placeholder="app=foo, kind=worker" bind:value={editSelector} />
        </label>
      </div>

      <div class="mt-3 rounded-box border border-base-300 p-3">
        <div class="text-xs font-medium text-base-content/70">Placement (AZ ⊃ Rack ⊃ Host)</div>
        {#each [
          { label: 'AZ',   kind: azKind,   setKind: (v: AxisKind) => (azKind = v),   spec: azSpec,   setSpec: (v: string) => (azSpec = v),   placeholder: 'DC-C' },
          { label: 'Rack', kind: rackKind, setKind: (v: AxisKind) => (rackKind = v), spec: rackSpec, setSpec: (v: string) => (rackSpec = v), placeholder: 'R2' },
          { label: 'Host', kind: hostKind, setKind: (v: AxisKind) => (hostKind = v), spec: hostSpec, setSpec: (v: string) => (hostSpec = v), placeholder: 'dc-a-r1-h2' },
        ] as axis (axis.label)}
          <div class="mt-2 grid grid-cols-[5rem_1fr_1fr] items-center gap-2">
            <span class="text-sm font-medium">{axis.label}</span>
            <select class="select select-sm select-bordered"
              value={axis.kind}
              onchange={(e) => axis.setKind(e.currentTarget.value as AxisKind)}>
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

      {#if placementErr}<div class="mt-3 alert alert-error py-2 text-sm">{placementErr}</div>{/if}
      {#if placementOk}<div class="mt-3 alert alert-success py-2 text-sm">Saved.</div>{/if}

      <div class="mt-3 flex">
        <button class="ml-auto btn btn-sm btn-ghost" onclick={seedPlacement}>Reset</button>
        <button class="btn btn-sm btn-primary ml-2"
          disabled={placementBusy} onclick={savePlacement}>
          {#if placementBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Save
        </button>
      </div>

    {:else}
      <!-- microVMs tab -->
      <p class="text-xs text-base-content/60">
        microVMs deployed under this rule (by nominal binding —
        <code>scheduling_rule = "{key}"</code> on the VM). The
        scheduler keeps the count topped up ; failed replicas show
        the latest scheduling status.
      </p>

      {#if vmsErr}<div class="mt-3 alert alert-error py-2 text-sm">{vmsErr}</div>{/if}

      <div class="mt-3 rounded-box border border-base-300 bg-base-100">
        <table class="table table-sm">
          <thead>
            <tr>
              <th>Name</th>
              <th>Image</th>
              <th>Host</th>
              <th>IP</th>
              <th>Flavor</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {#if vmsLoading}
              <tr><td colspan="6" class="py-6 text-center">
                <span class="loading loading-spinner"></span>
              </td></tr>
            {:else if vms.length === 0}
              <tr><td colspan="6" class="py-6 text-center text-base-content/50">
                No microVMs deployed under this rule yet. The scheduler
                will materialise replicas matching <code>scheduling_rule
                = "{key}"</code>.
              </td></tr>
            {:else}
              {#each vms as v (v.name)}
                <tr class="hover">
                  <td class="font-mono">{v.name}</td>
                  <td class="font-mono text-xs">{v.image}</td>
                  <td class="text-xs text-base-content/70">{v.host ?? '—'}</td>
                  <td class="font-mono text-xs">{v.ip ?? '—'}</td>
                  <td><span class="badge badge-xs badge-ghost">{v.flavor ?? '—'}</span></td>
                  <td><span class="badge badge-sm {statusClass(v.status)}">{v.status}</span></td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
</aside>
