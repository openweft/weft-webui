<script lang="ts">
  import type { ResourceMeta } from '../api';
  import { sectionIcon } from '../icons';

  let { grouped, active }: { grouped: { section: string; items: ResourceMeta[] }[]; active: string } =
    $props();

  // Per-section collapse state. Persisted in localStorage so the
  // operator's choice survives navigation and reloads. The key is the
  // section name ; missing-key = expanded (default).
  let collapsed = $state<Record<string, boolean>>(loadCollapse());

  function loadCollapse(): Record<string, boolean> {
    try {
      const raw = localStorage.getItem('weft-sidebar-collapse');
      return raw ? (JSON.parse(raw) as Record<string, boolean>) : {};
    } catch { return {}; }
  }
  function persist() {
    localStorage.setItem('weft-sidebar-collapse', JSON.stringify(collapsed));
  }

  function toggle(section: string) {
    collapsed = { ...collapsed, [section]: !collapsed[section] };
    persist();
  }

  // If the active resource is in a collapsed section, auto-expand it
  // so the operator's selection isn't hidden after a hash-link reload.
  $effect(() => {
    for (const g of grouped) {
      if (g.items.some((r) => r.id === active) && collapsed[g.section]) {
        collapsed = { ...collapsed, [g.section]: false };
        persist();
      }
    }
  });
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
        <li class="pt-3">
          <button
            type="button"
            class="flex w-full flex-row items-center gap-2 text-xs font-semibold uppercase tracking-wide text-base-content/60 hover:text-base-content"
            onclick={() => toggle(group.section)}
            aria-expanded={!collapsed[group.section]}
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70">{@html sectionIcon(group.section)}</svg>
            <span class="grow text-left">{group.section}</span>
            <svg viewBox="0 0 24 24" class="h-3 w-3 opacity-60 transition-transform"
              class:rotate-[-90deg]={collapsed[group.section]}
              fill="none" stroke="currentColor" stroke-width="2.5">
              <path d="m6 9 6 6 6-6" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
        </li>
        {#if !collapsed[group.section]}
          {#each group.items as r (r.id)}
            <li>
              <a href={`#/${r.id}`} class:menu-active={active === r.id}>
                <span class="truncate">{r.label}</span>
                <span class="badge badge-xs badge-ghost ml-auto">{r.count}</span>
              </a>
            </li>
          {/each}
        {/if}
      </ul>
    {/each}
  </nav>

  <div class="border-t border-base-300 px-4 py-3 text-xs text-base-content/50">
    early development · mock data
  </div>
</aside>
