<script lang="ts">
  // Right-side drawer that opens when a microVM row is clicked.
  // Three tabs hit three RPCs lazily on first activation :
  //   Summary  → /api/microvms/:name/status   (VMStatus)
  //   Timings  → /api/microvms/:name/timings  (VMTimings)
  //   Logs     → /api/microvms/:name/logs     (VMLogs)
  //
  // Each tab carries its own refresh button so the operator can poll
  // without re-opening the drawer. A "Refresh all" up top hits all
  // three at once. Mock-mode (no daemon) surfaces the 503 inline
  // instead of leaving the panel empty.
  import { onMount } from 'svelte';
  import {
    getVMStatus, getVMTimings, getVMLogs,
    startVM, stopVM, deleteVM,
    type VMStatus, type VMTimingEvent, type VMLogs, type Row,
  } from '../api';

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

  let tab = $state<'summary' | 'timings' | 'logs'>('summary');

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

  onMount(loadStatus);

  // Lazy-load timings / logs the first time their tab is shown.
  $effect(() => {
    if (tab === 'timings' && !timings && !timingsErr) loadTimings();
    if (tab === 'logs' && !logs && !logsErr) loadLogs();
  });

  async function refreshAll() {
    await Promise.allSettled([loadStatus(), loadTimings(), loadLogs()]);
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
    <button role="tab" class="tab" class:tab-active={tab === 'timings'} onclick={() => (tab = 'timings')}>Timings</button>
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
