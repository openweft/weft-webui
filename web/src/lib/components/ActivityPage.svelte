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
  // Mutually exclusive quick-filter chips ; null = no quick filter.
  let chip = $state<'' | 'errors' | 'mutations' | 'state'>('');
  // Filter by the OIDC `sub` that triggered the event. weft-agent
  // carries this in meta.actor on every mutation-derived event ; the
  // mock heartbeat synthesises one too so the dropdown isn't empty in
  // dev. Empty = all actors.
  let actorFilter = $state('');

  // Map each chip to a predicate. Errors = anything that contains
  // `.error` or `.failed` or has an "error" meta key. Mutations =
  // events whose verb conveys a write (create, delete, attach,
  // detach, push, change). State = lifecycle transitions
  // (vm.state.*, scheduling-rule.compliant/drifting/unschedulable).
  function chipMatch(e: PlatformEvent, c: typeof chip): boolean {
    if (c === '') return true;
    if (c === 'errors') {
      return e.kind.includes('.error') || e.kind.includes('.failed')
        || !!(e.meta && e.meta.error);
    }
    if (c === 'mutations') {
      return /\.(created|deleted|attached|detached|pushed|changed|updated|started|stopped|allocated|released|mapped|unmapped|upserted)\b/.test(e.kind);
    }
    if (c === 'state') {
      return e.kind.startsWith('vm.state.') || e.kind.startsWith('scheduling-rule.');
    }
    return true;
  }

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
      if (!chipMatch(e, chip)) return false;
      if (kindQ && !e.kind.includes(kindQ.toLowerCase())) return false;
      if (subjectQ && !(e.subject ?? '').toLowerCase().includes(subjectQ.toLowerCase())) return false;
      if (resourceFilter && eventToResource(e.kind) !== resourceFilter) return false;
      if (actorFilter && (e.meta?.actor ?? '') !== actorFilter) return false;
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

  // Same idea for the actor dropdown : populated from observed events
  // so the operator never picks a name with zero hits.
  let actors = $derived.by<string[]>(() => {
    const s = new Set<string>();
    for (const e of all) {
      const a = e.meta?.actor;
      if (typeof a === 'string' && a) s.add(a);
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
    <select class="select select-sm select-bordered" bind:value={actorFilter} title="Filter by actor (OIDC sub)">
      <option value="">all actors</option>
      {#each actors as a (a)}<option value={a}>{a}</option>{/each}
    </select>
    <button class="btn btn-sm btn-ghost" onclick={clearEventFeed} disabled={all.length === 0}>
      Clear
    </button>
  </div>
</div>

<!-- Quick-filter chip row : mutually exclusive ; click again to clear. -->
<div class="mt-3 flex items-center gap-2 text-xs">
  <span class="text-base-content/50">Quick:</span>
  {#each [
    { id: 'state',     label: 'Lifecycle' },
    { id: 'mutations', label: 'Mutations' },
    { id: 'errors',    label: 'Errors' },
  ] as c (c.id)}
    <button
      class="rounded-box border px-2 py-0.5 hover:bg-base-200"
      class:border-primary={chip === c.id}
      class:bg-primary={chip === c.id}
      class:text-primary-content={chip === c.id}
      class:border-base-300={chip !== c.id}
      onclick={() => (chip = chip === c.id ? '' : c.id as typeof chip)}
    >{c.label}</button>
  {/each}
  {#if chip}
    <button class="text-base-content/60 hover:text-base-content" onclick={() => (chip = '')}>clear</button>
  {/if}
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
