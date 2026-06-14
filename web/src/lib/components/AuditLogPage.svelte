<script lang="ts">
  // AuditLogPage — browse the recent admin audit trail.
  //
  // Reads /api/audit-log (admin-scope) and renders a sortable /
  // filterable table. Real-time refresh on a 5 s tick so an operator
  // tracking an incident sees events appear as they're emitted. Server-
  // side filtering on `action` + `result` keeps the wire payload small
  // ; client-side filtering on `subject` would also be cheap but isn't
  // implemented yet.

  import { onMount, onDestroy } from 'svelte';
  import {
    tailAuditLog, listThrottledIPs, clearThrottledIP,
    type AuditEvent, type ResourceMeta, type ThrottledIP,
  } from '../api';

  let { meta }: { meta: ResourceMeta } = $props();

  let events = $state<AuditEvent[]>([]);
  let enabled = $state(true);
  let loadErr = $state('');
  let lastRefresh = $state<string>('');

  // Server-side filter knobs.
  let filterAction = $state('');
  let filterResult = $state<'' | 'ok' | 'error'>('');
  let filterSubject = $state('');
  // datetime-local emits "2026-06-02T13:42" (no seconds, no zone).
  // We append :00Z to land an RFC3339 the backend accepts.
  let filterSince = $state('');
  let filterUntil = $state('');

  function rfc3339(local: string): string | undefined {
    if (!local) return undefined;
    return local + ':00Z';
  }
  let limit = $state(200);

  let pollTimer: ReturnType<typeof setInterval> | undefined;

  // Throttled-IP state surfaces the per-IP failure budget operators
  // see at the top of the page : counts, lock status, "Unlock"
  // button. Refreshed on the same 5 s tick as the event tail.
  let throttled = $state<ThrottledIP[]>([]);

  async function refresh() {
    try {
      const r = await tailAuditLog({
        limit,
        action: filterAction || undefined,
        result: filterResult || undefined,
        subject: filterSubject || undefined,
        since: rfc3339(filterSince),
        until: rfc3339(filterUntil),
      });
      events = r.events;
      enabled = r.enabled;
      loadErr = '';
      lastRefresh = new Date().toLocaleTimeString();
    } catch (e) {
      loadErr = String(e);
    }
    // Throttle list is independent of the audit-log toggle — even
    // when audit is disabled the operator may want to see who's
    // bouncing off the gate.
    try {
      throttled = await listThrottledIPs();
    } catch {
      throttled = [];
    }
  }

  async function unlock(ip: string) {
    if (!confirm(`Clear the auth-callback throttle for ${ip} ?`)) return;
    try {
      await clearThrottledIP(ip);
      await refresh();
    } catch (e) {
      loadErr = String(e);
    }
  }

  onMount(() => {
    refresh();
    pollTimer = setInterval(refresh, 5000);
  });
  onDestroy(() => { if (pollTimer) clearInterval(pollTimer); });

  // Re-trigger refresh on filter changes (debounced via the next tick).
  $effect(() => {
    void filterAction;
    void filterResult;
    void filterSubject;
    void filterSince;
    void filterUntil;
    void limit;
    refresh();
  });

  function actionBadge(action: string): string {
    if (action.startsWith('auth.')) return 'badge-info';
    if (action.endsWith('.delete')) return 'badge-error';
    if (action.endsWith('.create') || action.endsWith('.update')) return 'badge-primary';
    return 'badge-ghost';
  }

  function resultBadge(r: string): string {
    if (r === 'ok') return 'badge-success';
    if (r === 'error') return 'badge-error';
    return 'badge-ghost';
  }

  function shortIP(ip: string): string {
    if (!ip) return '';
    // Collapse trailing zeros in IPv6 like ::ffff:192.0.2.1 → 192.0.2.1
    const m = ip.match(/::ffff:(\d+\.\d+\.\d+\.\d+)$/);
    return m ? m[1] : ip;
  }

  // Build the CSV-export URL with the same filters the in-page
  // table is showing — operator hands one file to compliance, no
  // post-processing.
  function exportCSVHref(): string {
    const p = new URLSearchParams();
    p.set('limit', String(limit));
    if (filterAction) p.set('action', filterAction);
    if (filterResult) p.set('result', filterResult);
    if (filterSubject) p.set('subject', filterSubject);
    if (filterSince) p.set('since', filterSince + ':00Z');
    if (filterUntil) p.set('until', filterUntil + ':00Z');
    return '/api/audit-log/export.csv?' + p.toString();
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      {events.length} most-recent events
      {#if lastRefresh}
        · <span class="text-xs text-base-content/40">refreshed {lastRefresh}</span>
      {/if}
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    {#if enabled}
      <a class="btn btn-sm btn-ghost gap-1" href={exportCSVHref()} title="Export the current filter set as CSV">
        <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 3v12m0 0 5-5m-5 5-5-5M5 21h14" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        Export CSV
      </a>
    {/if}
    <button class="btn btn-sm btn-ghost gap-1" onclick={refresh} title="Force refresh">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M3 12a9 9 0 1 0 3-6.7M3 4v5h5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      Refresh
    </button>
  </div>
</div>

{#if loadErr}
  <div class="alert alert-error mt-4 py-2 text-sm">{loadErr}</div>
{/if}

{#if throttled.length > 0}
  <div class="mt-4 rounded-box border border-warning/40 bg-warning/5 p-3">
    <div class="flex items-center gap-2 text-sm font-semibold text-warning">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M12 9v4m0 4h.01M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      Auth-callback throttle : {throttled.length} IP{throttled.length === 1 ? '' : 's'} tracked
    </div>
    <div class="mt-2 overflow-x-auto">
      <table class="table table-sm">
        <thead>
          <tr class="text-base-content/60">
            <th>IP</th>
            <th>Failures</th>
            <th>Status</th>
            <th>Window expires</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each throttled as t (t.ip)}
            <tr>
              <td class="font-mono text-xs">{t.ip}</td>
              <td>{t.failures}</td>
              <td>
                {#if t.locked}
                  <span class="badge badge-sm badge-error">locked</span>
                {:else}
                  <span class="badge badge-sm badge-warning">tracking</span>
                {/if}
              </td>
              <td class="text-xs text-base-content/70">
                {#if t.expires_in_seconds > 0}
                  in {t.expires_in_seconds}s
                {:else}
                  expired
                {/if}
              </td>
              <td>
                <button class="btn btn-xs btn-ghost" onclick={() => unlock(t.ip)}>
                  Unlock
                </button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}

{#if !enabled}
  <div class="mt-4 rounded-box border border-base-300 bg-base-100 p-8 text-center">
    <h3 class="text-lg font-semibold text-base-content/80">Audit log not enabled</h3>
    <p class="mt-2 text-sm text-base-content/60">
      Start weft-webui with <code class="font-mono text-xs bg-base-200 px-1 rounded">--audit-log-path /var/log/weft-webui/audit.jsonl</code>
      (or set <code class="font-mono text-xs bg-base-200 px-1 rounded">WEBUI_AUDIT_LOG_PATH</code>)
      to persist admin actions to disk. This page tails the most recent entries from that file.
    </p>
  </div>
{:else}
  <!-- Filter bar -->
  <div class="mt-4 flex flex-wrap items-center gap-3 rounded-box border border-base-300 bg-base-100 p-3 text-sm">
    <label class="form-control">
      <span class="label-text mb-1 text-xs">Action contains</span>
      <input class="input input-bordered input-sm w-48" placeholder="auth. or az. ..."
        bind:value={filterAction}/>
    </label>
    <label class="form-control">
      <span class="label-text mb-1 text-xs">Result</span>
      <select class="select select-bordered select-sm w-32" bind:value={filterResult}>
        <option value="">any</option>
        <option value="ok">ok</option>
        <option value="error">error</option>
      </select>
    </label>
    <label class="form-control">
      <span class="label-text mb-1 text-xs">Subject contains</span>
      <input class="input input-bordered input-sm w-48" placeholder="alice@... or oidc sub"
        bind:value={filterSubject}/>
    </label>
    <label class="form-control">
      <span class="label-text mb-1 text-xs">Since (UTC)</span>
      <input type="datetime-local" class="input input-bordered input-sm"
        bind:value={filterSince}/>
    </label>
    <label class="form-control">
      <span class="label-text mb-1 text-xs">Until (UTC)</span>
      <input type="datetime-local" class="input input-bordered input-sm"
        bind:value={filterUntil}/>
    </label>
    <label class="form-control">
      <span class="label-text mb-1 text-xs">Tail size</span>
      <select class="select select-bordered select-sm w-32" bind:value={limit}>
        <option value={50}>50</option>
        <option value={200}>200</option>
        <option value={500}>500</option>
        <option value={1000}>1000</option>
      </select>
    </label>
  </div>

  <!-- Events table -->
  <div class="mt-4 overflow-x-auto rounded-box border border-base-300 bg-base-100">
    <table class="table table-sm">
      <thead>
        <tr class="text-base-content/60">
          <th>Time (UTC)</th>
          <th>Action</th>
          <th>Result</th>
          <th>Subject</th>
          <th>Resource</th>
          <th>IP</th>
          <th>Detail</th>
        </tr>
      </thead>
      <tbody>
        {#each events as ev (ev.ts + ev.action + ev.subject)}
          <tr>
            <td class="font-mono text-xs whitespace-nowrap">{ev.ts.replace('T', ' ').replace(/\.\d+Z$/, 'Z')}</td>
            <td>
              <span class="badge badge-sm {actionBadge(ev.action)}">{ev.action}</span>
            </td>
            <td>
              {#if ev.result}
                <span class="badge badge-sm {resultBadge(ev.result)}">{ev.result}</span>
              {/if}
            </td>
            <td class="font-mono text-xs">{ev.subject ?? ''}</td>
            <td class="text-xs">
              {#if ev.resource_kind}
                <span class="opacity-70">{ev.resource_kind}</span>
                {#if ev.resource_id}
                  <span class="font-mono opacity-50"> {ev.resource_id}</span>
                {/if}
              {/if}
            </td>
            <td class="font-mono text-xs">{shortIP(ev.remote_ip ?? '')}</td>
            <td class="text-xs text-base-content/70">
              {#if ev.error}
                <span class="text-error">{ev.error}</span>
              {:else if ev.extra}
                {#each Object.entries(ev.extra) as [k, v] (k)}
                  <span class="mr-2"><span class="opacity-50">{k}=</span>{v}</span>
                {/each}
              {/if}
            </td>
          </tr>
        {/each}
        {#if events.length === 0}
          <tr><td colspan="7" class="text-center italic text-base-content/40 py-6">
            no events match the current filters
          </td></tr>
        {/if}
      </tbody>
    </table>
  </div>
{/if}
