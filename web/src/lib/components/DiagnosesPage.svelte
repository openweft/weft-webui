<script lang="ts">
  // DiagnosesPage — cluster-wide diagnoses fed by weft-doctor.
  //
  // Initial render fetches /api/diagnoses (admin scope, Infra portal
  // only). On mount we also open an EventSource against
  // /api/diagnoses/stream and merge incoming `diagnosis` events into
  // the local state, deduped by `pattern_hash` (latest wins). The
  // server emits a stream of named SSE events ; we listen to the
  // `diagnosis` event specifically so unrelated keepalives /
  // heartbeats don't trip JSON.parse.
  //
  // Cards are sorted by severity (critical → high → medium → low)
  // then by occurrences desc — the noisier and more severe a pattern,
  // the higher it floats. Empty cache surfaces the "all quiet" state
  // rather than a blank pane.

  import { onMount, onDestroy } from 'svelte';
  import { listDiagnoses, type Diagnosis } from '../api';
  import { withBase } from '../endpoints';

  let items = $state<Diagnosis[]>([]);
  let loaded = $state(false);
  let loadErr = $state('');
  let streamState = $state<'idle' | 'open' | 'error'>('idle');
  let lastUpdate = $state<string>('');
  let expanded = $state<Record<string, boolean>>({});

  let es: EventSource | null = null;

  async function refresh() {
    try {
      items = await listDiagnoses();
      loadErr = '';
      lastUpdate = new Date().toLocaleTimeString();
    } catch (e) {
      loadErr = String(e);
    } finally {
      loaded = true;
    }
  }

  // mergeOne replaces the matching pattern_hash entry (or appends if
  // new). Latest-wins because the server's broadcast already carries
  // the freshly-updated counters + last_seen.
  function mergeOne(d: Diagnosis) {
    const i = items.findIndex((x) => x.pattern_hash === d.pattern_hash);
    if (i < 0) {
      items = [...items, d];
    } else {
      const next = items.slice();
      next[i] = d;
      items = next;
    }
    lastUpdate = new Date().toLocaleTimeString();
  }

  function openStream() {
    if (es) return;
    es = new EventSource(withBase('/api/diagnoses/stream'));
    es.onopen = () => { streamState = 'open'; };
    es.onerror = () => { streamState = 'error'; };
    // The Go side names its frames `event: diagnosis` ; the default
    // onmessage handler only fires for unnamed events, so we attach
    // an explicit named listener.
    es.addEventListener('diagnosis', (ev) => {
      try {
        const raw = JSON.parse((ev as MessageEvent).data) as Diagnosis;
        const d: Diagnosis = {
          ...raw,
          examples: raw.examples ?? [],
        };
        if (d.pattern_hash) mergeOne(d);
      } catch { /* ignore malformed frame */ }
    });
  }

  function closeStream() {
    if (!es) return;
    es.close();
    es = null;
    streamState = 'idle';
  }

  onMount(() => {
    void refresh();
    openStream();
  });
  onDestroy(closeStream);

  // ---- sort + presentation helpers --------------------------------

  const SEVERITY_RANK: Record<string, number> = {
    critical: 0,
    high: 1,
    medium: 2,
    low: 3,
  };

  let sorted = $derived(
    [...items].sort((a, b) => {
      const ra = SEVERITY_RANK[a.severity] ?? 99;
      const rb = SEVERITY_RANK[b.severity] ?? 99;
      if (ra !== rb) return ra - rb;
      return (b.occurrences ?? 0) - (a.occurrences ?? 0);
    }),
  );

  function severityGlyph(s: string): string {
    switch (s) {
      case 'critical': return '\u{1F534}'; // red circle
      case 'high':     return '\u{1F7E0}'; // orange circle
      case 'medium':   return '\u{1F7E1}'; // yellow circle
      case 'low':      return '\u{1F7E2}'; // green circle
      default:         return '⚪';    // white circle
    }
  }

  function severityBadge(s: string): string {
    switch (s) {
      case 'critical': return 'badge-error';
      case 'high':     return 'badge-warning';
      case 'medium':   return 'badge-info';
      case 'low':      return 'badge-success';
      default:         return 'badge-ghost';
    }
  }

  function streamBadge(s: typeof streamState): { cls: string; label: string } {
    if (s === 'open')  return { cls: 'badge-success', label: 'streaming' };
    if (s === 'error') return { cls: 'badge-error',   label: 'stream error' };
    return { cls: 'badge-ghost', label: 'idle' };
  }

  function fmtTs(ts?: string): string {
    if (!ts) return '—';
    return ts.replace('T', ' ').replace(/Z$/, ' UTC');
  }

  function shortHash(h: string): string {
    return h.length > 12 ? h.slice(0, 12) : h;
  }

  function toggle(hash: string) {
    expanded = { ...expanded, [hash]: !expanded[hash] };
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Cluster Diagnosis Dashboard</h2>
    <p class="text-sm text-base-content/60">
      Auto-maintained by <span class="font-mono">weft-doctor</span>
      {#if lastUpdate}
        · last update: <span class="text-xs text-base-content/40">{lastUpdate}</span>
      {/if}
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <span class="badge {streamBadge(streamState).cls} badge-sm">
      {streamBadge(streamState).label}
    </span>
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

{#if !loaded}
  <div class="mt-12 flex justify-center"><span class="loading loading-spinner loading-lg"></span></div>
{:else if sorted.length === 0}
  <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-10 text-center">
    <div class="text-3xl">{'\u{1F7E2}'}</div>
    <h3 class="mt-2 text-lg font-semibold text-base-content/80">All quiet — no active diagnoses</h3>
    <p class="mt-2 text-sm text-base-content/60">
      weft-doctor hasn't flagged any pattern matching its rule set.
      New events will appear here in real time.
    </p>
  </div>
{:else}
  <ul class="mt-4 flex flex-col gap-3">
    {#each sorted as d (d.pattern_hash)}
      <li class="rounded-box border border-base-300 bg-base-100 p-4">
        <div class="flex flex-wrap items-start gap-3">
          <div class="text-2xl leading-none">{severityGlyph(d.severity)}</div>
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="badge badge-sm {severityBadge(d.severity)} uppercase">{d.severity}</span>
              <h3 class="text-base font-semibold">{d.title}</h3>
              <span class="badge badge-sm badge-ghost ml-auto">
                {d.occurrences} occurrence{d.occurrences === 1 ? '' : 's'}
              </span>
            </div>

            {#if d.root_cause}
              <p class="mt-2 text-sm">
                <span class="font-semibold text-base-content/80">Root cause: </span>
                <span class="text-base-content/80">{d.root_cause}</span>
              </p>
            {/if}

            {#if d.suggested_action}
              <p class="mt-1 text-sm">
                <span class="font-semibold text-base-content/80">Suggested action: </span>
                <span class="text-base-content/80">{d.suggested_action}</span>
              </p>
            {/if}

            <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-base-content/60">
              {#if d.file_location}
                <span>
                  <span class="opacity-50">location:</span>
                  <span class="font-mono">{d.file_location}</span>
                </span>
              {/if}
              <span>
                <span class="opacity-50">hash:</span>
                <span class="font-mono" title={d.pattern_hash}>{shortHash(d.pattern_hash)}</span>
              </span>
              <span>
                <span class="opacity-50">first:</span>
                <span class="font-mono">{fmtTs(d.first_seen)}</span>
              </span>
              <span>
                <span class="opacity-50">last:</span>
                <span class="font-mono">{fmtTs(d.last_seen)}</span>
              </span>
            </div>

            {#if d.examples.length > 0}
              <div class="mt-3">
                <button
                  type="button"
                  class="btn btn-ghost btn-xs gap-1"
                  onclick={() => toggle(d.pattern_hash)}
                  aria-expanded={!!expanded[d.pattern_hash]}
                >
                  <svg viewBox="0 0 24 24" class="h-3 w-3 transition-transform"
                    class:rotate-90={!!expanded[d.pattern_hash]}
                    fill="none" stroke="currentColor" stroke-width="2.5">
                    <path d="m9 6 6 6-6 6" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  {expanded[d.pattern_hash] ? 'Hide' : 'Show'} examples ({d.examples.length})
                </button>

                {#if expanded[d.pattern_hash]}
                  <ul class="mt-2 flex flex-col gap-2">
                    {#each d.examples as ex, i (i)}
                      <li class="rounded border border-base-200 bg-base-200/40 p-2 font-mono text-xs">
                        <div class="flex flex-wrap gap-x-3 gap-y-1 text-base-content/60">
                          {#if ex.time}<span>{fmtTs(ex.time)}</span>{/if}
                          {#if ex.level}<span class="uppercase">{ex.level}</span>{/if}
                          {#if ex.source}<span class="opacity-60">{ex.source}</span>{/if}
                        </div>
                        {#if ex.msg}
                          <div class="mt-1 break-words text-base-content/80">{ex.msg}</div>
                        {/if}
                        {#if ex.attrs && Object.keys(ex.attrs).length > 0}
                          <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-base-content/60">
                            {#each Object.entries(ex.attrs) as [k, v] (k)}
                              <span>
                                <span class="opacity-50">{k}=</span>{String(v)}
                              </span>
                            {/each}
                          </div>
                        {/if}
                      </li>
                    {/each}
                  </ul>
                {/if}
              </div>
            {/if}
          </div>
        </div>
      </li>
    {/each}
  </ul>
{/if}
