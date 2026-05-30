<script lang="ts">
  // Right-side drawer that opens when a microVM row is clicked.
  // Four tabs hit different RPCs lazily on first activation :
  //   Summary  → /api/microvms/:name/status        (VMStatus)
  //   Volumes  → /api/resources/volumes (filtered) (ListVolumes)
  //   Timings  → /api/microvms/:name/timings       (VMTimings)
  //   Logs     → /api/microvms/:name/logs          (VMLogs)
  //
  // Each tab carries its own refresh button so the operator can poll
  // without re-opening the drawer. A "Refresh all" up top hits all
  // of them at once. Mock-mode (no daemon) surfaces the 503 inline
  // instead of leaving the panel empty.
  import { onMount, onDestroy } from 'svelte';
  import {
    getVMStatus, getVMTimings, getVMLogs, getRows,
    startVM, stopVM, deleteVM,
    attachVolume, detachVolume,
    type VMStatus, type VMTimingEvent, type VMLogs, type Row,
  } from '../api';
  import { openScopedEvents } from '../events';

  let {
    row,
    onClose,
    onChanged,
  }: {
    row: Row;
    onClose: () => void;
    onChanged: () => void; // table refresh on start/stop/delete
  } = $props();

  // Derived so a future re-mount with a different row works ; today
  // ResourcePage unmounts the drawer between selections, but this
  // keeps the prop tracking sound.
  let name = $derived(row.name as string);

  let tab = $state<'summary' | 'volumes' | 'timings' | 'logs'>('summary');

  // Per-tab loading + data + error.
  let status = $state<VMStatus | null>(null);
  let statusErr = $state('');
  let statusBusy = $state(false);

  let timings = $state<VMTimingEvent[] | null>(null);
  let timingsErr = $state('');
  let timingsBusy = $state(false);

  let logs = $state<VMLogs | null>(null);
  let logsErr = $state('');
  let logsBusy = $state(false);

  // Volumes tab : the table-wide volume list, filtered by
  // attached_to == this VM's name OR uuid. The "available" pool is the
  // detached subset of the same project, used by the Attach picker.
  let volumes = $state<Row[] | null>(null);
  let volumesErr = $state('');
  let volumesBusy = $state(false);
  let attachBusy = $state<string | null>(null); // volume uuid being attached/detached
  // Inline "Attach" picker state.
  let pickerOpen = $state(false);
  let pickerError = $state('');

  // Action-button state.
  let actionErr = $state('');
  let actionBusy = $state(false);

  async function loadStatus() {
    statusBusy = true; statusErr = '';
    try { status = await getVMStatus(name); }
    catch (e) { statusErr = String(e); }
    finally { statusBusy = false; }
  }
  async function loadTimings() {
    timingsBusy = true; timingsErr = '';
    try { timings = await getVMTimings(name); }
    catch (e) { timingsErr = String(e); }
    finally { timingsBusy = false; }
  }
  async function loadLogs() {
    logsBusy = true; logsErr = '';
    try { logs = await getVMLogs(name); }
    catch (e) { logsErr = String(e); }
    finally { logsBusy = false; }
  }
  async function loadVolumes() {
    volumesBusy = true; volumesErr = '';
    try { volumes = await getRows('volumes'); }
    catch (e) { volumesErr = String(e); }
    finally { volumesBusy = false; }
  }

  onMount(loadStatus);

  // Per-VM event subscription : the agent's WatchEvents stream is
  // filtered to the VM's name so every state transition, network up,
  // exec ready, etc. lands here directly. Each incoming event is
  // prepended to the timings list so the Timings tab updates live
  // without the operator having to refresh.
  let scopedClose: (() => void) | null = null;
  let liveEvents = $state(0); // ticker shown next to the Timings tab label
  $effect(() => {
    if (!name) return;
    const { source, close } = openScopedEvents({ kindPrefix: 'vm.', subject: name });
    source.onmessage = (e) => {
      try {
        const ev = JSON.parse(e.data) as { ts: string; kind: string; subject: string; meta?: Record<string, string> };
        timings = [
          { name: ev.kind, ts: ev.ts, meta: ev.meta ?? {} },
          ...(timings ?? []),
        ];
        liveEvents++;
      } catch { /* malformed frame */ }
    };
    scopedClose?.();
    scopedClose = close;
    return () => close();
  });
  onDestroy(() => scopedClose?.());

  // Lazy-load timings / logs / volumes the first time their tab is shown.
  $effect(() => {
    if (tab === 'timings' && !timings && !timingsErr) loadTimings();
    if (tab === 'logs' && !logs && !logsErr) loadLogs();
    if (tab === 'volumes' && !volumes && !volumesErr) loadVolumes();
  });

  async function refreshAll() {
    await Promise.allSettled([loadStatus(), loadTimings(), loadLogs(), loadVolumes()]);
  }

  // Volume sets : attached to this VM, vs available (detached) in
  // the same project. We match on attached_to == VM name (the mock
  // uses names) OR the VM's UUID (live mode).
  let vmUUID = $derived((status?.name ? (status as VMStatus & {uuid?: string}).uuid : (row.uuid as string)) || '');
  let projectScope = $derived(String(row.project ?? ''));
  let attached = $derived.by<Row[]>(() => {
    if (!volumes) return [];
    return volumes.filter((v) => {
      const at = String(v.attached_to ?? '');
      return at === name || (vmUUID && at === vmUUID);
    });
  });
  let available = $derived.by<Row[]>(() => {
    if (!volumes) return [];
    return volumes.filter((v) => {
      const at = String(v.attached_to ?? '');
      const sameProject = !projectScope || String(v.project ?? '') === projectScope;
      return at === '' && sameProject;
    });
  });

  async function attach(volumeUUID: string) {
    if (!vmUUID) {
      pickerError = 'this VM has no UUID yet — VMStatus must succeed first';
      return;
    }
    pickerError = '';
    attachBusy = volumeUUID;
    try {
      await attachVolume(volumeUUID, vmUUID);
      pickerOpen = false;
      await loadVolumes();
      onChanged();
    } catch (e) { pickerError = String(e); }
    finally { attachBusy = null; }
  }
  async function detach(volumeUUID: string) {
    if (!confirm('Detach the volume ? Data on the volume is preserved.')) return;
    attachBusy = volumeUUID;
    try {
      await detachVolume(volumeUUID);
      await loadVolumes();
      onChanged();
    } catch (e) { volumesErr = String(e); }
    finally { attachBusy = null; }
  }

  async function runAction(verb: 'start' | 'stop' | 'delete') {
    actionErr = '';
    actionBusy = true;
    try {
      if (verb === 'start') await startVM(name);
      if (verb === 'stop')  await stopVM(name);
      if (verb === 'delete') {
        if (!confirm(`Delete microVM ${name} ? This is irreversible.`)) {
          actionBusy = false;
          return;
        }
        await deleteVM(name);
        onChanged();
        onClose();
        return;
      }
      // start/stop : refresh status + parent table.
      await loadStatus();
      onChanged();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }

  function fmtBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
    if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
    return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
  }

  // Status badge colour, mirroring ResourceTable's logic.
  function statusClass(v: unknown): string {
    switch (String(v).toLowerCase()) {
      case 'running': return 'badge-success';
      case 'stopped': return 'badge-warning';
      case 'error':
      case 'failed': return 'badge-error';
      default: return 'badge-ghost';
    }
  }
</script>

<!-- Backdrop : click to close. -->
<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col bg-base-100 shadow-2xl">
  <!-- Header -->
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div>
      <div class="flex items-baseline gap-2">
        <h2 class="text-lg font-bold">{name}</h2>
        <span class="badge badge-sm {statusClass(status?.status ?? row.status)}">
          {status?.status ?? row.status ?? '—'}
        </span>
      </div>
      <p class="text-xs text-base-content/60">
        project {row.project ?? '—'} · host {row.host ?? '—'} · network {row.network ?? '—'}
      </p>
    </div>
    <div class="ml-auto flex items-center gap-1">
      <button class="btn btn-ghost btn-xs" title="Refresh all" onclick={refreshAll}>↻</button>
      <button class="btn btn-ghost btn-xs" aria-label="Close" onclick={onClose}>✕</button>
    </div>
  </header>

  <!-- Action bar -->
  <div class="flex shrink-0 items-center gap-2 border-b border-base-300 px-5 py-2">
    <button class="btn btn-xs btn-success" disabled={actionBusy} onclick={() => runAction('start')}>Start</button>
    <button class="btn btn-xs btn-warning" disabled={actionBusy} onclick={() => runAction('stop')}>Stop</button>
    <button class="btn btn-xs btn-error ml-auto" disabled={actionBusy} onclick={() => runAction('delete')}>Delete</button>
  </div>
  {#if actionErr}
    <div class="alert alert-error m-3 text-xs">{actionErr}</div>
  {/if}

  <!-- Tabs -->
  <div role="tablist" class="tabs tabs-bordered shrink-0 px-5">
    <button role="tab" class="tab" class:tab-active={tab === 'summary'} onclick={() => (tab = 'summary')}>Summary</button>
    <button role="tab" class="tab" class:tab-active={tab === 'volumes'} onclick={() => (tab = 'volumes')}>
      Volumes
      {#if attached.length > 0}<span class="ml-1 badge badge-xs">{attached.length}</span>{/if}
    </button>
    <button role="tab" class="tab" class:tab-active={tab === 'timings'} onclick={() => (tab = 'timings')}>
      Timings
      {#if liveEvents > 0}
        <span class="ml-1 inline-block h-1.5 w-1.5 rounded-full bg-success" title="{liveEvents} live event(s)"></span>
      {/if}
    </button>
    <button role="tab" class="tab" class:tab-active={tab === 'logs'}    onclick={() => (tab = 'logs')}>Logs</button>
  </div>

  <!-- Body -->
  <div class="flex-1 overflow-y-auto px-5 py-4">
    {#if tab === 'summary'}
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold">VM status</h3>
        <button class="ml-auto btn btn-xs btn-ghost" disabled={statusBusy} onclick={loadStatus}>
          {#if statusBusy}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
        </button>
      </div>
      {#if statusErr}
        <div class="mt-2 alert alert-error text-sm">{statusErr}</div>
      {:else if status}
        <dl class="mt-2 grid grid-cols-[8rem_1fr] gap-y-1 text-sm">
          <dt class="text-base-content/60">name</dt>      <dd class="font-mono">{status.name}</dd>
          <dt class="text-base-content/60">image</dt>     <dd class="font-mono">{status.image || '—'}</dd>
          <dt class="text-base-content/60">os</dt>        <dd class="font-mono">{status.os || '—'}</dd>
          <dt class="text-base-content/60">ip</dt>        <dd class="font-mono">{status.ip || '—'}</dd>
          <dt class="text-base-content/60">cpu</dt>       <dd class="tabular-nums">{status.cpu}</dd>
          <dt class="text-base-content/60">memory</dt>    <dd class="tabular-nums">{status.mem_mb} MB</dd>
          <dt class="text-base-content/60">disk</dt>      <dd class="tabular-nums">{status.disk_gb} GB</dd>
        </dl>
      {:else}
        <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
      {/if}

    {:else if tab === 'volumes'}
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold">Attached volumes</h3>
        <button class="ml-auto btn btn-xs btn-ghost" disabled={volumesBusy} onclick={loadVolumes}>
          {#if volumesBusy}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
        </button>
      </div>
      {#if volumesErr}
        <div class="mt-2 alert alert-error text-sm">{volumesErr}</div>
      {:else if !volumes}
        <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
      {:else if attached.length === 0}
        <p class="mt-3 text-sm text-base-content/50">No volumes attached.</p>
      {:else}
        <ul class="mt-2 divide-y divide-base-300">
          {#each attached as v (v.uuid ?? v.name)}
            <li class="flex items-baseline gap-3 py-2 text-sm">
              <span class="font-medium">{v.name}</span>
              <span class="text-xs text-base-content/60">{v.size_gib} GiB · {v.format}</span>
              <button class="ml-auto btn btn-xs btn-ghost text-error"
                disabled={!!attachBusy}
                onclick={() => detach(String(v.uuid))}>
                {#if attachBusy === v.uuid}<span class="loading loading-spinner loading-xs"></span>{/if}
                Detach
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="mt-4 flex items-center gap-2">
        <h3 class="text-sm font-semibold">Attach</h3>
        <button class="ml-auto btn btn-xs btn-primary"
          disabled={pickerOpen || !volumes || available.length === 0}
          onclick={() => { pickerOpen = true; pickerError = ''; }}>
          {#if available.length === 0}no available volumes{:else}+ attach…{/if}
        </button>
      </div>
      {#if pickerOpen}
        <div class="mt-2 rounded-box border border-base-300 p-2">
          <p class="text-xs text-base-content/60">Pick a detached volume from project {projectScope || '—'}.</p>
          <ul class="mt-2 max-h-60 divide-y divide-base-300 overflow-y-auto">
            {#each available as v (v.uuid ?? v.name)}
              <li class="flex items-baseline gap-3 py-2 text-sm">
                <button class="font-medium hover:underline text-left grow"
                  disabled={!!attachBusy}
                  onclick={() => attach(String(v.uuid))}>
                  {v.name}
                </button>
                <span class="text-xs text-base-content/60">{v.size_gib} GiB · {v.format}</span>
                {#if attachBusy === v.uuid}<span class="loading loading-spinner loading-xs"></span>{/if}
              </li>
            {/each}
          </ul>
          {#if pickerError}<div class="mt-2 alert alert-error py-2 text-xs">{pickerError}</div>{/if}
          <div class="mt-2 text-right">
            <button class="btn btn-xs btn-ghost" onclick={() => (pickerOpen = false)}>Close</button>
          </div>
        </div>
      {/if}

    {:else if tab === 'timings'}
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold">Lifecycle events</h3>
        <button class="ml-auto btn btn-xs btn-ghost" disabled={timingsBusy} onclick={loadTimings}>
          {#if timingsBusy}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
        </button>
      </div>
      {#if timingsErr}
        <div class="mt-2 alert alert-error text-sm">{timingsErr}</div>
      {:else if timings && timings.length > 0}
        <ol class="mt-2 space-y-1 font-mono text-xs">
          {#each timings as e (e.ts + e.name)}
            <li class="flex items-start gap-3">
              <span class="shrink-0 text-base-content/50">{e.ts.replace('T', ' ').slice(0, 23)}</span>
              <span class="grow">{e.name}</span>
              {#if e.meta && Object.keys(e.meta).length > 0}
                <span class="text-base-content/50">
                  {Object.entries(e.meta).map(([k, v]) => `${k}=${v}`).join(' ')}
                </span>
              {/if}
            </li>
          {/each}
        </ol>
      {:else if timings}
        <p class="mt-4 text-sm text-base-content/50">No events recorded yet.</p>
      {:else}
        <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
      {/if}

    {:else if tab === 'logs'}
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold">Console</h3>
        {#if logs}
          <span class="text-xs text-base-content/50">
            tail · total {fmtBytes(logs.total_bytes)}
          </span>
        {/if}
        <button class="ml-auto btn btn-xs btn-ghost" disabled={logsBusy} onclick={loadLogs}>
          {#if logsBusy}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
        </button>
      </div>
      {#if logsErr}
        <div class="mt-2 alert alert-error text-sm">{logsErr}</div>
      {:else if logs}
        <pre class="mt-2 max-h-[70vh] overflow-auto rounded-box bg-base-200 p-3 font-mono text-xs leading-relaxed">{logs.contents || '(empty)'}</pre>
      {:else}
        <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
      {/if}
    {/if}
  </div>
</aside>
