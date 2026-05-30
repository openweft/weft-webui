<script lang="ts">
  // RegistryPage — master-detail aligned on DNS / Security / Networks :
  //
  //   Master (left) : unified list of registries — the cluster's
  //     local zot.dc-* instances AND the configured remotes
  //     (proxy / replica). New / Edit / Delete only act on remotes ;
  //     locals are part of the cluster topology and can't be
  //     mutated from here.
  //   Detail (right) : artifacts hosted by the selected registry
  //     (filtered from /api/resources/registries by `registry` field).
  //     Pushing artifacts is a CLI workflow (`weft registry push …`) —
  //     authentication, multi-arch manifests, signing, and large
  //     multi-GB layer uploads don't fit a browser modal well.
  import {
    getRows, getMe,
    listRegistryRemotes, setRegistryRemote, deleteRegistryRemote,
    searchRegistryRemote,
    type ResourceMeta, type Row, type Me, type RegistryRemote,
    type RemoteSearchHit,
  } from '../api';
  import ArtifactRepoDrawer from './ArtifactRepoDrawer.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  // Local zots are baked into the cluster topology (one per DC).
  // Live wiring would read them from /api/hosts ; the dashboard mock
  // pins the three known names.
  const LOCAL_REGISTRIES = [
    { name: 'zot.dc-a', url: 'https://zot.dc-a.weft.local', kind: 'local' as const, az: 'DC-A' },
    { name: 'zot.dc-b', url: 'https://zot.dc-b.weft.local', kind: 'local' as const, az: 'DC-B' },
    { name: 'zot.dc-c', url: 'https://zot.dc-c.weft.local', kind: 'local' as const, az: 'DC-C' },
  ];

  type RegistryRow = {
    name: string;
    url: string;
    kind: 'local' | 'proxy' | 'replica';
    enabled: boolean;
    az?: string;
    last_sync?: string;
    username?: string;
    updated_at?: string;
    updated_by?: string;
  };

  // ---- shared ----
  let me = $state<Me | null>(null);
  let canEdit = $derived(!!me && (me.cluster_admin || me.tenant_admin));
  $effect(() => { getMe().then((u) => (me = u)).catch(() => { /* api.ts handled */ }); });

  // ---- registries (master) ----
  let remotes = $state<RegistryRemote[]>([]);
  let remotesLoading = $state(true);
  let remotesErr = $state('');
  let registryQuery = $state('');
  let selectedRegistry = $state<string>('zot.dc-a');

  function refreshRemotes() {
    remotesLoading = true; remotesErr = '';
    listRegistryRemotes()
      .then((rs) => (remotes = rs))
      .catch((e) => (remotesErr = String(e)))
      .finally(() => (remotesLoading = false));
  }
  $effect(refreshRemotes);

  let allRegistries = $derived.by<RegistryRow[]>(() => {
    const locals: RegistryRow[] = LOCAL_REGISTRIES.map((l) => ({
      name: l.name, url: l.url, kind: l.kind, enabled: true, az: l.az,
    }));
    const remoteRows: RegistryRow[] = remotes.map((r) => ({
      name: r.name, url: r.url,
      kind: (r.kind === 'replica' ? 'replica' : 'proxy'),
      enabled: !!r.enabled, last_sync: r.last_sync,
      username: r.username, updated_at: r.updated_at, updated_by: r.updated_by,
    }));
    return [...locals, ...remoteRows];
  });

  let filteredRegistries = $derived.by(() => {
    const q = registryQuery.trim().toLowerCase();
    if (!q) return allRegistries;
    return allRegistries.filter((r) =>
      r.name.toLowerCase().includes(q)
      || r.url.toLowerCase().includes(q)
      || r.kind.toLowerCase().includes(q),
    );
  });

  let selected = $derived<RegistryRow | null>(
    allRegistries.find((r) => r.name === selectedRegistry) ?? null,
  );
  let selectedIsRemote = $derived(selected !== null && selected.kind !== 'local');

  function clickRegistry(r: RegistryRow) {
    selectedRegistry = selectedRegistry === r.name ? '' : r.name;
    registryActionErr = '';
  }

  function kindBadge(k: string): string {
    switch (k) {
      case 'local':   return 'badge-success';
      case 'proxy':   return 'badge-info';
      case 'replica': return 'badge-warning';
      default:        return 'badge-ghost';
    }
  }

  // ---- registry remote edit modal ----
  let remoteDlg: HTMLDialogElement;
  let remoteMode = $state<'create' | 'edit'>('create');
  let remoteName = $state('');
  let remoteUrl = $state('');
  let remoteKind = $state<'proxy' | 'replica'>('proxy');
  let remoteEnabled = $state(true);
  let remoteUsername = $state('');
  let remoteFormErr = $state('');
  let remoteFormBusy = $state(false);
  let registryActionBusy = $state(false);
  let registryActionErr = $state('');

  function startRemoteNew() {
    registryActionErr = '';
    remoteMode = 'create';
    remoteName = '';
    remoteUrl = '';
    remoteKind = 'proxy';
    remoteEnabled = true;
    remoteUsername = '';
    remoteFormErr = '';
    remoteDlg.showModal();
  }

  function startRemoteEdit() {
    if (!selected || selected.kind === 'local') return;
    registryActionErr = '';
    remoteMode = 'edit';
    remoteName = selected.name;
    remoteUrl = selected.url;
    remoteKind = (selected.kind === 'replica' ? 'replica' : 'proxy');
    remoteEnabled = selected.enabled;
    remoteUsername = selected.username ?? '';
    remoteFormErr = '';
    remoteDlg.showModal();
  }

  async function submitRemote(e: SubmitEvent) {
    e.preventDefault();
    remoteFormErr = '';
    if (!remoteName.trim()) return (remoteFormErr = 'Name is required.');
    if (!remoteUrl.trim()) return (remoteFormErr = 'URL is required.');
    remoteFormBusy = true;
    try {
      await setRegistryRemote({
        name: remoteName.trim(),
        url: remoteUrl.trim(),
        kind: remoteKind,
        enabled: remoteEnabled,
        username: remoteUsername.trim() || undefined,
      });
      remoteDlg.close();
      selectedRegistry = remoteName.trim();
      refreshRemotes();
    } catch (err) {
      remoteFormErr = String(err);
    } finally {
      remoteFormBusy = false;
    }
  }

  async function startRemoteDelete() {
    if (!selected || selected.kind === 'local') return;
    if (!confirm(`Delete remote "${selected.name}" ? Sync against this remote stops on the next reconcile ; cached artifacts stay until garbage-collection.`)) return;
    registryActionBusy = true; registryActionErr = '';
    try {
      await deleteRegistryRemote(selected.name);
      selectedRegistry = 'zot.dc-a';
      refreshRemotes();
    } catch (e) {
      registryActionErr = String(e);
    } finally {
      registryActionBusy = false;
    }
  }

  // ---- artifacts (detail) ----
  let allArtifacts = $state<Row[]>([]);
  let artifactsLoading = $state(true);
  let artifactsErr = $state('');
  let artifactQuery = $state('');

  function refresh() {
    artifactsLoading = true;
    getRows('registries')
      .then((r) => (allArtifacts = r))
      .catch((e) => (artifactsErr = String(e)))
      .finally(() => (artifactsLoading = false));
  }
  $effect(refresh);

  let artifactsForSelected = $derived.by<Row[]>(() => {
    if (!selected) return [];
    const q = artifactQuery.trim().toLowerCase();
    return allArtifacts.filter((r) => {
      if (r.registry !== selected.name) return false;
      if (!q) return true;
      return Object.values(r).some((v) => String(v).toLowerCase().includes(q));
    });
  });

  // Group artifacts by repository so the table shows one row per
  // (registry, repository) — the per-tag rows live in
  // ArtifactRepoDrawer. Removes the duplicate-repo-per-tag spam in
  // the table view.
  type RepoGroup = {
    repository: string;
    type: string;
    arches: string;
    tagCount: number;
    sizeSummary: string;  // first tag's size, or "varies"
    lastPushed: string;   // first tag's "pushed" (mock doesn't carry timestamps to sort properly)
    tags: Row[];
  };
  let groupedArtifacts = $derived.by<RepoGroup[]>(() => {
    const m = new Map<string, RepoGroup>();
    for (const r of artifactsForSelected) {
      const repo = String(r.repository);
      const g = m.get(repo);
      if (g) {
        g.tagCount++;
        g.tags.push(r);
        if (g.sizeSummary !== String(r.size)) g.sizeSummary = 'varies';
      } else {
        m.set(repo, {
          repository: repo,
          type: String(r.type ?? '—'),
          arches: String(r.arch ?? '—'),
          tagCount: 1,
          sizeSummary: String(r.size ?? '—'),
          lastPushed: String(r.pushed ?? '—'),
          tags: [r],
        });
      }
    }
    return [...m.values()].sort((a, b) => a.repository.localeCompare(b.repository));
  });

  // Two-stage UX matching the rest of the dashboard : click a row
  // to select (highlight), click the header Edit button to open
  // the side drawer with the per-tag detail.
  let selectedRepoName = $state('');
  let selectedRepoGroup = $derived<RepoGroup | null>(
    groupedArtifacts.find((g) => g.repository === selectedRepoName) ?? null,
  );
  let drawerRepo = $state<RepoGroup | null>(null);

  function clickRepoRow(g: RepoGroup) {
    selectedRepoName = selectedRepoName === g.repository ? '' : g.repository;
  }
  function openRepoTags() {
    if (!selectedRepoGroup) return;
    drawerRepo = selectedRepoGroup;
  }
  function closeRepoDrawer() {
    drawerRepo = null;
  }

  // Reset selection when the active registry changes (the previous
  // selection's repo might not exist in the new registry).
  $effect(() => { selectedRegistry; selectedRepoName = ''; });

  function typeBadge(t: unknown): string {
    switch (String(t).toLowerCase()) {
      case 'container': return 'badge-info';
      case 'raw':       return 'badge-warning';
      case 'chart':     return 'badge-success';
      case 'model':     return 'badge-secondary';
      default:          return 'badge-ghost';
    }
  }

  // ---- proxy search mode ----
  //
  // Proxies are external registries (Docker Hub, GHCR, …) that we
  // can't enumerate — millions of images. The detail pane swaps the
  // list view for a search box ; the user queries the upstream by
  // repository substring and we surface matching hits. Local + Replica
  // keep the artifact-list mode unchanged.
  let searchQuery = $state('');
  let searchHits = $state<RemoteSearchHit[] | null>(null);
  let searchLoading = $state(false);
  let searchErr = $state('');

  async function runSearch() {
    if (!selected || selected.kind !== 'proxy') return;
    searchLoading = true; searchErr = '';
    try {
      searchHits = await searchRegistryRemote(selected.name, searchQuery.trim());
    } catch (e) {
      searchErr = String(e);
    } finally {
      searchLoading = false;
    }
  }

  // Auto-fetch a "featured" subset (empty query) when the operator
  // first lands on a proxy, then again on every selection change.
  $effect(() => {
    if (selected?.kind === 'proxy') {
      searchQuery = '';
      searchHits = null;
      searchErr = '';
      runSearch();
    } else {
      searchHits = null;
    }
  });

</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      Cluster registries + remote federations · pick a registry on the left to browse its artifacts on the right.
    </p>
  </div>
</div>

<div class="mt-4 flex gap-4">
  <!-- Master : registries list -->
  <section class="w-80 shrink-0 flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">Registries</h3>
      {#if remotesLoading}<span class="loading loading-spinner loading-xs"></span>{/if}
      <span class="ml-auto text-xs text-base-content/50">{filteredRegistries.length} of {allRegistries.length}</span>
    </div>

    <label class="input input-sm input-bordered flex items-center gap-2">
      <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <input type="search" class="grow" placeholder="Filter registries…" bind:value={registryQuery} />
    </label>

    {#if canEdit}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-sm btn-primary gap-1" onclick={startRemoteNew}
          title="Add a remote registry (proxy / replica)">
          <span class="text-base leading-none">+</span> New
        </button>
        <button class="btn btn-sm btn-warning gap-1"
          disabled={!selectedIsRemote || registryActionBusy}
          onclick={startRemoteEdit}
          title={selectedIsRemote ? `Edit "${selected?.name}"` : 'Select a remote to edit (locals are read-only)'}>
          Edit
        </button>
        <button class="btn btn-sm btn-error gap-1"
          disabled={!selectedIsRemote || registryActionBusy}
          onclick={startRemoteDelete}
          title={selectedIsRemote ? `Delete "${selected?.name}"` : 'Select a remote to delete (locals are read-only)'}>
          {#if registryActionBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Delete
        </button>
      </div>
    {/if}

    {#if remotesErr}<div class="alert alert-error py-2 text-sm">{remotesErr}</div>{/if}
    {#if registryActionErr}<div class="alert alert-error py-2 text-sm">{registryActionErr}</div>{/if}

    <ul class="menu menu-sm w-full rounded-box border border-base-300 bg-base-100">
      {#each filteredRegistries as r (r.name)}
        <li>
          <button class:menu-active={selectedRegistry === r.name}
            onclick={() => clickRegistry(r)}>
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-70" fill="none" stroke="currentColor" stroke-width="1.7">
              <rect x="3" y="6" width="18" height="12" rx="2" />
              <path d="M7 10h2M7 14h2" />
            </svg>
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-2">
                <span class="truncate font-medium">{r.name}</span>
                <span class="badge badge-xs {kindBadge(r.kind)}">{r.kind}</span>
                {#if !r.enabled}<span class="badge badge-xs badge-ghost">off</span>{/if}
              </div>
              <div class="text-[10px] text-base-content/50 font-mono truncate">
                {r.url}
              </div>
            </div>
          </button>
        </li>
      {:else}
        <li class="px-3 py-2 text-sm text-base-content/50">
          {allRegistries.length === 0 ? 'No registries.' : 'No registries match the filter.'}
        </li>
      {/each}
    </ul>
  </section>

  <!-- Detail : artifacts hosted by the selected registry -->
  <section class="min-w-0 flex-1 flex flex-col gap-2">
    {#if !selected}
      <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
        Select a registry to browse its artifacts.
      </div>
    {:else if selected.kind === 'proxy'}
      <!-- Proxy : remote external registry — search-only. We can't
           enumerate Docker Hub / GHCR ; the operator queries by
           repository substring and we surface the upstream hits. -->
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">
            Search <span class="font-mono normal-case text-base-content">{selected.name}</span>
          </h3>
          <p class="text-xs text-base-content/50">
            <span class="badge badge-xs {kindBadge(selected.kind)}">proxy</span>
            · external registry — search by repository name
            {#if selected.last_sync}· last sync {selected.last_sync.slice(0, 19).replace('T', ' ')}{/if}
          </p>
        </div>
      </div>

      <form class="flex items-center gap-2" onsubmit={(e) => { e.preventDefault(); runSearch(); }}>
        <label class="input input-sm input-bordered flex flex-1 items-center gap-2">
          <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
          </svg>
          <input type="search" class="grow"
            placeholder="repository substring (e.g. 'alpine', 'postgres', 'team-alpha/web')"
            bind:value={searchQuery} />
        </label>
        <button type="submit" class="btn btn-sm btn-primary" disabled={searchLoading}>
          {#if searchLoading}<span class="loading loading-spinner loading-xs"></span>{/if}
          Search
        </button>
      </form>

      {#if searchErr}<div class="alert alert-error py-2 text-sm">{searchErr}</div>{/if}

      {#if searchLoading && !searchHits}
        <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
      {:else if searchHits === null}
        <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
          Type a repository substring and hit Search to query <span class="font-mono">{selected.name}</span>.
        </div>
      {:else if searchHits.length === 0}
        <div class="rounded-box border border-base-300 bg-base-100 p-10 text-center text-base-content/50">
          No matches in <span class="font-mono">{selected.name}</span> for "{searchQuery}".
        </div>
      {:else}
        <div class="rounded-box border border-base-300 bg-base-100">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>Repository</th>
                <th>Tag</th>
                <th>Type</th>
                <th>Architectures</th>
                <th>Size</th>
                <th>Pushed</th>
              </tr>
            </thead>
            <tbody>
              {#each searchHits as h (`${h.repository}:${h.tag}`)}
                <tr>
                  <td class="font-mono">{h.repository}</td>
                  <td class="font-mono">{h.tag}</td>
                  <td><span class="badge badge-sm badge-ghost">{h.type}</span></td>
                  <td class="text-xs">{h.arches}</td>
                  <td class="font-mono text-xs">{h.size}</td>
                  <td class="text-xs text-base-content/70">{h.pushed}</td>
                </tr>
              {/each}
            </tbody>
          </table>
          {#if !searchQuery.trim()}
            <p class="px-3 py-2 text-xs text-base-content/40">
              Showing a "featured" subset · type a query and Search to widen the lookup.
            </p>
          {/if}
        </div>
      {/if}

    {:else}
      <!-- Local zot.dc-* or Replica : we own the artifacts, list directly. -->
      <div class="flex items-center gap-2">
        <div>
          <h3 class="text-sm font-semibold uppercase tracking-wide text-base-content/60">
            Artifacts in <span class="font-mono normal-case text-base-content">{selected.name}</span>
          </h3>
          <p class="text-xs text-base-content/50">
            <span class="badge badge-xs {kindBadge(selected.kind)}">{selected.kind}</span>
            · {groupedArtifacts.length} {groupedArtifacts.length === 1 ? 'repository' : 'repositories'}
            · {artifactsForSelected.length} {artifactsForSelected.length === 1 ? 'tag' : 'tags'}
            {#if selected.kind === 'replica' && selected.last_sync}
              · last sync {selected.last_sync.slice(0, 19).replace('T', ' ')}
            {/if}
          </p>
        </div>
        <div class="ml-auto flex items-center gap-2">
          <label class="input input-sm input-bordered flex items-center gap-2">
            <svg viewBox="0 0 24 24" class="h-4 w-4 opacity-50" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
            </svg>
            <input type="search" class="grow" placeholder="Filter artifacts…" bind:value={artifactQuery} />
          </label>
          <button class="btn btn-sm btn-primary gap-1"
            disabled={!selectedRepoGroup}
            onclick={openRepoTags}
            title={selectedRepoGroup ? `View tags for "${selectedRepoGroup.repository}"` : 'Select a repository to view its tags'}>
            <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M7 7h.01M7 3h5a2 2 0 0 1 1.41.59l7 7a2 2 0 0 1 0 2.82l-6 6a2 2 0 0 1-2.82 0l-7-7A2 2 0 0 1 4 12V7a4 4 0 0 1 4-4z" />
            </svg>
            Tags
          </button>
        </div>
      </div>

      {#if artifactsErr}<div class="alert alert-error py-2 text-sm">{artifactsErr}</div>{/if}

      {#if artifactsLoading}
        <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
      {:else}
        <!-- Grouped by repository : one row per (registry, repo).
             Click a row to open the per-tag detail drawer.
             Dropping the Tag column avoids duplicate-repo spam — a
             5-tag image showed up as 5 rows before. -->
        <div class="overflow-x-auto rounded-box border border-base-300 bg-base-100">
          <table class="table table-zebra table-sm">
            <thead>
              <tr>
                <th>Repository</th>
                <th>Type</th>
                <th>Architectures</th>
                <th>Tags</th>
                <th>Size</th>
                <th>Last pushed</th>
              </tr>
            </thead>
            <tbody>
              {#if groupedArtifacts.length === 0}
                <tr><td colspan="6" class="py-8 text-center text-base-content/50">
                  {artifactsForSelected.length === 0 && !artifactQuery
                    ? `No artifacts in ${selected.name}.`
                    : 'No repositories match the filter.'}
                </td></tr>
              {:else}
                {#each groupedArtifacts as g (g.repository)}
                  <tr class="hover cursor-pointer"
                    class:bg-primary={selectedRepoName === g.repository}
                    class:text-primary-content={selectedRepoName === g.repository}
                    onclick={() => clickRepoRow(g)}>
                    <td class="font-mono">{g.repository}</td>
                    <td><span class="badge badge-sm {typeBadge(g.type)}">{g.type}</span></td>
                    <td class="text-xs">{g.arches}</td>
                    <td class="font-mono text-xs tabular-nums">{g.tagCount}</td>
                    <td class="font-mono text-xs">{g.sizeSummary}</td>
                    <td class="text-xs text-base-content/70">{g.lastPushed}</td>
                  </tr>
                {/each}
              {/if}
            </tbody>
          </table>
        </div>

        <div class="mt-2 flex items-center text-xs text-base-content/70">
          <span class="tabular-nums">
            <span class="font-medium text-base-content">{groupedArtifacts.length}</span>
            {groupedArtifacts.length === 1 ? 'repository' : 'repositories'}
            ·
            <span class="font-medium text-base-content">{artifactsForSelected.length}</span>
            {artifactsForSelected.length === 1 ? 'tag total' : 'tags total'}
          </span>
          <button class="ml-auto btn btn-ghost btn-xs gap-1" onclick={refresh}
            title="Reload artifacts" aria-label="Reload">
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 12a9 9 0 0 1 15.5-6.3L21 8" />
              <path d="M21 3v5h-5" />
              <path d="M21 12a9 9 0 0 1-15.5 6.3L3 16" />
              <path d="M3 21v-5h5" />
            </svg>
            Reload
          </button>
        </div>
      {/if}
    {/if}
  </section>
</div>

{#if drawerRepo && selected}
  <ArtifactRepoDrawer
    registry={selected.name}
    repository={drawerRepo.repository}
    artifacts={drawerRepo.tags}
    onClose={closeRepoDrawer}
  />
{/if}

<!-- Remote create / edit modal -->
<dialog class="modal" bind:this={remoteDlg}>
  <div class="modal-box max-w-lg">
    <h3 class="text-lg font-bold">{remoteMode === 'create' ? 'Add remote registry' : 'Edit remote registry'}</h3>
    <p class="mt-1 text-sm text-base-content/60">
      Proxy = pull-through cache (local serves the upstream). Replica = push mirror (every local push fans out to the remote).
    </p>

    <form class="mt-4 flex flex-col gap-3" onsubmit={submitRemote}>
      <div class="grid grid-cols-2 gap-3">
        <label class="form-control">
          <span class="label-text mb-1 text-xs">Name</span>
          <input class="input input-sm input-bordered font-mono" placeholder="docker-hub"
            disabled={remoteMode === 'edit'} bind:value={remoteName} />
        </label>
        <label class="form-control">
          <span class="label-text mb-1 text-xs">Kind</span>
          <select class="select select-sm select-bordered" bind:value={remoteKind}>
            <option value="proxy">proxy (pull-through cache)</option>
            <option value="replica">replica (push mirror)</option>
          </select>
        </label>
      </div>

      <label class="form-control">
        <span class="label-text mb-1 text-xs">URL</span>
        <input class="input input-sm input-bordered" placeholder="https://registry-1.docker.io" bind:value={remoteUrl} />
      </label>

      <label class="form-control">
        <span class="label-text mb-1 text-xs">Username (optional)</span>
        <input class="input input-sm input-bordered" placeholder="leave empty for public" bind:value={remoteUsername} />
      </label>

      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" class="toggle toggle-sm" bind:checked={remoteEnabled} />
        Enabled
      </label>

      {#if remoteFormErr}<div class="alert alert-error py-2 text-sm">{remoteFormErr}</div>{/if}

      <div class="modal-action mt-1">
        <button type="button" class="btn btn-sm btn-ghost" onclick={() => remoteDlg.close()}>Cancel</button>
        <button type="submit" class="btn btn-sm btn-primary" disabled={remoteFormBusy}>
          {#if remoteFormBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          {remoteMode === 'create' ? 'Add' : 'Save'}
        </button>
      </div>
    </form>
  </div>
  <form method="dialog" class="modal-backdrop"><button>close</button></form>
</dialog>

