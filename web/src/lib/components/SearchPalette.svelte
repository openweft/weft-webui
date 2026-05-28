<script lang="ts">
  // Command palette : Cmd-K (⌘K on macOS, Ctrl-K elsewhere) opens a
  // modal that searches across every resource the operator can see.
  // Arrow keys + Enter navigate ; Escape closes.
  //
  // Loading strategy : the first open triggers parallel fetches of
  // every resource id in the sidebar. Subsequent opens reuse the
  // cache, refreshed lazily ; events on the live stream invalidate
  // a single resource's cache so subsequent searches see fresh data.
  import { onMount, onDestroy } from 'svelte';
  import { getResources, getRows, type ResourceMeta, type Row } from '../api';
  import { lastEvents, eventToResource } from '../events';
  import { go } from '../router';

  let open = $state(false);
  let query = $state('');
  let cursor = $state(0);   // highlighted result index
  let inputEl = $state<HTMLInputElement | undefined>();

  // Catalogue of available resources (mirror of what the sidebar
  // already loaded ; calling getResources again is cheap).
  let metas = $state<ResourceMeta[]>([]);
  // resource id → rows. Filled lazily on first open.
  let cache = $state<Record<string, Row[]>>({});
  let loading = $state(false);

  // ---- result building -----------------------------------------------
  //
  // Each row is fingerprinted across every string-ish field once, so
  // a fuzzy substring match is O(rows × fields × query.length). Below
  // a few thousand rows this is imperceptible ; if it ever becomes
  // a problem we'd swap for a proper index (lunr, minisearch).
  interface Result {
    score: number;
    resourceId: string;
    resourceLabel: string;
    row: Row;
    label: string;     // primary display (usually .name or .address)
    sub: string;       // secondary line (project / IP / status)
  }

  function primaryLabel(r: Row): string {
    return String(r.name ?? r.address ?? r.email ?? r.uuid ?? '?');
  }
  function secondaryLine(r: Row): string {
    const bits: string[] = [];
    if (r.project) bits.push(`project ${r.project}`);
    if (r.ip) bits.push(String(r.ip));
    if (r.status) bits.push(String(r.status));
    if (r.cidr) bits.push(String(r.cidr));
    return bits.join(' · ');
  }
  function scoreRow(r: Row, q: string): number {
    if (!q) return 0;
    // Exact prefix on the primary label is best, then any substring
    // anywhere in the row. The "anywhere" search joins every value
    // once per row for a single .includes() check.
    const label = primaryLabel(r).toLowerCase();
    if (label === q) return 100;
    if (label.startsWith(q)) return 80;
    if (label.includes(q)) return 60;
    const blob = Object.values(r).map(String).join(' ').toLowerCase();
    if (blob.includes(q)) return 30;
    return 0;
  }

  let results = $derived.by<Result[]>(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    const out: Result[] = [];
    for (const meta of metas) {
      const rows = cache[meta.id];
      if (!rows) continue;
      for (const r of rows) {
        const score = scoreRow(r, q);
        if (score > 0) {
          out.push({
            score,
            resourceId: meta.id,
            resourceLabel: meta.label,
            row: r,
            label: primaryLabel(r),
            sub: secondaryLine(r),
          });
        }
      }
    }
    out.sort((a, b) => b.score - a.score || a.label.localeCompare(b.label));
    return out.slice(0, 30);
  });

  $effect(() => { cursor = 0; query; }); // reset cursor on query change

  // ---- loading -------------------------------------------------------

  async function loadAll() {
    if (metas.length === 0) {
      try { metas = await getResources(); } catch { metas = []; }
    }
    loading = true;
    await Promise.allSettled(metas.map(async (m) => {
      if (cache[m.id]) return;
      try { cache = { ...cache, [m.id]: await getRows(m.id) }; }
      catch { cache = { ...cache, [m.id]: [] }; }
    }));
    loading = false;
  }

  // Invalidate a resource's cache when a live event hits it ; the
  // next search query will surface the fresh data on demand.
  let lastSeen = 0;
  let unsubEvents: () => void;
  onMount(() => {
    unsubEvents = lastEvents.subscribe((all) => {
      const newCount = all.length - lastSeen;
      lastSeen = all.length;
      for (let i = 0; i < newCount; i++) {
        const id = eventToResource(all[i].kind);
        if (id && cache[id]) {
          // Drop the cache entry ; we'll refetch on the next open or
          // search. Cheap : we keep the meta list around.
          const { [id]: _, ...rest } = cache;
          void _;
          cache = rest;
        }
      }
    });
  });

  // ---- keyboard / lifecycle -----------------------------------------

  function onKey(e: KeyboardEvent) {
    // Toggle. Use metaKey on macOS, ctrlKey elsewhere — both fire fine
    // on the same handler.
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      open = !open;
      if (open) {
        loadAll();
        queueMicrotask(() => inputEl?.focus());
      }
      return;
    }
    if (!open) return;
    if (e.key === 'Escape') { e.preventDefault(); open = false; return; }
    if (e.key === 'ArrowDown') { e.preventDefault(); cursor = Math.min(cursor + 1, results.length - 1); return; }
    if (e.key === 'ArrowUp')   { e.preventDefault(); cursor = Math.max(cursor - 1, 0); return; }
    if (e.key === 'Enter')     { e.preventDefault(); pick(results[cursor]); return; }
  }
  onMount(() => window.addEventListener('keydown', onKey));
  onDestroy(() => { window.removeEventListener('keydown', onKey); unsubEvents?.(); });

  // Resources that own a detail drawer ; for these the palette deep-
  // links straight into the drawer via ?detail=<row-key>. Everything
  // else lands on the resource table where the operator can drill.
  const DRAWER_RESOURCES = new Set(['microvms', 'security-groups', 'loadbalancers']);

  function pick(r: Result | undefined) {
    if (!r) return;
    if (DRAWER_RESOURCES.has(r.resourceId)) {
      // Prefer uuid (stable) ; fall back to name when the row lacks one
      // (mock rows often do). ResourcePage matches on either.
      const key = String(r.row.uuid ?? r.row.name ?? '');
      go(r.resourceId, key ? { detail: key } : undefined);
    } else {
      go(r.resourceId);
    }
    open = false;
    query = '';
  }
</script>

{#if open}
  <!-- Backdrop -->
  <button class="fixed inset-0 z-[60] bg-base-300/60 backdrop-blur-sm"
    aria-label="Close palette" onclick={() => (open = false)}></button>

  <div class="fixed left-1/2 top-24 z-[70] w-full max-w-xl -translate-x-1/2 rounded-box bg-base-100 shadow-2xl">
    <div class="flex items-center gap-2 border-b border-base-300 px-3 py-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input
        bind:this={inputEl}
        bind:value={query}
        class="grow border-0 bg-transparent text-sm outline-none focus:outline-none"
        placeholder="Search VMs, volumes, networks, projects…"
        autocomplete="off"
      />
      {#if loading}
        <span class="loading loading-spinner loading-xs opacity-60"></span>
      {/if}
      <kbd class="kbd kbd-xs">esc</kbd>
    </div>

    <div class="max-h-96 overflow-y-auto p-1">
      {#if !query.trim()}
        <p class="px-3 py-6 text-center text-xs text-base-content/50">
          Type to search across every resource in your scope.
          <br />
          <span class="text-base-content/40">⌘K / Ctrl-K to toggle · ↑↓ to navigate · ↵ to open</span>
        </p>
      {:else if results.length === 0}
        <p class="px-3 py-6 text-center text-sm text-base-content/50">
          No match{loading ? ' yet…' : '.'}
        </p>
      {:else}
        <ul class="space-y-0.5">
          {#each results as r, i (r.resourceId + ':' + r.label + ':' + i)}
            <li>
              <button
                class="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-base-200"
                class:bg-base-200={i === cursor}
                onmouseenter={() => (cursor = i)}
                onclick={() => pick(r)}
              >
                <span class="badge badge-xs badge-ghost shrink-0">{r.resourceLabel}</span>
                <span class="grow truncate font-medium">{r.label}</span>
                {#if r.sub}
                  <span class="shrink-0 truncate text-xs text-base-content/50">{r.sub}</span>
                {/if}
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    <div class="border-t border-base-300 px-3 py-1.5 text-[10px] text-base-content/40">
      ↑↓ navigate · ↵ open · ⌘K close
    </div>
  </div>
{/if}
