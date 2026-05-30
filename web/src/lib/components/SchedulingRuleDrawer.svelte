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
    listSchedulingRuleMicroVMs,
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

  type Tab = 'general' | 'microvms';
  let tab = $state<Tab>('general');

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
