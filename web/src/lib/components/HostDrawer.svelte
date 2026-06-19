<script lang="ts">
  // HostDrawer — right-side slide-in detail view for one cluster host.
  // Opens when a HostsPage row is clicked. Mirrors the
  // MicroVMDrawer / SSHKeyDrawer / LoadBalancerDrawer pattern :
  //   fixed inset-y-0 right-0 · max-w-2xl · shadow-2xl · backdrop click
  //   closes · ✕ button in the header.
  //
  // Surfaces every field the dispatcher row carries (typed columns +
  // free-form properties / capabilities / network_types / volume_backends
  // when present). Lifecycle action buttons (cordon / uncordon / remove)
  // live in the footer and call back into the parent so the table
  // refresh + row removal stay in HostsPage.
  import { cordonHost, uncordonHost, removeHost, type HostRow } from '../api';

  let {
    host,
    canEdit,
    onClose,
    onChanged,
    onRemoved,
  }: {
    host: HostRow;
    canEdit: boolean;
    onClose: () => void;
    onChanged: () => void;
    onRemoved: (uuid: string) => void;
  } = $props();

  let busy = $state(false);
  let err = $state('');

  // Pretty-print one badge per status value. Same vocabulary as the
  // table page so the operator's eye doesn't have to re-learn colours.
  function statusBadge(s: string): string {
    switch (s) {
      case 'active':       return 'badge-success';
      case 'draining':     return 'badge-warning';
      case 'provisioning': return 'badge-info';
      case 'down':         return 'badge-error';
      case 'removed':      return 'badge-ghost';
      default:             return 'badge-ghost';
    }
  }

  // Render passthrough fields that aren't typed columns yet. Keys
  // come from the polymorphic dispatcher's row projection ; values
  // are coerced to printable strings here so the template doesn't
  // have to ternary-guard.
  function pretty(v: unknown): string {
    if (v == null) return '—';
    if (Array.isArray(v)) return v.join(', ') || '—';
    if (typeof v === 'object') {
      try { return JSON.stringify(v); } catch { return String(v); }
    }
    return String(v);
  }

  // Typed columns the table already shows — drawer hides them from the
  // "Extras" grid so the same info isn't printed twice.
  const KNOWN = new Set([
    'uuid', 'name', 'az', 'rack', 'hypervisor', 'arch', 'status',
    'connected', 'last_seen', 'gpu', 'position_u', 'height_u',
  ]);

  let extras = $derived.by(() => {
    return Object.entries(host)
      .filter(([k, v]) => !KNOWN.has(k) && v != null && v !== '')
      .sort(([a], [b]) => a.localeCompare(b));
  });

  async function doCordon() {
    busy = true; err = '';
    try { await cordonHost(host.uuid, host); onChanged(); } catch (e) { err = String(e); }
    finally { busy = false; }
  }

  async function doUncordon() {
    busy = true; err = '';
    try { await uncordonHost(host.uuid, host); onChanged(); } catch (e) { err = String(e); }
    finally { busy = false; }
  }

  async function doRemove() {
    if (!confirm(
      `Remove host "${host.name}" (${host.uuid}) from the inventory ?\n\n` +
      `The host record is deleted from etcd ; in-flight VMs on this host stay running ` +
      `until the operator drains them. Re-register with "weft host register" to undo.`,
    )) return;
    busy = true; err = '';
    try {
      await removeHost(host.uuid);
      onRemoved(host.uuid);
    } catch (e) {
      err = String(e);
    } finally {
      busy = false;
    }
  }
</script>

<!-- Backdrop : click closes. Same pattern as the other drawers. -->
<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col bg-base-100 shadow-2xl">
  <!-- Header -->
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div class="min-w-0">
      <h2 class="text-lg font-bold truncate font-mono">{host.name}</h2>
      <p class="text-xs text-base-content/60 flex items-center gap-2">
        <span class="badge badge-xs {statusBadge(host.status)}">{host.status || 'unknown'}</span>
        <span>{host.az || '—'} · {host.rack || '—'}</span>
        <span class="font-mono truncate">{host.uuid}</span>
      </p>
    </div>
    <button class="ml-auto btn btn-ghost btn-xs" aria-label="Close" onclick={onClose}>✕</button>
  </header>

  <!-- Body -->
  <div class="flex-1 overflow-y-auto px-5 py-4">
    <section class="rounded-box border border-base-300 bg-base-200/40 p-4">
      <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-base-content/60">
        Topology
      </h3>
      <div class="grid grid-cols-[10rem_1fr] gap-y-1 text-sm">
        <span class="text-base-content/50">Hostname</span>
        <span class="font-mono">{host.name}</span>

        <span class="text-base-content/50">UUID</span>
        <span class="font-mono text-xs break-all">{host.uuid}</span>

        <span class="text-base-content/50">Availability zone</span>
        <span class="font-mono">{host.az || '—'}</span>

        <span class="text-base-content/50">Rack</span>
        <span class="font-mono">{host.rack || '—'}</span>

        <span class="text-base-content/50">Position</span>
        <span class="font-mono">
          {host.position_u ? `U${host.position_u}` : '—'}
          {host.height_u && host.height_u > 1 ? ` (${host.height_u}U)` : ''}
        </span>
      </div>
    </section>

    <section class="mt-4 rounded-box border border-base-300 bg-base-200/40 p-4">
      <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-base-content/60">
        Hardware & backend
      </h3>
      <div class="grid grid-cols-[10rem_1fr] gap-y-1 text-sm">
        <span class="text-base-content/50">Hypervisor</span>
        <span class="font-mono">{host.hypervisor || '—'}</span>

        <span class="text-base-content/50">Architecture</span>
        <span class="font-mono">{host.arch || '—'}</span>

        <span class="text-base-content/50">GPU</span>
        <span class="font-mono">{host.gpu || 'none'}</span>
      </div>
    </section>

    <section class="mt-4 rounded-box border border-base-300 bg-base-200/40 p-4">
      <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-base-content/60">
        Lifecycle
      </h3>
      <div class="grid grid-cols-[10rem_1fr] gap-y-1 text-sm">
        <span class="text-base-content/50">Status</span>
        <span>
          <span class="badge badge-sm {statusBadge(host.status)}">{host.status || 'unknown'}</span>
        </span>

        <span class="text-base-content/50">Connected</span>
        <span>
          {#if host.connected === true}
            <span class="badge badge-sm badge-success">live</span>
          {:else if host.connected === false}
            <span class="badge badge-sm badge-error">disconnected</span>
          {:else}
            <span class="text-base-content/50">—</span>
          {/if}
        </span>

        <span class="text-base-content/50">Last seen</span>
        <span class="font-mono text-xs">{host.last_seen ?? '—'}</span>
      </div>
    </section>

    {#if extras.length > 0}
      <section class="mt-4 rounded-box border border-base-300 bg-base-200/40 p-4">
        <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-base-content/60">
          Extras
        </h3>
        <div class="grid grid-cols-[10rem_1fr] gap-y-1 text-sm">
          {#each extras as [k, v] (k)}
            <span class="text-base-content/50 font-mono">{k}</span>
            <span class="font-mono break-all">{pretty(v)}</span>
          {/each}
        </div>
      </section>
    {/if}

    {#if err}
      <div class="mt-4 alert alert-error py-2 text-sm">{err}</div>
    {/if}
  </div>

  <!-- Footer / actions -->
  {#if canEdit}
    <footer class="flex shrink-0 items-center gap-2 border-t border-base-300 px-5 py-3">
      {#if host.status === 'draining'}
        <button class="btn btn-sm btn-primary gap-1"
          disabled={busy}
          onclick={doUncordon}
          title="Mark this host eligible for scheduling again">
          {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Uncordon
        </button>
      {:else}
        <button class="btn btn-sm btn-warning gap-1"
          disabled={busy}
          onclick={doCordon}
          title="Stop scheduling new workloads on this host">
          {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Cordon
        </button>
      {/if}
      <button class="btn btn-sm btn-error gap-1 ml-auto"
        disabled={busy}
        onclick={doRemove}
        title="Delete the host record (irreversible)">
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Remove
      </button>
    </footer>
  {/if}
</aside>
