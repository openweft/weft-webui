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
</script>

<div class="overflow-x-auto rounded-box border border-base-300 bg-base-100">
  <table class="table table-zebra table-sm">
    <thead>
      <tr>
        {#each columns as c (c.key)}
          <th>{c.label}</th>
        {/each}
        <th class="w-0 text-right">Actions</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as r, i (i)}
        <tr class="hover">
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
