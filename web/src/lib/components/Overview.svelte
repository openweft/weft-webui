<script lang="ts">
  import { getQuotas, type ResourceMeta, type Quota } from '../api';
  import { sectionIcon, quotaIcon } from '../icons';

  let { grouped }: { grouped: { section: string; items: ResourceMeta[] }[] } = $props();

  let quotas = $state<Quota[]>([]);
  $effect(() => {
    getQuotas()
      .then((q) => (quotas = q))
      .catch(() => (quotas = []));
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
        <a
          href={`#/${r.id}`}
          class="rounded-box border border-base-300 bg-base-100 p-4 transition hover:border-primary hover:shadow"
        >
          <div class="text-3xl font-bold tabular-nums">{r.count}</div>
          <div class="mt-1 text-sm text-base-content/70">{r.label}</div>
        </a>
      {/each}
    </div>
  </section>
{/each}
