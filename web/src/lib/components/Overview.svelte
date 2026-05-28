<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { getQuotas, getSummary, type ResourceMeta, type Quota } from '../api';
  import { lastEvents, eventToResource } from '../events';
  import { sectionIcon, quotaIcon } from '../icons';

  let { grouped }: { grouped: { section: string; items: ResourceMeta[] }[] } = $props();

  let quotas = $state<Quota[]>([]);
  // Live counts pulled from /api/summary ; overrides the static
  // `count` carried on `grouped.items` (which is fixed at init).
  let counts = $state<Record<string, number>>({});
  // Per-resource "just changed" flag → drives a 1s flash on the
  // count cell so the operator notices a delta.
  let flashing = $state<Record<string, boolean>>({});
  // Previous count map kept so we can detect deltas across refreshes.
  let prevCounts: Record<string, number> = {};

  function flashCount(id: string) {
    flashing = { ...flashing, [id]: true };
    setTimeout(() => { flashing = { ...flashing, [id]: false }; }, 1000);
  }

  async function refresh() {
    try {
      const [summary, q] = await Promise.all([getSummary(), getQuotas().catch(() => [])]);
      const next: Record<string, number> = {};
      for (const s of summary) next[s.id] = s.count;
      // Flag every id whose count changed since the last refresh.
      for (const id of Object.keys(next)) {
        if (prevCounts[id] !== undefined && prevCounts[id] !== next[id]) {
          flashCount(id);
        }
      }
      prevCounts = next;
      counts = next;
      quotas = q;
    } catch { /* surface elsewhere */ }
  }
  onMount(refresh);

  // Auto-refresh on relevant live events (any event that maps to a
  // tracked resource). Debounced so a burst collapses into one fetch.
  let refreshTimer: ReturnType<typeof setTimeout> | null = null;
  let lastSeen = 0;
  let unsubscribe: () => void;
  function scheduleRefresh() {
    if (refreshTimer) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => { refreshTimer = null; refresh(); }, 500);
  }
  onMount(() => {
    unsubscribe = lastEvents.subscribe((all) => {
      const newCount = all.length - lastSeen;
      lastSeen = all.length;
      for (let i = 0; i < newCount; i++) {
        if (eventToResource(all[i].kind)) {
          scheduleRefresh();
          return;
        }
      }
    });
  });
  onDestroy(() => {
    unsubscribe?.();
    if (refreshTimer) clearTimeout(refreshTimer);
  });

  const pct = (q: Quota) => (q.limit > 0 ? Math.round((q.used / q.limit) * 100) : 0);
  // Consumption bands : green < 60 ≤ yellow < 75 ≤ orange < 90 ≤ red.
  function barColor(p: number): string {
    if (p >= 90) return '#ef4444';
    if (p >= 75) return '#f97316';
    if (p >= 60) return '#eab308';
    return '#22c55e';
  }
</script>

<div>
  <h2 class="text-2xl font-bold">Overview</h2>
  <p class="text-sm text-base-content/60">Quota consumption and every Weft object type at a glance.</p>
</div>

{#if quotas.length}
  <section class="mt-6">
    <h3 class="mb-2 text-sm font-semibold uppercase tracking-wide text-base-content/60">Quotas</h3>
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
      {#each quotas as q (q.id)}
        {@const p = pct(q)}
        {@const c = barColor(p)}
        <div class="rounded-box border border-base-300 bg-base-100 p-4">
          <div class="flex items-center gap-2">
            <svg viewBox="0 0 24 24" class="h-4 w-4 text-base-content/70">{@html quotaIcon(q.icon)}</svg>
            <span class="text-sm font-medium">{q.label}</span>
            <span class="ml-auto text-xs tabular-nums text-base-content/60">
              {q.used} / {q.limit}{q.unit ? ' ' + q.unit : ''}
            </span>
          </div>
          <div class="mt-2 h-2 w-full overflow-hidden rounded-full bg-base-300">
            <div class="h-full rounded-full transition-[width]" style={`width:${Math.min(p, 100)}%;background:${c}`}></div>
          </div>
          <div class="mt-1 text-right text-[11px] font-medium tabular-nums" style={`color:${c}`}>{p}%</div>
        </div>
      {/each}
    </div>
  </section>
{/if}

{#each grouped as group (group.section)}
  <section class="mt-6">
    <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold uppercase tracking-wide text-base-content/60">
      <svg viewBox="0 0 24 24" class="h-4 w-4">{@html sectionIcon(group.section)}</svg>
      {group.section}
    </h3>
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
      {#each group.items as r (r.id)}
        {@const c = counts[r.id] ?? r.count}
        <a
          href={`#/${r.id}`}
          class="rounded-box border border-base-300 bg-base-100 p-4 transition hover:border-primary hover:shadow"
        >
          <div class="text-3xl font-bold tabular-nums transition-colors duration-700"
            class:text-primary={flashing[r.id]}>{c}</div>
          <div class="mt-1 text-sm text-base-content/70">{r.label}</div>
        </a>
      {/each}
    </div>
  </section>
{/each}
