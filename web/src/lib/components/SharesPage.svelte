<script lang="ts">
  import { getRows, type Row } from '../api';
  import FileBrowser from './FileBrowser.svelte';

  let shares = $state<Row[]>([]);
  let selected = $state<string>('');

  $effect(() => {
    getRows('shares').then((rows) => {
      shares = rows;
      if (!selected && rows.length) selected = String(rows[0].name);
    });
  });

  let current = $derived(shares.find((s) => String(s.name) === selected));
</script>

<div>
  <h2 class="text-2xl font-bold">Shares</h2>
  <p class="text-sm text-base-content/60">POSIX filesystems (RWX) mounted across workloads</p>
</div>

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
          <span>{current.size_gb} GB</span>
          <span>· {current.mounts} mounts</span>
          <span class="badge badge-sm badge-success">{current.status}</span>
        </div>
      {/if}
      {#key selected}
        <FileBrowser kind="shares" container={selected} />
      {/key}
    {/if}
  </section>
</div>
