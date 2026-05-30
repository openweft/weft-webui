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
    listVMKeys, addVMKey, removeVMKey,
    listVMProperties, setVMProperty, removeVMProperty,
    listUEFIVars, setUEFIVar, removeUEFIVar,
    type VMStatus, type VMTimingEvent, type VMLogs, type Row, type VMSSHKey,
    type VMProperty, type UEFIVar,
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

  let tab = $state<'summary' | 'volumes' | 'keys' | 'props' | 'uefi' | 'timings' | 'logs'>('summary');

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

  // SSH keys tab : pushed at runtime (not baked into create-time
  // SSHPub), the guest's weft-vm-agent applies via NATS — same
  // Subscriber+ApplyFunc pattern as the mesh / mounts concerns. The
  // dashboard surface is plain CRUD over the names.
  let keys = $state<VMSSHKey[] | null>(null);
  let keysErr = $state('');
  let keysBusy = $state(false);
  let newKey = $state('');
  let addKeyBusy = $state(false);
  let addKeyErr = $state('');

  // Properties tab : host-set key/value annotations. The guest_readable
  // flag opts the entry into the guest-side read surface (NATS).
  let vmProperties = $state<VMProperty[] | null>(null);
  let propsErr = $state('');
  let propsBusy = $state(false);
  let propKey = $state('');
  let propValue = $state('');
  let propGuestReadable = $state(false);
  let propBusy = $state(false);
  let propAddErr = $state('');

  // UEFI vars tab : firmware NVRAM editor. Values stay in hex on the
  // wire ; we let the operator paste hex pairs (with optional spaces
  // for readability) and validate server-side.
  let uefi = $state<UEFIVar[] | null>(null);
  let uefiErr = $state('');
  let uefiBusy = $state(false);
  let uefiName = $state('');
  let uefiNS = $state('');         // empty → server defaults to EFI Global
  let uefiValueHex = $state('');
  let uefiAttrs = $state<string[]>(['NonVolatile', 'BootServiceAccess', 'RuntimeAccess']);
  const allAttrs = ['NonVolatile', 'BootServiceAccess', 'RuntimeAccess', 'HardwareErrorRecord', 'AuthenticatedWriteAccess', 'TimeBasedAuthenticatedWriteAccess', 'AppendWrite'];
  let uefiAddBusy = $state(false);
  let uefiAddErr = $state('');

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
  async function loadKeys() {
    keysBusy = true; keysErr = '';
    try { keys = await listVMKeys(name); }
    catch (e) { keysErr = String(e); }
    finally { keysBusy = false; }
  }
  async function submitKey() {
    const v = newKey.trim();
    if (!v) return;
    addKeyBusy = true; addKeyErr = '';
    try {
      await addVMKey(name, v);
      newKey = '';
      await loadKeys();
    } catch (e) { addKeyErr = String(e); }
    finally { addKeyBusy = false; }
  }
  async function delKey(fp: string) {
    if (!confirm(`Remove key ${fp.slice(0, 25)}… ? It stops authorising next session ; existing connections aren't dropped.`)) return;
    try {
      await removeVMKey(name, fp);
      await loadKeys();
    } catch (e) { keysErr = String(e); }
  }

  async function loadProps() {
    propsBusy = true; propsErr = '';
    try { vmProperties = await listVMProperties(name); }
    catch (e) { propsErr = String(e); }
    finally { propsBusy = false; }
  }
  async function submitProp() {
    const k = propKey.trim();
    if (!k) return;
    propBusy = true; propAddErr = '';
    try {
      await setVMProperty(name, { key: k, value: propValue, guest_readable: propGuestReadable });
      propKey = ''; propValue = ''; propGuestReadable = false;
      await loadProps();
    } catch (e) { propAddErr = String(e); }
    finally { propBusy = false; }
  }
  async function delProp(k: string) {
    if (!confirm(`Remove property "${k}" ?`)) return;
    try {
      await removeVMProperty(name, k);
      await loadProps();
    } catch (e) { propsErr = String(e); }
  }
  async function toggleGuestReadable(p: VMProperty) {
    try {
      await setVMProperty(name, { key: p.key, value: p.value, guest_readable: !p.guest_readable });
      await loadProps();
    } catch (e) { propsErr = String(e); }
  }

  async function loadUEFI() {
    uefiBusy = true; uefiErr = '';
    try { uefi = await listUEFIVars(name); }
    catch (e) { uefiErr = String(e); }
    finally { uefiBusy = false; }
  }
  async function submitUEFI() {
    const n = uefiName.trim();
    if (!n) return;
    uefiAddBusy = true; uefiAddErr = '';
    try {
      await setUEFIVar(name, {
        namespace: uefiNS.trim() || undefined,
        name: n,
        value_hex: uefiValueHex.replace(/\s+/g, ''),
        attributes: uefiAttrs,
      });
      uefiName = ''; uefiNS = ''; uefiValueHex = '';
      await loadUEFI();
    } catch (e) { uefiAddErr = String(e); }
    finally { uefiAddBusy = false; }
  }
  async function delUEFI(v: UEFIVar) {
    if (!confirm(`Remove UEFI variable ${v.name} ? Next boot will see the firmware default.`)) return;
    try {
      await removeUEFIVar(name, v.namespace, v.name);
      await loadUEFI();
    } catch (e) { uefiErr = String(e); }
  }
  function toggleAttr(a: string) {
    uefiAttrs = uefiAttrs.includes(a) ? uefiAttrs.filter(x => x !== a) : [...uefiAttrs, a];
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

  // Lazy-load each tab the first time it's opened.
  $effect(() => {
    if (tab === 'timings' && !timings && !timingsErr) loadTimings();
    if (tab === 'logs' && !logs && !logsErr) loadLogs();
    if (tab === 'volumes' && !volumes && !volumesErr) loadVolumes();
    if (tab === 'keys' && !keys && !keysErr) loadKeys();
    if (tab === 'props' && !vmProperties && !propsErr) loadProps();
    if (tab === 'uefi' && !uefi && !uefiErr) loadUEFI();
  });

  async function refreshAll() {
    await Promise.allSettled([loadStatus(), loadTimings(), loadLogs(), loadVolumes(), loadKeys(), loadProps(), loadUEFI()]);
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
    <button role="tab" class="tab" class:tab-active={tab === 'keys'} onclick={() => (tab = 'keys')}>
      SSH keys
      {#if keys && keys.length > 0}<span class="ml-1 badge badge-xs">{keys.length}</span>{/if}
    </button>
    <button role="tab" class="tab" class:tab-active={tab === 'props'} onclick={() => (tab = 'props')}>
      Properties
      {#if vmProperties && vmProperties.length > 0}<span class="ml-1 badge badge-xs">{vmProperties.length}</span>{/if}
    </button>
    <button role="tab" class="tab" class:tab-active={tab === 'uefi'} onclick={() => (tab = 'uefi')}>
      UEFI
      {#if uefi && uefi.length > 0}<span class="ml-1 badge badge-xs">{uefi.length}</span>{/if}
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

    {:else if tab === 'keys'}
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold">SSH keys</h3>
        <span class="text-xs text-base-content/50">
          pushed at runtime · no cloud-init dependency
        </span>
        <button class="ml-auto btn btn-xs btn-ghost" disabled={keysBusy} onclick={loadKeys}>
          {#if keysBusy}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
        </button>
      </div>
      {#if keysErr}
        <div class="mt-2 alert alert-error text-sm">{keysErr}</div>
      {:else if !keys}
        <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
      {:else if keys.length === 0}
        <p class="mt-3 text-sm text-base-content/50">No keys authorised. Paste a public key below.</p>
      {:else}
        <ul class="mt-2 divide-y divide-base-300">
          {#each keys as k (k.fingerprint)}
            <li class="flex items-center gap-3 py-2 text-sm">
              <div class="min-w-0 grow">
                <div class="truncate font-mono text-xs">{k.fingerprint}</div>
                <div class="text-xs text-base-content/60">
                  <span class="badge badge-xs badge-ghost">{k.type}</span>
                  {k.comment || '—'}
                  <span class="ml-2 text-base-content/40">added {k.added_at.slice(0, 10)}</span>
                </div>
              </div>
              <button class="btn btn-xs btn-ghost text-error" onclick={() => delKey(k.fingerprint)}>
                Remove
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="mt-4">
        <label class="form-control">
          <span class="label-text mb-1 text-xs">Add public key</span>
          <textarea
            class="textarea textarea-sm textarea-bordered font-mono text-xs"
            rows="3"
            placeholder={'ssh-ed25519 AAAA… user@host'}
            bind:value={newKey}
          ></textarea>
          <span class="mt-1 text-xs text-base-content/50">
            One line, ssh-keygen format. Fingerprint is computed server-side ; same key added twice is a no-op.
          </span>
        </label>
        {#if addKeyErr}<div class="mt-2 alert alert-error py-2 text-xs">{addKeyErr}</div>{/if}
        <div class="mt-2 text-right">
          <button class="btn btn-xs btn-primary gap-1"
            disabled={addKeyBusy || !newKey.trim()}
            onclick={submitKey}
          >
            {#if addKeyBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
            Add key
          </button>
        </div>
      </div>

    {:else if tab === 'props'}
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold">Properties</h3>
        <span class="text-xs text-base-content/50">
          host-set annotations · guest-readable ones flow to weft-vm-agent via NATS
        </span>
        <button class="ml-auto btn btn-xs btn-ghost" disabled={propsBusy} onclick={loadProps}>
          {#if propsBusy}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
        </button>
      </div>
      {#if propsErr}
        <div class="mt-2 alert alert-error text-sm">{propsErr}</div>
      {:else if !vmProperties}
        <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
      {:else if vmProperties.length === 0}
        <p class="mt-3 text-sm text-base-content/50">No properties set.</p>
      {:else}
        <table class="table table-sm mt-2">
          <thead>
            <tr>
              <th>Key</th>
              <th>Value</th>
              <th class="w-24 text-center" title="Guest-readable : the in-VM agent can read this">Guest</th>
              <th class="w-16"></th>
            </tr>
          </thead>
          <tbody>
            {#each vmProperties as p (p.key)}
              <tr>
                <td class="font-mono text-xs">{p.key}</td>
                <td class="font-mono text-xs">{p.value}</td>
                <td class="text-center">
                  <input type="checkbox" class="checkbox checkbox-xs"
                    checked={p.guest_readable}
                    onchange={() => toggleGuestReadable(p)} />
                </td>
                <td>
                  <button class="btn btn-xs btn-ghost text-error" onclick={() => delProp(p.key)}>×</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}

      <div class="mt-4 rounded-box border border-base-300 p-3">
        <div class="text-xs font-semibold mb-2">Add / update a property</div>
        <div class="grid grid-cols-[1fr_1fr] gap-2">
          <input class="input input-xs input-bordered font-mono" placeholder="key (e.g. owner)" bind:value={propKey} />
          <input class="input input-xs input-bordered font-mono" placeholder="value" bind:value={propValue} />
        </div>
        <label class="label cursor-pointer justify-start gap-2 mt-2">
          <input type="checkbox" class="checkbox checkbox-xs" bind:checked={propGuestReadable} />
          <span class="label-text text-xs">Guest-readable (weft-vm-agent inside the VM can read this value)</span>
        </label>
        {#if propAddErr}<div class="mt-2 alert alert-error py-2 text-xs">{propAddErr}</div>{/if}
        <div class="mt-2 text-right">
          <button class="btn btn-xs btn-primary"
            disabled={propBusy || !propKey.trim()}
            onclick={submitProp}>
            {#if propBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
            Save
          </button>
        </div>
      </div>

    {:else if tab === 'uefi'}
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold">UEFI variables</h3>
        <span class="text-xs text-base-content/50">
          firmware NVRAM · raw bytes shown as hex
        </span>
        <button class="ml-auto btn btn-xs btn-ghost" disabled={uefiBusy} onclick={loadUEFI}>
          {#if uefiBusy}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
        </button>
      </div>
      {#if uefiErr}
        <div class="mt-2 alert alert-error text-sm">{uefiErr}</div>
      {:else if !uefi}
        <div class="py-8 text-center"><span class="loading loading-spinner loading-md"></span></div>
      {:else if uefi.length === 0}
        <p class="mt-3 text-sm text-base-content/50">No UEFI variables stored. Firmware defaults apply.</p>
      {:else}
        <ul class="mt-2 divide-y divide-base-300">
          {#each uefi as v (v.namespace + '/' + v.name)}
            <li class="py-2 text-sm">
              <div class="flex items-baseline gap-2">
                <span class="font-mono font-semibold">{v.name}</span>
                <span class="text-xs text-base-content/50 font-mono truncate" title={v.namespace}>{v.namespace.slice(0, 8)}…</span>
                <button class="ml-auto btn btn-xs btn-ghost text-error" onclick={() => delUEFI(v)}>Remove</button>
              </div>
              <div class="mt-1 flex flex-wrap gap-1">
                {#each v.attributes as a (a)}
                  <span class="badge badge-xs badge-ghost">{a}</span>
                {/each}
              </div>
              <div class="mt-1 font-mono text-xs break-all text-base-content/70">
                {v.value_hex || '(empty)'}
              </div>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="mt-4 rounded-box border border-base-300 p-3">
        <div class="text-xs font-semibold mb-2">Add / update a variable</div>
        <div class="grid grid-cols-[1fr_2fr] gap-2">
          <input class="input input-xs input-bordered font-mono" placeholder="name (e.g. BootOrder)" bind:value={uefiName} />
          <input class="input input-xs input-bordered font-mono"
            placeholder="namespace GUID (empty = EFI Global)" bind:value={uefiNS} />
        </div>
        <label class="form-control mt-2">
          <span class="label-text text-xs">Value (hex pairs, spaces allowed)</span>
          <textarea class="textarea textarea-sm textarea-bordered font-mono text-xs"
            rows="2" placeholder="0000  or  01 00 00 00 58 00 …" bind:value={uefiValueHex}></textarea>
        </label>
        <div class="mt-2 flex flex-wrap gap-2">
          {#each allAttrs as a (a)}
            <label class="label cursor-pointer gap-1">
              <input type="checkbox" class="checkbox checkbox-xs"
                checked={uefiAttrs.includes(a)} onchange={() => toggleAttr(a)} />
              <span class="label-text text-xs">{a}</span>
            </label>
          {/each}
        </div>
        {#if uefiAddErr}<div class="mt-2 alert alert-error py-2 text-xs">{uefiAddErr}</div>{/if}
        <div class="mt-2 text-right">
          <button class="btn btn-xs btn-primary"
            disabled={uefiAddBusy || !uefiName.trim()}
            onclick={submitUEFI}>
            {#if uefiAddBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
            Save
          </button>
        </div>
      </div>

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
