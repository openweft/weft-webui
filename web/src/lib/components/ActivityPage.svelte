<script lang="ts">
  // Activity page : a scrollable chronology of every platform event
  // the SPA has seen since the tab opened (capped at 500). The toast
  // bar still surfaces the latest ones in the corner ; this page is
  // the place to dig in.
  //
  // Filters :
  //   kind        — prefix or substring match (e.g. "vm." or "error")
  //   subject     — substring match
  //   resource    — collapse the kind to its mapped resource id
  //                (eventToResource)
  import { onMount, onDestroy } from 'svelte';
  import { eventFeed, clearEventFeed, eventToResource, eventsConnection, type PlatformEvent } from '../events';

  let kindQ = $state('');
  let subjectQ = $state('');
  let resourceFilter = $state('');

  let all = $state<PlatformEvent[]>([]);
  let connection = $state<'idle' | 'open' | 'error'>('idle');
  let unsubFeed: () => void;
  let unsubConn: () => void;
  onMount(() => {
    unsubFeed = eventFeed.subscribe((xs) => (all = xs));
    unsubConn = eventsConnection.subscribe((c) => (connection = c));
  });
  onDestroy(() => { unsubFeed?.(); unsubConn?.(); });

  let filtered = $derived.by<PlatformEvent[]>(() => {
    return all.filter((e) => {
      if (kindQ && !e.kind.includes(kindQ.toLowerCase())) return false;
      if (subjectQ && !(e.subject ?? '').toLowerCase().includes(subjectQ.toLowerCase())) return false;
      if (resourceFilter && eventToResource(e.kind) !== resourceFilter) return false;
      return true;
    });
  });

  // The set of distinct resource ids in the buffer drives the filter
  // dropdown so the operator only sees options that match real data.
  let resources = $derived.by<string[]>(() => {
    const s = new Set<string>();
    for (const e of all) {
      const r = eventToResource(e.kind);
      if (r) s.add(r);
    }
    return [...s].sort();
  });

  function rowColor(kind: string): string {
    if (kind.startsWith('vm.state.')) return 'border-success';
    if (kind.startsWith('lb.') || kind.startsWith('dns.')) return 'border-info';
    if (kind.startsWith('scheduling-rule.')) return 'border-primary';
    if (kind.startsWith('security-group.') || kind.startsWith('floating-ip.')) return 'border-warning';
    if (kind.includes('.error') || kind.includes('.failed')) return 'border-error';
    return 'border-base-300';
  }
  function shortTs(iso: string): string {
    // "2026-05-28T19:33:26.414915Z" → "19:33:26.414"
    const t = iso.split('T')[1] || iso;
    return t.slice(0, 12);
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Activity</h2>
    <p class="text-sm text-base-content/60">
      Every platform event since this tab opened
      ({filtered.length} / {all.length})
      <span class="ml-2 inline-flex items-center gap-1 text-xs">
        <span class="inline-block h-1.5 w-1.5 rounded-full"
          class:bg-success={connection === 'open'}
          class:bg-warning={connection === 'idle'}
          class:bg-error={connection === 'error'}></span>
        {connection}
      </span>
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <input class="input input-sm input-bordered w-40 font-mono"
      placeholder="kind filter" bind:value={kindQ} />
    <input class="input input-sm input-bordered w-40 font-mono"
      placeholder="subject filter" bind:value={subjectQ} />
    <select class="select select-sm select-bordered" bind:value={resourceFilter}>
      <option value="">all resources</option>
      {#each resources as r (r)}<option value={r}>{r}</option>{/each}
    </select>
    <button class="btn btn-sm btn-ghost" onclick={clearEventFeed} disabled={all.length === 0}>
      Clear
    </button>
  </div>
</div>

{#if all.length === 0}
  <div class="mt-10 rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
    No events yet. Mutations from the dashboard, the CLI, or
    weft-network reconcilers will land here as they happen.
  </div>
{:else if filtered.length === 0}
  <div class="mt-6 alert">No events match the current filters.</div>
{:else}
  <ol class="mt-4 space-y-1">
    {#each filtered as e (e.ts + e.kind + e.subject)}
      <li class="flex items-baseline gap-3 rounded-box border-l-4 bg-base-100 px-3 py-1.5 text-xs {rowColor(e.kind)}">
        <span class="shrink-0 font-mono text-base-content/50 tabular-nums">{shortTs(e.ts)}</span>
        <span class="shrink-0 font-mono">{e.kind}</span>
        {#if eventToResource(e.kind)}
          <a class="badge badge-xs badge-ghost" href={`#/${eventToResource(e.kind)}`}>
            {eventToResource(e.kind)}
          </a>
        {/if}
        <span class="truncate font-medium">{e.subject || '—'}</span>
        {#if e.project}
          <span class="text-base-content/50">· {e.project}</span>
        {/if}
        {#if e.meta && Object.keys(e.meta).length > 0}
          <span class="ml-auto truncate text-base-content/50">
            {Object.entries(e.meta).map(([k, v]) => `${k}=${v}`).join(' ')}
          </span>
        {/if}
      </li>
    {/each}
  </ol>
{/if}
