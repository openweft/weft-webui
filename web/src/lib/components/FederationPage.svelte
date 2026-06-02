<script lang="ts">
  // FederationPage — federation-lite peer table mirroring what
  // `weft federation list` prints on the CLI. One row per peer, with
  // a color-coded status badge :
  //
  //   - live        (green)   recent successful poll
  //   - stale       (yellow)  last poll older than the stale TTL
  //   - unreachable (red)     no successful poll on record
  //
  // The table is read-only ; mutating the federation membership is
  // done via `weft federation join / leave` on the CLI (no UI surface
  // for it yet — federation v0.2 keeps the table operator-driven).
  import { listFederationPeers, type FederationPeer } from '../api';

  let peers = $state<FederationPeer[]>([]);
  let loading = $state(true);
  let err = $state('');

  async function refresh() {
    loading = true; err = '';
    try {
      peers = await listFederationPeers();
    } catch (e) {
      err = String(e);
    } finally {
      loading = false;
    }
  }
  $effect(() => { void refresh(); });

  function statusBadge(s: string): { class: string; label: string } {
    if (s === 'live')        return { class: 'badge-success', label: 'live' };
    if (s === 'stale')       return { class: 'badge-warning', label: 'stale' };
    if (s === 'unreachable') return { class: 'badge-error',   label: 'unreachable' };
    return { class: 'badge-ghost', label: s };
  }

  function fmtLastSeen(nsUnix: number): string {
    if (!nsUnix || nsUnix === 0) return '—';
    const ms = Math.floor(nsUnix / 1_000_000);
    const d = new Date(ms);
    if (Number.isNaN(d.getTime())) return '—';
    const now = Date.now();
    const delta = Math.max(0, now - ms);
    return `${relative(delta)} ago (${d.toISOString().slice(0, 19).replace('T', ' ')} UTC)`;
  }

  // relative emits a coarse "27s" / "9m" / "2h" / "3d" string. Coarse
  // is fine — federation status is more useful than per-second drift.
  function relative(ms: number): string {
    const sec = Math.floor(ms / 1000);
    if (sec < 60) return `${sec}s`;
    const min = Math.floor(sec / 60);
    if (min < 60) return `${min}m`;
    const h = Math.floor(min / 60);
    if (h < 24) return `${h}h`;
    const d = Math.floor(h / 24);
    return `${d}d`;
  }

  // Live count drives the header badge ; a federation with every peer
  // unreachable is a P1, worth surfacing prominently.
  let liveCount = $derived(peers.filter((p) => p.status === 'live').length);
  let unreachableCount = $derived(peers.filter((p) => p.status === 'unreachable').length);
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Federation</h2>
    <p class="text-sm text-base-content/60">
      Federation-lite peer table · pulls each peer's
      <span class="font-mono">/cluster-info</span> on a 30s cadence.
      Mirrors <span class="font-mono">weft federation list</span>.
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <span class="badge badge-success">{liveCount} live</span>
    {#if unreachableCount > 0}
      <span class="badge badge-error">{unreachableCount} unreachable</span>
    {/if}
    <button class="btn btn-ghost btn-sm" onclick={refresh} title="Refresh peer table">
      {#if loading}<span class="loading loading-spinner loading-xs"></span>{:else}↻{/if}
    </button>
  </div>
</div>

{#if err}
  <div class="alert alert-error mt-4">{err}</div>
{/if}

<div class="mt-4 overflow-x-auto rounded-box border border-base-300 bg-base-100">
  <table class="table table-sm">
    <thead>
      <tr>
        <th>Name</th>
        <th>Region</th>
        <th>Weight</th>
        <th>Last seen</th>
        <th>Status</th>
        <th>URL</th>
      </tr>
    </thead>
    <tbody>
      {#each peers as p (p.url)}
        {@const b = statusBadge(p.status)}
        <tr>
          <td class="font-medium">{p.name || '—'}</td>
          <td>{p.region || '—'}</td>
          <td class="font-mono text-xs">{p.weight === 0 ? '100' : p.weight}</td>
          <td class="text-xs">
            {fmtLastSeen(p.last_seen_unix_ns)}
            {#if p.last_error}
              <div class="text-[10px] text-error/80">{p.last_error}</div>
            {/if}
          </td>
          <td><span class="badge {b.class}">{b.label}</span></td>
          <td class="font-mono text-[10px] text-base-content/60 max-w-xs truncate" title={p.url}>{p.url}</td>
        </tr>
      {:else}
        <tr><td colspan="6" class="text-center text-sm text-base-content/50 py-6">
          {loading ? 'Loading…' : 'No peers configured. Run `weft federation join` to add one.'}
        </td></tr>
      {/each}
    </tbody>
  </table>
</div>
