<script lang="ts">
  import { type Column, type Row } from '../api';

  let {
    columns,
    rows,
    selectedKey = '',
    onSelect,
    onReload,
  }: {
    columns: Column[];
    rows: Row[];
    // Optional : the key (uuid or name) of the row to highlight as
    // selected. Pair with onSelect to drive header-level Edit /
    // Delete buttons from a sibling component.
    selectedKey?: string;
    // Called when a row is clicked. Convention : the parent uses
    // this to toggle a selection highlight only — the drawer opens
    // exclusively via the header Edit button.
    onSelect?: (row: Row) => void;
    // Called when the operator clicks the bottom-bar reload button.
    // Parent re-fetches its data ; the table itself doesn't own the
    // source. Reload hidden when not supplied.
    onReload?: () => void;
  } = $props();

  function statusClass(v: unknown): string {
    switch (String(v).toLowerCase()) {
      case 'active':
      case 'running':
      case 'up':
      case 'in-use':
        return 'badge-success';
      case 'available':
        return 'badge-info';
      case 'draining':
      case 'stopped':
      case 'disabled':
        return 'badge-warning';
      case 'error':
      case 'failed':
        return 'badge-error';
      default:
        return 'badge-ghost';
    }
  }

  const isStatus = (key: string) => key === 'status';
  const isBool = (v: unknown) => typeof v === 'boolean';
  const isEmpty = (v: unknown) => v === '' || v === null || v === undefined;

  // DNS records carry a `source` column distinguishing operator-edited
  // records (`static`) from records reconciled by weft-network from
  // the VM / LB tables (`auto`). Render it as a coloured chip so the
  // operator can tell at a glance which rows they shouldn't bother
  // editing.
  const isSource = (key: string) => key === 'source';
  function sourceClass(v: unknown): string {
    switch (String(v).toLowerCase()) {
      case 'static': return 'badge-ghost';
      case 'auto':   return 'badge-info';
      default:       return 'badge-ghost';
    }
  }

  // ---- click-to-sort ----
  let sortKey = $state('');
  let sortDir = $state<'asc' | 'desc'>('asc');

  function toggleSort(key: string) {
    if (sortKey === key) sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    else {
      sortKey = key;
      sortDir = 'asc';
    }
  }

  function cmp(a: unknown, b: unknown): number {
    const ae = isEmpty(a);
    const be = isEmpty(b);
    if (ae || be) return ae === be ? 0 : ae ? 1 : -1;
    if (typeof a === 'number' && typeof b === 'number') return a - b;
    if (typeof a === 'boolean' && typeof b === 'boolean') return (a ? 1 : 0) - (b ? 1 : 0);
    return String(a).localeCompare(String(b), undefined, { numeric: true });
  }

  let sorted = $derived.by(() => {
    if (!sortKey) return rows;
    const dir = sortDir === 'asc' ? 1 : -1;
    return [...rows].sort((x, y) => cmp(x[sortKey], y[sortKey]) * dir);
  });

  // ---- pagination ----
  //
  // Client-side : pageSize ∈ {10,25,50,100,all}. Default 25. Reset
  // to page 1 whenever the visible set shrinks (new filter, new
  // resource) so we never end up beyond the last page.
  const PAGE_SIZES = [10, 25, 50, 100];
  let pageSize = $state<number>(
    Number(localStorage.getItem('weft-table-page-size')) || 25,
  );
  let page = $state(1);
  $effect(() => { localStorage.setItem('weft-table-page-size', String(pageSize)); });

  let totalPages = $derived(pageSize === 0 ? 1 : Math.max(1, Math.ceil(sorted.length / pageSize)));
  // Clamp current page on data shrink.
  $effect(() => { if (page > totalPages) page = totalPages; });

  let paged = $derived.by(() => {
    if (pageSize === 0) return sorted;
    const start = (page - 1) * pageSize;
    return sorted.slice(start, start + pageSize);
  });
  let firstIdx = $derived(sorted.length === 0 ? 0 : (page - 1) * pageSize + 1);
  let lastIdx = $derived(Math.min(page * pageSize, sorted.length));

  // rowKey : (uuid or name) used to drive the selected-row highlight.
  // Mutations no longer live in this component — the parent drives
  // them via header N/E/D buttons or a dedicated drawer, so the
  // Actions column was removed to save horizontal space.
  function rowKey(r: Row): string {
    return (r.uuid as string) || (r.name as string) || '';
  }
</script>

<div class="overflow-x-auto rounded-box border border-base-300 bg-base-100">
  <table class="table table-zebra table-sm">
    <thead>
      <tr>
        {#each columns as c (c.key)}
          <th class="cursor-pointer select-none hover:text-base-content" onclick={() => toggleSort(c.key)}>
            <span class="inline-flex items-center gap-1">
              {c.label}
              {#if sortKey === c.key}
                <span class="text-[10px] opacity-70">{sortDir === 'asc' ? '▲' : '▼'}</span>
              {/if}
            </span>
          </th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each paged as r, i (i)}
        <tr class="hover"
          class:cursor-pointer={!!onSelect}
          class:bg-primary={selectedKey !== '' && rowKey(r) === selectedKey}
          class:text-primary-content={selectedKey !== '' && rowKey(r) === selectedKey}
          data-name={typeof r.name === 'string' ? r.name : ''}
          onclick={() => onSelect?.(r)}>
          {#each columns as c (c.key)}
            <td>
              {#if isStatus(c.key)}
                <span class="badge badge-sm {statusClass(r[c.key])}">{r[c.key]}</span>
              {:else if isSource(c.key)}
                <span class="badge badge-sm {sourceClass(r[c.key])}">{r[c.key]}</span>
              {:else if isBool(r[c.key])}
                <span class="badge badge-sm {r[c.key] ? 'badge-success' : 'badge-ghost'}">
                  {r[c.key] ? 'yes' : 'no'}
                </span>
              {:else if isEmpty(r[c.key])}
                <span class="text-base-content/30">—</span>
              {:else if c.key === 'name' || c.key === 'username' || c.key === 'address'}
                <span class="font-medium">{r[c.key]}</span>
              {:else}
                {r[c.key]}
              {/if}
            </td>
          {/each}
        </tr>
      {:else}
        <tr>
          <td colspan={columns.length} class="py-8 text-center text-base-content/50">
            No matching rows.
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<!-- Bottom bar : always visible. Shows total / window / page nav /
     rows-per-page / reload. Mirrors the layout other dashboards use
     for their tables — operator gets a single place for navigation
     and a fresh-data button. -->
<div class="mt-2 flex flex-wrap items-center gap-3 text-xs text-base-content/70">
  <span class="tabular-nums">
    {#if sorted.length === 0}
      <span class="text-base-content/50">no items</span>
    {:else}
      Showing <span class="font-medium text-base-content">{firstIdx}–{lastIdx}</span>
      of <span class="font-medium text-base-content">{sorted.length}</span>
      {sorted.length === 1 ? 'item' : 'items'}
    {/if}
  </span>

  <span class="ml-auto inline-flex items-center gap-1">
    <span>rows / page</span>
    <select class="select select-xs select-bordered"
      value={pageSize}
      onchange={(e) => { pageSize = Number(e.currentTarget.value); page = 1; }}>
      {#each PAGE_SIZES as n (n)}
        <option value={n}>{n}</option>
      {/each}
      <option value={0}>all</option>
    </select>
  </span>

  <span class="inline-flex items-center gap-1">
    <button class="btn btn-ghost btn-xs" disabled={page <= 1}
      onclick={() => (page = 1)} title="First page" aria-label="First page">«</button>
    <button class="btn btn-ghost btn-xs" disabled={page <= 1}
      onclick={() => (page -= 1)} title="Previous page" aria-label="Previous page">‹</button>
    <span class="tabular-nums px-1">
      page <span class="font-medium text-base-content">{page}</span> / {totalPages}
    </span>
    <button class="btn btn-ghost btn-xs" disabled={page >= totalPages}
      onclick={() => (page += 1)} title="Next page" aria-label="Next page">›</button>
    <button class="btn btn-ghost btn-xs" disabled={page >= totalPages}
      onclick={() => (page = totalPages)} title="Last page" aria-label="Last page">»</button>
  </span>

  {#if onReload}
    <button class="btn btn-ghost btn-xs gap-1"
      onclick={() => onReload?.()}
      title="Reload the table" aria-label="Reload">
      <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3 12a9 9 0 0 1 15.5-6.3L21 8" />
        <path d="M21 3v5h-5" />
        <path d="M21 12a9 9 0 0 1-15.5 6.3L3 16" />
        <path d="M3 21v-5h5" />
      </svg>
      Reload
    </button>
  {/if}
</div>
