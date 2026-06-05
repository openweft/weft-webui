<script lang="ts">
  // GroupsTreePage — Identity → Groups in a tenant-first tree.
  //
  // The flat /api/resources/groups stream carries one row per
  // (tenant, group) pair. The natural hierarchy is tenant → group,
  // so we render <details> nodes per tenant with the group list
  // inside. Empty tenants (no groups) still surface so a tenant-
  // admin can see "this tenant has no groups yet" at a glance.
  //
  // Live signal : polls /api/resources/groups (and /api/resources/tenants
  // for the empty-tenant case) every 8 s. Same idiom as the inventory
  // tree but tenant-scoped.
  //
  // Right pane intentionally LEAN : group description + member count.
  // Creation / editing lives on the existing tenant-detail page
  // (TenantsPage's per-tenant drawer) — duplicating CRUD here would
  // diverge silently from the canonical source.

  import { onMount, onDestroy } from 'svelte';
  import { getRowsPage, type Row, type ResourceMeta } from '../api';

  let { meta }: { meta: ResourceMeta } = $props();

  let groups = $state<Row[]>([]);
  let tenants = $state<Row[]>([]);
  let loadErr = $state('');
  let lastRefresh = $state<string>('');

  type Selected = { tenant: string; group?: string } | null;
  let selected = $state<Selected>(null);

  // Expanded tenants — default = all open so the operator sees the
  // whole tree on first load. A future preference could remember the
  // collapsed set per session ; for now stateless is fine.
  let expanded = $state<Set<string>>(new Set());
  let initialised = $state(false);

  let pollTimer: ReturnType<typeof setInterval> | undefined;
  const POLL_MS = 8000;

  async function refresh() {
    try {
      const [g, t] = await Promise.all([
        getRowsPage('groups',  { limit: 1000 }),
        getRowsPage('tenants', { limit: 1000 }),
      ]);
      groups = g.rows ?? [];
      tenants = t.rows ?? [];
      // Seed expanded on first successful load.
      if (!initialised) {
        for (const row of tenants) expanded.add(String(row.name ?? ''));
        expanded = new Set(expanded); // trigger reactivity
        initialised = true;
      }
      loadErr = '';
      lastRefresh = new Date().toLocaleTimeString();
    } catch (e) {
      loadErr = String(e);
    }
  }

  onMount(() => {
    refresh();
    pollTimer = setInterval(refresh, POLL_MS);
  });
  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  // Group the flat rows by tenant. We START from the tenants list
  // (so empty tenants surface) and merge in the group rows from the
  // groups stream. A group whose tenant is missing from the tenants
  // list still shows up under a "(unknown tenant)" bucket — that
  // shouldn't happen in practice but rendering honestly surfaces
  // any cross-store drift bug.
  let groupsByTenant = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const t of tenants) {
      m.set(String(t.name ?? ''), []);
    }
    for (const g of groups) {
      const k = String(g.tenant ?? '');
      const bucket = m.get(k) ?? [];
      bucket.push(g);
      m.set(k, bucket);
    }
    // Sort group lists by name for stable display.
    for (const list of m.values()) {
      list.sort((a, b) => String(a.name ?? '').localeCompare(String(b.name ?? '')));
    }
    return m;
  });

  // Tenant ordering : alphabetical by name. The flat /api/resources
  // already sorts, but we re-sort here in case the merged tenant
  // list (mock + future live) drifts in order.
  let tenantNames = $derived.by(() => {
    const names = Array.from(groupsByTenant.keys());
    names.sort((a, b) => a.localeCompare(b));
    return names;
  });

  // Summary stats for the header.
  let totalGroups = $derived(groups.length);
  let totalTenants = $derived(tenantNames.length);

  function toggleTenant(name: string) {
    if (expanded.has(name)) expanded.delete(name);
    else expanded.add(name);
    expanded = new Set(expanded);
  }
  function expandAll() {
    expanded = new Set(tenantNames);
  }
  function collapseAll() {
    expanded = new Set();
  }

  function selectTenant(name: string) {
    selected = { tenant: name };
  }
  function selectGroup(tenant: string, group: string) {
    selected = { tenant, group };
  }

  // The right pane reads either a group detail (when selected.group
  // is set) or a tenant aggregate (when only selected.tenant is set).
  let detail = $derived.by(() => {
    const s = selected;
    if (!s) return null;
    const list = groupsByTenant.get(s.tenant) ?? [];
    if (s.group) {
      return list.find((g) => String(g.name ?? '') === s.group) ?? null;
    }
    return null;
  });

  function asString(v: unknown): string {
    return typeof v === 'string' ? v : '';
  }
  function asInt(v: unknown): number {
    if (typeof v === 'number') return v;
    if (typeof v === 'string' && v.length > 0 && Number.isFinite(Number(v))) return Number(v);
    return 0;
  }
</script>

<div class="flex h-full flex-col">
  <header class="flex flex-wrap items-center gap-3 border-b border-base-300 bg-base-100 px-5 py-3">
    <div>
      <h1 class="text-lg font-bold">{meta.label}</h1>
      <p class="text-xs text-base-content/60">
        {totalGroups} groups across {totalTenants} tenants · grouped by tenant
        {#if lastRefresh}· refreshed {lastRefresh}{/if}
      </p>
    </div>
    <div class="ml-auto flex items-center gap-2">
      <button type="button" class="btn btn-xs btn-ghost" onclick={expandAll}>Expand all</button>
      <button type="button" class="btn btn-xs btn-ghost" onclick={collapseAll}>Collapse all</button>
      <button type="button" class="btn btn-xs btn-ghost" onclick={refresh}>Refresh</button>
    </div>
  </header>

  {#if loadErr}
    <div class="m-4 alert alert-error py-2 text-sm">{loadErr}</div>
  {/if}

  <div class="grid flex-1 grid-cols-1 lg:grid-cols-[1fr_22rem] overflow-hidden">
    <!-- Tree -->
    <div class="overflow-y-auto p-4">
      <ul class="space-y-1">
        {#each tenantNames as tenant (tenant)}
          {@const tenantGroups = groupsByTenant.get(tenant) ?? []}
          {@const isOpen = expanded.has(tenant)}
          <li>
            <div class="flex items-center gap-1">
              <button type="button"
                class="btn btn-ghost btn-xs px-1 w-6"
                onclick={() => toggleTenant(tenant)}
                aria-label={isOpen ? `Collapse ${tenant || '(no tenant)'}` : `Expand ${tenant || '(no tenant)'}`}>
                {isOpen ? '▼' : '▶'}
              </button>
              <button type="button"
                class="flex-1 text-left rounded px-2 py-1 text-sm hover:bg-base-200
                       {selected?.tenant === tenant && !selected?.group ? 'bg-base-200 font-semibold' : ''}"
                onclick={() => selectTenant(tenant)}>
                <span class="mr-2">🏢</span>
                {tenant || '(no tenant)'}
                <span class="ml-2 text-xs text-base-content/50">{tenantGroups.length} group{tenantGroups.length === 1 ? '' : 's'}</span>
              </button>
            </div>

            {#if isOpen}
              <ul class="ml-8 mt-1 space-y-0.5 border-l border-base-300 pl-2">
                {#if tenantGroups.length === 0}
                  <li class="px-2 py-1 text-xs italic text-base-content/40">no groups</li>
                {:else}
                  {#each tenantGroups as g (g.uuid ?? g.name)}
                    {@const name = asString(g.name)}
                    {@const desc = asString(g.description)}
                    {@const members = asInt(g.members)}
                    {@const sel = selected?.tenant === tenant && selected?.group === name}
                    <li>
                      <button type="button"
                        class="flex w-full items-center gap-2 rounded px-2 py-1 text-left text-sm hover:bg-base-200
                               {sel ? 'bg-primary/15 font-semibold' : ''}"
                        onclick={() => selectGroup(tenant, name)}>
                        <span>👥</span>
                        <span class="font-mono text-xs">{name}</span>
                        <span class="ml-auto text-xs text-base-content/50 tabular-nums">{members} member{members === 1 ? '' : 's'}</span>
                      </button>
                      {#if desc}
                        <p class="ml-8 truncate text-xs text-base-content/40" title={desc}>{desc}</p>
                      {/if}
                    </li>
                  {/each}
                {/if}
              </ul>
            {/if}
          </li>
        {/each}

        {#if tenantNames.length === 0}
          <li class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-base-content/50">
            no tenants registered
          </li>
        {/if}
      </ul>
    </div>

    <!-- Detail pane -->
    <aside class="border-l border-base-300 bg-base-200/30 overflow-y-auto p-4">
      {#if !selected}
        <p class="text-sm text-base-content/50">Select a tenant or group on the left.</p>
      {:else if selected.group && detail}
        <h2 class="text-base font-semibold">
          <span class="mr-1">👥</span>{detail.name}
        </h2>
        <p class="text-xs text-base-content/60">In tenant <span class="font-mono">{selected.tenant}</span></p>
        <div class="mt-3 grid gap-2 text-sm">
          <div>
            <div class="text-xs font-medium text-base-content/60">Description</div>
            <div class="mt-1">{asString(detail.description) || '—'}</div>
          </div>
          <div>
            <div class="text-xs font-medium text-base-content/60">Members</div>
            <div class="mt-1 tabular-nums">{asInt(detail.members)}</div>
          </div>
        </div>
        <p class="mt-4 text-xs text-base-content/50">
          Group membership is edited on the tenant detail page
          (Identity → Tenants → <span class="font-mono">{selected.tenant}</span>).
        </p>
      {:else if selected.group}
        <p class="text-sm text-base-content/50">Group not found.</p>
      {:else}
        <h2 class="text-base font-semibold">
          <span class="mr-1">🏢</span>{selected.tenant || '(no tenant)'}
        </h2>
        {@const list = groupsByTenant.get(selected.tenant) ?? []}
        <p class="text-xs text-base-content/60">{list.length} group{list.length === 1 ? '' : 's'}</p>
        {#if list.length > 0}
          <ul class="mt-3 space-y-1 text-sm">
            {#each list as g (g.uuid ?? g.name)}
              <li class="flex items-center gap-2">
                <span class="font-mono text-xs">{asString(g.name)}</span>
                <span class="ml-auto text-xs text-base-content/50">{asInt(g.members)} m.</span>
              </li>
            {/each}
          </ul>
        {/if}
      {/if}
    </aside>
  </div>
</div>
