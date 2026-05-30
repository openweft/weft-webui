<script lang="ts">
  import type { ResourceMeta } from '../api';
  import { sectionIcon } from '../icons';

  let { grouped }: { grouped: { section: string; items: ResourceMeta[] }[] } = $props();
</script>

<div>
  <h2 class="text-2xl font-bold">Overview</h2>
  <p class="text-sm text-base-content/60">Every Weft object type at a glance.</p>
</div>

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
