<script lang="ts">
  import { startVM, stopVM, deleteVM, deleteVolume, deleteNetwork, type Column, type Row } from '../api';

  let {
    columns,
    rows,
    resourceId = '',
    onChange,
  }: {
    columns: Column[];
    rows: Row[];
    // Optional : the registry id this table represents (e.g. "microvms",
    // "volumes"). Used to pick the right live mutator from the row
    // dropdown. Tables that don't pass it stay read-only.
    resourceId?: string;
    // Called after a successful mutation so the parent can refresh.
    onChange?: () => void;
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

  // ---- row actions ----
  //
  // Tables that pass resourceId light up the dropdown with real
  // mutators. Errors surface as a banner so the operator sees what
  // went wrong (typically a 503 in mock mode, or a 502 with the gRPC
  // status text).
  let actionError = $state('');
  let busyRow = $state<string | null>(null);

  function rowKey(r: Row): string {
    return (r.uuid as string) || (r.name as string) || '';
  }

  async function runAction(action: 'start' | 'stop' | 'delete', r: Row) {
    actionError = '';
    const key = rowKey(r);
    busyRow = key;
    try {
      switch (resourceId) {
        case 'microvms': {
          const name = r.name as string;
          if (action === 'start')  await startVM(name);
          if (action === 'stop')   await stopVM(name);
          if (action === 'delete') {
            if (!confirm(`Delete microVM ${name} ? This is irreversible.`)) break;
            await deleteVM(name);
          }
          break;
        }
        case 'volumes': {
          if (action !== 'delete') break;
          const uuid = r.uuid as string;
          if (!confirm(`Delete volume ${r.name} ? This is irreversible.`)) break;
          await deleteVolume(uuid);
          break;
        }
        case 'networks': {
          if (action !== 'delete') break;
          const uuid = r.uuid as string;
          if (!confirm(`Delete network ${r.name} ?`)) break;
          await deleteNetwork(uuid);
          break;
        }
      }
      onChange?.();
    } catch (e) {
      actionError = String(e);
    } finally {
      busyRow = null;
    }
  }

  // Which actions does the row dropdown surface, given the resource ?
  const showStartStop = $derived(resourceId === 'microvms');
  const showDelete    = $derived(['microvms', 'volumes', 'networks'].includes(resourceId));
  const liveWired     = $derived(showStartStop || showDelete);
</script>

{#if actionError}
  <div class="alert alert-error mb-2 text-sm">
    {actionError}
    <button class="ml-auto btn btn-xs btn-ghost" onclick={() => (actionError = '')}>dismiss</button>
  </div>
{/if}

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
              <div tabindex="0" role="button" class="btn btn-ghost btn-xs">
                {#if busyRow === rowKey(r)}
                  <span class="loading loading-spinner loading-xs"></span>
                {:else}⋯{/if}
              </div>
              <ul class="menu dropdown-content z-10 w-36 rounded-box bg-base-100 p-1 shadow">
                {#if showStartStop}
                  <li><button onclick={() => runAction('start', r)}>Start</button></li>
                  <li><button onclick={() => runAction('stop',  r)}>Stop</button></li>
                {/if}
                {#if !liveWired}
                  <li class="disabled px-2 py-1 text-xs text-base-content/50">read-only</li>
                {/if}
                {#if showDelete}
                  <li>
                    <button class="text-error" onclick={() => runAction('delete', r)}>Delete</button>
                  </li>
                {/if}
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
