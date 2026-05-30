<script lang="ts">
  import type { Column, Row } from '../api';

  let { columns, rows }: { columns: Column[]; rows: Row[] } = $props();

  // Map a status-ish value to a DaisyUI badge colour.
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
    if (ae || be) return ae === be ? 0 : ae ? 1 : -1; // empties last
    if (typeof a === 'number' && typeof b === 'number') return a - b;
    if (typeof a === 'boolean' && typeof b === 'boolean') return (a ? 1 : 0) - (b ? 1 : 0);
    return String(a).localeCompare(String(b), undefined, { numeric: true });
  }

  let sorted = $derived.by(() => {
    if (!sortKey) return rows;
    const dir = sortDir === 'asc' ? 1 : -1;
    return [...rows].sort((x, y) => cmp(x[sortKey], y[sortKey]) * dir);
  });
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
        <th class="w-0 text-right">Actions</th>
      </tr>
    </thead>
    <tbody>
      {#each sorted as r, i (i)}
        <!-- data-name carries the row's `name` cell so a wrapper can
             intercept clicks (e.g. TenantsPage drill-down). -->
        <tr class="hover" data-name={typeof r.name === 'string' ? r.name : ''}>
          {#each columns as c (c.key)}
            <td>
              {#if isStatus(c.key)}
                <span class="badge badge-sm {statusClass(r[c.key])}">{r[c.key]}</span>
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
          <td class="text-right">
            <div class="dropdown dropdown-end">
              <div tabindex="0" role="button" class="btn btn-ghost btn-xs">⋯</div>
              <ul class="menu dropdown-content z-10 w-32 rounded-box bg-base-100 p-1 shadow">
                <li><button>View</button></li>
                <li><button>Edit</button></li>
                <li><button class="text-error">Delete</button></li>
              </ul>
            </div>
          </td>
        </tr>
      {:else}
        <tr>
          <td colspan={columns.length + 1} class="py-8 text-center text-base-content/50">
            No matching rows.
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
