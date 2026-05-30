<script lang="ts">
  // PluginsPage — superadmin-only marketplace of *-as-a-service
  // modules. Installing / enabling a plugin makes its contributed
  // resources visible in the sidebar (no rebuild, no reload —
  // App.svelte re-fetches /api/resources via the onChange callback).
  //
  // Master-detail aligned on DNSPage / SecurityPage / NetworksPage :
  //   Master — plugin list with kind badge + install state
  //   Detail — plugin description + Install / Uninstall / Enable / Disable
  import {
    listPlugins, installPlugin, uninstallPlugin,
    enablePlugin, disablePlugin,
    type Plugin, type ResourceMeta,
  } from '../api';

  let {
    meta,
    onChange,
  }: {
    meta: ResourceMeta;
    // Fires after install / uninstall / enable / disable so the
    // parent (App.svelte) re-fetches the resource catalogue and
    // the sidebar reflects the new state.
    onChange: () => void;
  } = $props();

  let plugins = $state<Plugin[]>([]);
  let loading = $state(true);
  let err = $state('');
  let query = $state('');
  let sectionFilter = $state('all');
  let selectedID = $state('');

  function refresh() {
    loading = true; err = '';
    listPlugins()
      .then((ps) => {
        plugins = ps;
        if (selectedID && !ps.find((p) => p.id === selectedID)) {
          selectedID = ps[0]?.id ?? '';
        } else if (!selectedID && ps.length > 0) {
          selectedID = ps[0].id;
        }
      })
      .catch((e) => (err = String(e)))
      .finally(() => (loading = false));
  }
  $effect(refresh);

  // Stable section ordering for the category combo and the flat list
  // (sort key when sectionFilter === 'all'). New sections introduced
  // by future plugins land at the end in discovery order.
  const SECTION_ORDER = ['Storage', 'Network', 'Database', 'Cache', 'Streaming', 'Analytics', 'Lakehouse'];

  // All sections actually present in the catalogue, ordered by
  // SECTION_ORDER then by first-seen for anything novel. Drives the
  // combo's <option> list and the section-then-name sort of the flat
  // list.
  let allSections = $derived.by(() => {
    const seen = new Set<string>();
    for (const p of plugins) seen.add(p.section || 'Other');
    const ordered: string[] = [];
    for (const s of SECTION_ORDER) {
      if (seen.has(s)) { ordered.push(s); seen.delete(s); }
    }
    for (const s of seen) ordered.push(s);
    return ordered;
  });

  // Per-section counts shown next to each combo option, so the user
  // knows how many entries a category contains before clicking.
  let sectionCounts = $derived.by(() => {
    const m = new Map<string, number>();
    for (const p of plugins) {
      const k = p.section || 'Other';
      m.set(k, (m.get(k) ?? 0) + 1);
    }
    return m;
  });

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    const sectionRank = (s: string) => {
      const i = SECTION_ORDER.indexOf(s);
      return i === -1 ? SECTION_ORDER.length : i;
    };
    return plugins
      .filter((p) => sectionFilter === 'all' || (p.section || 'Other') === sectionFilter)
      .filter((p) => {
        if (!q) return true;
        return (
          p.id.toLowerCase().includes(q)
          || p.name.toLowerCase().includes(q)
          || p.vendor.toLowerCase().includes(q)
          || p.section.toLowerCase().includes(q)
        );
      })
      .sort((a, b) => {
        const sa = sectionRank(a.section || 'Other');
        const sb = sectionRank(b.section || 'Other');
        if (sa !== sb) return sa - sb;
        return a.name.localeCompare(b.name);
      });
  });

  let selected = $derived<Plugin | null>(
    plugins.find((p) => p.id === selectedID) ?? null,
  );

  // Plugins that overlap on at least one contributed resource. Two
  // plugins can stay co-installed (the gate is open as soon as either
  // is enabled), but for storage / registry / LB backends the
  // operator typically picks one — surfacing the overlap makes that
  // choice explicit.
  let alternatives = $derived.by(() => {
    if (!selected) return [] as Plugin[];
    const mine = new Set(selected.resources ?? []);
    return plugins.filter((p) => {
      if (p.id === selected!.id) return false;
      return (p.resources ?? []).some((r) => mine.has(r));
    });
  });

  // Plugins already installed+enabled that overlap with the one
  // we're about to install — used by the install handler to warn
  // before opening a second backend for the same resource.
  let activeConflict = $derived(
    alternatives.find((p) => p.install_status === 'installed' && p.enabled) ?? null,
  );

  function clickPlugin(p: Plugin) {
    selectedID = selectedID === p.id ? '' : p.id;
    actionErr = '';
  }

  let actionBusy = $state(false);
  let actionErr = $state('');

  async function doInstall() {
    if (!selected) return;
    if (activeConflict) {
      const overlapping = (selected.resources ?? []).filter((r) =>
        (activeConflict!.resources ?? []).includes(r),
      ).join(', ');
      const msg = `"${activeConflict.name}" already provides ${overlapping}. `
        + `Two plugins serving the same resource is supported but only meaningful in specific patterns `
        + `(e.g. versitygw S3 surface on top of a CubeFS POSIX backend). `
        + `If you meant to switch backends, the recommended path is migrate data, then uninstall the previous one.\n\n`
        + `Continue installing "${selected.name}" anyway?`;
      if (!confirm(msg)) return;
    }
    actionBusy = true; actionErr = '';
    try {
      await installPlugin(selected.id);
      refresh();
      onChange();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }
  async function doUninstall() {
    if (!selected) return;
    const contributed = (selected.resources ?? []).length;
    if (!confirm(`Uninstall "${selected.name}" ? Its ${contributed} contributed resource${contributed === 1 ? '' : 's'} will disappear from the sidebar. Existing rows stay in the backend ; reinstalling restores visibility.`)) return;
    actionBusy = true; actionErr = '';
    try {
      await uninstallPlugin(selected.id);
      refresh();
      onChange();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }
  async function doToggleEnabled() {
    if (!selected) return;
    actionBusy = true; actionErr = '';
    try {
      if (selected.enabled) await disablePlugin(selected.id);
      else await enablePlugin(selected.id);
      refresh();
      onChange();
    } catch (e) {
      actionErr = String(e);
    } finally {
      actionBusy = false;
    }
  }

  function statusBadge(p: Plugin): { class: string; label: string } {
    if (p.install_status === 'installed' && p.enabled) return { class: 'badge-success', label: 'enabled' };
    if (p.install_status === 'installed') return { class: 'badge-warning', label: 'disabled' };
    return { class: 'badge-ghost', label: 'available' };
  }

  function fmtUpdated(ts: string): string {
    if (!ts) return '—';
    return ts.slice(0, 19).replace('T', ' ');
  }

  let installedCount = $derived(plugins.filter((p) => p.install_status === 'installed').length);
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      *-as-a-service marketplace · {installedCount} of {plugins.length} installed.
      Installing a plugin exposes its contributed resources in the sidebar.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Master : plugin list -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Marketplace</h3>
      {#if loading}<span class="loading loading-spinner loading-xs"></span>{/if}
      <span class="ml-auto text-xs text-base-content/50">{filtered.length} of {plugins.length}</span>
    </div>

    <select class="select select-sm select-bordered w-full" bind:value={sectionFilter}>
      <option value="all">All categories · {plugins.length}</option>
      {#each allSections as s (s)}
        <option value={s}>{s} · {sectionCounts.get(s) ?? 0}</option>
      {/each}
    </select>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter plugins…" bind:value={query} />
    </label>

    {#if err}<div class="alert alert-error py-2 text-sm">{err}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filtered as p (p.id)}
        {@const b = statusBadge(p)}
        <li>
          <button class:menu-active={selectedID === p.id}
            onclick={() => clickPlugin(p)}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M9 3v4H5v4h4v4H5v6h6v-4h4v4h6v-6h-4v-4h4V7h-6V3z" stroke-linejoin="round" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium">{p.name}</div>
              <div class="text-[10px] text-base-content/50">
                {p.vendor} · v{p.version}{sectionFilter === 'all' ? ` · ${p.section}` : ''}
              </div>
            </div>
            <span class="badge badge-xs {b.class}">{b.label}</span>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {plugins.length === 0 ? 'No plugins available.' : 'No plugins match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <!-- Detail : selected plugin description + actions -->
  <section class="min-w-0 flex-1 flex flex-col gap-3">
    {#if !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a plugin on the left.
      </div>
    {:else}
      {@const b = statusBadge(selected)}
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-lg font-semibold">{selected.name}</h3>
          <p class="text-xs text-base-content/50">
            <span class="font-mono">{selected.id}</span>
            · v{selected.version} · by {selected.vendor}
            · contributes to <span class="badge badge-xs badge-ghost">{selected.section}</span>
            · <span class="badge badge-xs {b.class}">{b.label}</span>
          </p>
        </div>
        <div class="ml-auto flex items-center gap-2">
          {#if selected.install_status === 'available'}
            <button class="btn btn-sm btn-primary" disabled={actionBusy} onclick={doInstall}
              title={`Install ${selected.name}`}>
              {#if actionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              Install
            </button>
          {:else}
            <button class="btn btn-sm btn-warning gap-1" disabled={actionBusy}
              onclick={doToggleEnabled}
              title={selected.enabled ? 'Hide the contributed resources without uninstalling' : 'Re-show the contributed resources'}>
              {#if actionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              {selected.enabled ? 'Disable' : 'Enable'}
            </button>
            <button class="btn btn-sm btn-error" disabled={actionBusy} onclick={doUninstall}
              title={`Uninstall ${selected.name}`}>
              Uninstall
            </button>
          {/if}
        </div>
      </div>

      {#if actionErr}<div class="alert alert-error py-2 text-sm">{actionErr}</div>{/if}

      <div class="rounded-box border border-base-300 bg-base-100 p-4">
        <p class="text-sm text-base-content/80">{selected.description}</p>
      </div>

      <div class="rounded-box border border-base-300 bg-base-100 p-4">
        <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60 mb-2">
          Contributes
        </h4>
        {#if (selected.resources ?? []).length === 0}
          <p class="text-sm text-base-content/50">No resources declared.</p>
        {:else}
          <ul class="flex flex-wrap gap-2">
            {#each (selected.resources ?? []) as r (r)}
              <li><code class="badge badge-sm badge-ghost">{r}</code></li>
            {/each}
          </ul>
          <p class="mt-2 text-xs text-base-content/50">
            These resource IDs become visible in the sidebar once the plugin is installed + enabled. Multiple plugins can contribute the same resource (e.g. cubefs-storage / ceph-storage both expose <code>shares</code> + <code>buckets</code>) — installing any of them opens the gate.
          </p>
        {/if}
      </div>

      {#if alternatives.length > 0}
        <div class="rounded-box border border-base-300 bg-base-100 p-4">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60 mb-2">
            Alternatives
          </h4>
          <ul class="flex flex-col gap-1">
            {#each alternatives as alt (alt.id)}
              {@const ab = statusBadge(alt)}
              {@const shared = (alt.resources ?? []).filter((r) => (selected!.resources ?? []).includes(r))}
              <li>
                <button class="flex w-full items-center gap-2 rounded-box px-2 py-1 text-left hover:bg-base-200"
                  onclick={() => (selectedID = alt.id)}>
                  <span class="font-medium text-sm">{alt.name}</span>
                  <span class="text-xs text-base-content/50">v{alt.version} · {alt.vendor}</span>
                  <span class="ml-auto flex items-center gap-1">
                    <span class="text-xs text-base-content/50">overlaps on <code class="font-mono">{shared.join(', ')}</code></span>
                    <span class="badge badge-xs {ab.class}">{ab.label}</span>
                  </span>
                </button>
              </li>
            {/each}
          </ul>
          {#if activeConflict && selected.install_status === 'available'}
            <p class="mt-2 text-xs text-warning">
              <span class="font-semibold">{activeConflict.name}</span> is currently active — installing this plugin alongside is supported but typically you migrate then uninstall the previous one.
            </p>
          {:else}
            <p class="mt-2 text-xs text-base-content/50">
              These plugins contribute one or more of the same resources. For storage / registry / LB backends, pick one and migrate data before switching.
            </p>
          {/if}
        </div>
      {/if}

      {#if selected.install_status === 'installed'}
        <div class="rounded-box border border-base-300 bg-base-100 p-4">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-base-content/60 mb-2">
            Install record
          </h4>
          <dl class="grid grid-cols-2 gap-2 text-xs">
            <div><dt class="text-base-content/50">Installed at</dt>
              <dd class="font-mono">{fmtUpdated(selected.installed_at ?? '')}</dd></div>
            <div><dt class="text-base-content/50">Installed by</dt>
              <dd class="font-mono">{selected.installed_by || '—'}</dd></div>
          </dl>
        </div>
      {/if}
    {/if}
  </section>
</div>
