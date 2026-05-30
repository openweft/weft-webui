<script lang="ts">
  import { getRows, getMe, deleteShare, type Me, type Row } from '../api';
  import FileBrowser from './FileBrowser.svelte';
  import CreateShareModal from './CreateShareModal.svelte';

  let shares = $state<Row[]>([]);
  let selected = $state<string>('');
  let me = $state<Me | null>(null);

  // The "+ New share" affordance is gated client-side : visible only
  // to tenant admins (cluster admins included). The server enforces
  // the same check ; the gate just avoids surfacing a button that
  // would 403 anyway.
  let canCreate = $derived(!!me && (me.cluster_admin || me.tenant_admin));

  function refresh() {
    getRows('shares').then((rows) => {
      shares = rows;
      if (rows.length && !rows.find((r) => r.name === selected)) {
        selected = String(rows[0].name);
      } else if (rows.length === 0) {
        selected = '';
      }
    });
  }
  $effect(refresh);
  $effect(() => { getMe().then((u) => (me = u)).catch(() => { /* api.ts handled it */ }); });

  let current = $derived(shares.find((s) => String(s.name) === selected));

  let createOpen = $state(false);
  let deleteError = $state('');

  async function delSelected() {
    if (!current) return;
    if (!confirm(`Delete share ${current.name} ? CubeFS volume must be unmounted from every microVM first.`)) return;
    deleteError = '';
    try {
      await deleteShare(String(current.name));
      refresh();
    } catch (e) { deleteError = String(e); }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Shares</h2>
    <p class="text-sm text-base-content/60">POSIX filesystems (RWX) mounted across workloads</p>
  </div>
  {#if canCreate}
    <button class="ml-auto btn btn-sm btn-primary gap-1" onclick={() => (createOpen = true)}>
      <span class="text-base leading-none">+</span> New share
    </button>
  {/if}
</div>

{#if deleteError}
  <div class="mt-2 alert alert-error text-sm">{deleteError}</div>
{/if}

<div class="mt-4 flex gap-4">
  <aside class="w-60 shrink-0">
    <div class="mb-2 text-xs font-semibold uppercase tracking-wide text-base-content/60">Shares</div>
    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each shares as s (s.name)}
        <li>
          <button class:menu-active={selected === s.name} onclick={() => (selected = String(s.name))}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M3 7h6l2 2h10v9H3z" stroke-linejoin="round" />
            </svg>
            <div class="min-w-0">
              <div class="truncate">{s.name}</div>
              <div class="text-[10px] text-base-content/50">{s.backend} · {s.mounts} mounts</div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">No shares.</li>
      {/each}
    </ul>
  </aside>

  <section class="min-w-0 flex-1">
    {#if !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a share.
      </div>
    {:else}
      {#if current}
        <div class="mb-2 flex flex-wrap items-center gap-2 text-sm text-base-content/60">
          <span class="badge badge-sm badge-ghost">{current.backend}</span>
          <span class="badge badge-sm badge-ghost">project {current.project ?? '—'}</span>
          <span>{current.size_gb} GB</span>
          {#if current.readonly}<span class="badge badge-sm badge-warning">RO</span>{/if}
          <span>· {current.mounts} mounts</span>
          <span class="badge badge-sm" class:badge-success={current.status === 'active'}
            class:badge-warning={current.status === 'provisioning'}
            class:badge-error={current.status === 'failed'}>{current.status}</span>
          {#if canCreate}
            <button class="ml-auto btn btn-xs btn-ghost text-error" onclick={delSelected}>
              Delete
            </button>
          {/if}
        </div>
      {/if}
      {#key selected}
        <FileBrowser kind="shares" container={selected} />
      {/key}
    {/if}
  </section>
</div>

<CreateShareModal bind:open={createOpen} onCreated={refresh} />
