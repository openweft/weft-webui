<script lang="ts">
  import type { ResourceMeta } from '../api';
  import { sectionIcon } from '../icons';

  let { grouped, active }: { grouped: { section: string; items: ResourceMeta[] }[]; active: string } =
    $props();
</script>

<aside class="flex h-full w-64 shrink-0 flex-col border-r border-base-300 bg-base-100">
  <a href="#/" class="flex items-center gap-2 px-5 h-16 border-b border-base-300">
    <span class="inline-block h-3 w-3 rounded-sm bg-gradient-to-br from-cyan-400 to-indigo-500"></span>
    <span class="text-lg font-bold tracking-tight">Weft</span>
    <span class="badge badge-sm badge-ghost ml-auto">dashboard</span>
  </a>

  <nav class="flex-1 overflow-y-auto px-2 py-3">
    <ul class="menu menu-sm w-full gap-0.5">
      <li>
        <a href="#/" class:menu-active={active === ''} class="font-medium">
          <svg viewBox="0 0 24 24" class="h-4 w-4">{@html sectionIcon('Overview')}</svg>
          Overview
        </a>
      </li>
    </ul>

    {#each grouped as group (group.section)}
      <ul class="menu menu-sm w-full gap-0.5">
        <li class="menu-title flex flex-row items-center gap-2 pt-3">
          <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70">{@html sectionIcon(group.section)}</svg>
          {group.section}
        </li>
        {#each group.items as r (r.id)}
          <li>
            <a href={`#/${r.id}`} class:menu-active={active === r.id}>
              <span class="truncate">{r.label}</span>
              <span class="badge badge-xs badge-ghost ml-auto">{r.count}</span>
            </a>
          </li>
        {/each}
      </ul>
    {/each}
  </nav>

  <div class="border-t border-base-300 px-4 py-3 text-xs text-base-content/50">
    early development · mock data
  </div>
</aside>
