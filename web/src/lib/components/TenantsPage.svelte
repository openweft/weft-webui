<script lang="ts">
  // TenantsPage : two layers in one component —
  //
  //   - list  : the tenants table + (cluster-admin) "Create tenant" CTA
  //   - detail: drill-down for a single tenant with Projects / Members /
  //             Groups, and the three tenant-admin actions
  //
  // The selected tenant lives in local state (no router change needed) ;
  // a back-button returns to the list.
  //
  // Affordances are gated by /api/tenants/{name}.caller — the server
  // already enforces it, this is just for UI clarity.
  import { onMount } from 'svelte';
  import {
    getRows,
    getMe,
    getTenant,
    createTenant,
    addTenantAdmin,
    addTenantProject,
    addTenantMember,
    grantProjectRole,
    getTenantQuota,
    setTenantQuota,
    getProjectQuota,
    setProjectQuota,
    QUOTA_DIMS,
    type ResourceMeta,
    type Row,
    type TenantDetail,
    type TenantQuotaView,
    type ProjectQuotaView,
    type Quotas,
  } from '../api';
  import ResourceTable from './ResourceTable.svelte';
  import QuotaBars from './QuotaBars.svelte';

  let { meta }: { meta: ResourceMeta } = $props();

  // ---------- list ----------
  let rows = $state<Row[]>([]);
  let loading = $state(true);
  let error = $state('');
  let selected = $state<string | null>(null);
  // canCreateTenant is driven by the user's role. The POST is only
  // wired on the admin listener — clicking from the user UI yields a
  // 404, which the modal surfaces as an error. Keeping the gate
  // role-based (not port-based) means a cluster admin sees the
  // affordance wherever they're working from.
  let canCreateTenant = $state(false);

  async function refreshList() {
    loading = true;
    try {
      rows = await getRows('tenants');
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  }
  onMount(refreshList);

  // Resolve the role flag once. cluster_admin == superadmin in /api/me's
  // vocabulary ; tenant-level admin is detected per-tenant via the
  // `caller` block returned by /api/tenants/{name}.
  onMount(async () => {
    try {
      const me = await getMe();
      canCreateTenant = me.cluster_admin;
    } catch { canCreateTenant = false; }
  });

  // ---------- detail ----------
  let detail = $state<TenantDetail | null>(null);
  let detailErr = $state('');

  async function openTenant(name: string) {
    selected = name;
    detail = null;
    detailErr = '';
    tenantQuota = null;
    try {
      detail = await getTenant(name);
    } catch (e) {
      detailErr = String(e);
    }
    refreshTenantQuota(name); // parallel with detail render, populates the card
  }
  async function refreshDetail() {
    if (selected) await openTenant(selected);
  }
  function closeDetail() {
    selected = null;
    detail = null;
    refreshList();
  }

  // Catch click on a row : ResourceTable renders the rows, we listen on
  // the wrapper.
  function onRowClick(e: MouseEvent) {
    const cell = (e.target as HTMLElement).closest('tr');
    if (!cell || !cell.dataset.name) return;
    openTenant(cell.dataset.name);
  }

  // ---------- modals ----------
  let createDlg: HTMLDialogElement;
  let createName = $state(''), createDomain = $state(''), createErr = $state('');

  let setAdminDlg: HTMLDialogElement;
  let setAdminEmail = $state(''), setAdminErr = $state('');

  let addProjectDlg: HTMLDialogElement;
  let addProjectName = $state(''), addProjectErr = $state('');

  let addMemberDlg: HTMLDialogElement;
  let addMemberEmail = $state('');
  let addMemberGroups = $state<string[]>(['developers']);
  let addMemberErr = $state('');

  let grantDlg: HTMLDialogElement;
  let grantProject = $state('');
  let grantEmail = $state('');
  let grantRole = $state('editor');
  let grantErr = $state('');

  // ---------- quotas ----------
  // tenantQuota is the read-only view rendered in the Quotas card.
  // Loaded on drill-down (alongside getTenant). projectQuotas keep the
  // per-project view available for the "Edit quotas" modal.
  let tenantQuota = $state<TenantQuotaView | null>(null);
  let projectQuotaCache = $state<Record<string, ProjectQuotaView>>({});

  async function refreshTenantQuota(name: string) {
    try { tenantQuota = await getTenantQuota(name); }
    catch { tenantQuota = null; }
  }

  let tenantQuotaDlg: HTMLDialogElement;
  let tenantQuotaDraft = $state<Quotas>(zeroQuotas());
  let tenantQuotaErr = $state('');

  let projectQuotaDlg: HTMLDialogElement;
  let projectQuotaProject = $state('');
  let projectQuotaView = $state<ProjectQuotaView | null>(null);
  let projectQuotaDraft = $state<Quotas>(zeroQuotas());
  let projectQuotaErr = $state('');

  function zeroQuotas(): Quotas {
    return {
      vcpu: 0, ram_gib: 0, volumes: 0, volumes_gib: 0, shares: 0, shares_gib: 0,
      buckets: 0, buckets_gib: 0, registry_gib: 0, floating_ips: 0, projects: 0,
    };
  }

  function openEditTenantQuota() {
    if (!tenantQuota) return;
    tenantQuotaDraft = { ...tenantQuota.cap };
    tenantQuotaErr = '';
    tenantQuotaDlg.showModal();
  }
  async function submitTenantQuota(e: SubmitEvent) {
    e.preventDefault();
    tenantQuotaErr = '';
    if (!selected) return;
    try {
      await setTenantQuota(selected, tenantQuotaDraft);
      tenantQuotaDlg.close();
      refreshTenantQuota(selected);
    } catch (err) { tenantQuotaErr = String(err); }
  }

  async function openEditProjectQuota(projectName: string) {
    projectQuotaProject = projectName;
    projectQuotaErr = '';
    try {
      const v = await getProjectQuota(projectName);
      projectQuotaCache[projectName] = v;
      projectQuotaView = v;
      projectQuotaDraft = { ...v.project };
      projectQuotaDlg.showModal();
    } catch (err) { projectQuotaErr = String(err); }
  }
  async function submitProjectQuota(e: SubmitEvent) {
    e.preventDefault();
    projectQuotaErr = '';
    try {
      const v = await setProjectQuota(projectQuotaProject, projectQuotaDraft);
      projectQuotaCache[projectQuotaProject] = v;
      projectQuotaView = v;
      projectQuotaDlg.close();
      if (selected) refreshTenantQuota(selected);
    } catch (err) { projectQuotaErr = String(err); }
  }

  // For the project modal : "what extra am I asking compared to the
  // current value?" — passed to QuotaBars as `extra` so the bar
  // overlays current siblings_total with the requested delta.
  function projectExtra(view: ProjectQuotaView | null, draft: Quotas): Partial<Quotas> {
    if (!view) return {};
    const out: Partial<Quotas> = {};
    (Object.keys(draft) as (keyof Quotas)[]).forEach((k) => {
      const delta = draft[k] - (view.project[k] ?? 0);
      if (delta > 0) (out as Record<string, number>)[k] = delta;
    });
    return out;
  }
  let projectDraftExtra = $derived(projectExtra(projectQuotaView, projectQuotaDraft));

  async function submitCreateTenant(e: SubmitEvent) {
    e.preventDefault();
    createErr = '';
    try {
      await createTenant(createName.trim(), createDomain.trim());
      createName = ''; createDomain = '';
      createDlg.close();
      refreshList();
    } catch (err) { createErr = String(err); }
  }
  async function submitSetAdmin(e: SubmitEvent) {
    e.preventDefault();
    setAdminErr = '';
    if (!selected) return;
    try {
      await addTenantAdmin(selected, setAdminEmail.trim());
      setAdminEmail = '';
      setAdminDlg.close();
      refreshDetail();
    } catch (err) { setAdminErr = String(err); }
  }
  async function submitAddProject(e: SubmitEvent) {
    e.preventDefault();
    addProjectErr = '';
    if (!selected) return;
    try {
      await addTenantProject(selected, addProjectName.trim());
      addProjectName = '';
      addProjectDlg.close();
      refreshDetail();
    } catch (err) { addProjectErr = String(err); }
  }
  async function submitAddMember(e: SubmitEvent) {
    e.preventDefault();
    addMemberErr = '';
    if (!selected) return;
    try {
      await addTenantMember(selected, addMemberEmail.trim(), addMemberGroups);
      addMemberEmail = '';
      addMemberGroups = ['developers'];
      addMemberDlg.close();
      refreshDetail();
    } catch (err) { addMemberErr = String(err); }
  }
  async function submitGrant(e: SubmitEvent) {
    e.preventDefault();
    grantErr = '';
    try {
      await grantProjectRole(grantProject, grantEmail.trim(), grantRole);
      grantEmail = '';
      grantDlg.close();
      refreshDetail();
    } catch (err) { grantErr = String(err); }
  }

  function openGrant(project: string) {
    grantProject = project; grantEmail = ''; grantRole = 'editor'; grantErr = '';
    grantDlg.showModal();
  }
  function toggleAddMemberGroup(g: string) {
    addMemberGroups = addMemberGroups.includes(g)
      ? addMemberGroups.filter(x => x !== g)
      : [...addMemberGroups, g];
  }
</script>

{#if !selected}
  <!-- List view -->
  <div class="flex items-center gap-3">
    <div>
      <h2 class="text-2xl font-bold">{meta.label}</h2>
      <p class="text-sm text-base-content/60">{rows.length} tenant{rows.length === 1 ? '' : 's'} · click to manage</p>
    </div>
    {#if canCreateTenant}
      <div class="ml-auto">
        <button class="btn btn-sm btn-primary" onclick={() => { createErr=''; createDlg.showModal(); }}>
          + Create tenant
        </button>
      </div>
    {/if}
  </div>

  <div class="mt-4">
    {#if loading}
      <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
    {:else if error}
      <div class="alert alert-error">{error}</div>
    {:else}
      <div role="presentation" onclick={onRowClick} class="cursor-pointer">
        <ResourceTable columns={meta.columns} rows={rows} />
      </div>
    {/if}
  </div>
{:else}
  <!-- Detail view -->
  <div class="flex items-center gap-3">
    <button class="btn btn-sm btn-ghost gap-1" onclick={closeDetail}>
      <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <path d="m15 18-6-6 6-6" />
      </svg>
      Tenants
    </button>
    <div>
      <h2 class="text-2xl font-bold">{selected}</h2>
      {#if detail}
        <p class="text-sm text-base-content/60">
          {detail.domain} · status {detail.status} · {detail.projects.length} projects · {detail.members.length} members
        </p>
      {/if}
    </div>
    {#if detail?.caller.cluster_admin}
      <div class="ml-auto">
        <button class="btn btn-sm btn-outline" onclick={() => { setAdminErr=''; setAdminDlg.showModal(); }}>
          Set tenant admin
        </button>
      </div>
    {/if}
  </div>

  {#if !detail && !detailErr}
    <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
  {:else if detailErr}
    <div class="mt-4 alert alert-error">{detailErr}</div>
  {:else if detail}
    <div class="mt-6 grid gap-6 lg:grid-cols-3">
      <!-- Projects -->
      <section class="lg:col-span-2 card bg-base-100 shadow">
        <div class="card-body p-4">
          <div class="flex items-center">
            <h3 class="card-title text-base">Projects</h3>
            {#if detail.caller.tenant_admin}
              <button class="ml-auto btn btn-xs btn-primary"
                onclick={() => { addProjectErr=''; addProjectDlg.showModal(); }}>+ project</button>
            {/if}
          </div>
          {#if detail.projects.length === 0}
            <p class="text-sm text-base-content/60">No projects yet.</p>
          {:else}
            <ul class="divide-y divide-base-300">
              {#each detail.projects as p (p.uuid)}
                <li class="py-2">
                  <div class="flex items-center">
                    <div>
                      <div class="font-medium">{p.name}</div>
                      <div class="text-xs text-base-content/50 font-mono">{p.uuid}</div>
                    </div>
                    {#if detail.caller.tenant_admin}
                      <div class="ml-auto flex gap-1">
                        <button class="btn btn-xs btn-ghost" onclick={() => openEditProjectQuota(p.name)}>
                          Quotas
                        </button>
                        <button class="btn btn-xs btn-ghost" onclick={() => openGrant(p.name)}>
                          Grant role
                        </button>
                      </div>
                    {/if}
                  </div>
                  {#if Object.keys(p.roles).length > 0}
                    <div class="mt-1 flex flex-wrap gap-1">
                      {#each Object.entries(p.roles) as [email, role] (email)}
                        <span class="badge badge-sm badge-ghost">{email}:{role}</span>
                      {/each}
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      </section>

      <!-- Members -->
      <section class="card bg-base-100 shadow">
        <div class="card-body p-4">
          <div class="flex items-center">
            <h3 class="card-title text-base">Members</h3>
            {#if detail.caller.tenant_admin}
              <button class="ml-auto btn btn-xs btn-primary"
                onclick={() => { addMemberErr=''; addMemberDlg.showModal(); }}>+ member</button>
            {/if}
          </div>
          <ul class="divide-y divide-base-300">
            {#each detail.members as m (m.email)}
              <li class="py-2 flex items-baseline gap-2">
                <span class="font-medium">{m.name}</span>
                {#if m.admin}<span class="badge badge-error badge-xs">admin</span>{/if}
                <span class="ml-auto text-xs text-base-content/60">{m.groups.join(', ')}</span>
              </li>
            {/each}
          </ul>
        </div>
      </section>

      <!-- Quotas (tenant cap + sum-of-projects bars). Edit gated to
           cluster admin (the only persona allowed to bump the cap). -->
      <section class="lg:col-span-3 card bg-base-100 shadow">
        <div class="card-body p-4">
          <div class="flex items-center">
            <h3 class="card-title text-base">Quotas</h3>
            <span class="ml-2 text-xs text-base-content/60">
              tenant cap · used by projects · remaining
            </span>
            {#if detail.caller.cluster_admin}
              <button class="ml-auto btn btn-xs btn-outline"
                onclick={openEditTenantQuota} disabled={!tenantQuota}>
                Edit cap
              </button>
            {/if}
          </div>
          {#if !tenantQuota}
            <div class="py-4 text-center text-base-content/50 text-sm">loading…</div>
          {:else}
            <QuotaBars bars={tenantQuota.remaining} />
          {/if}
        </div>
      </section>

      <!-- Groups -->
      <section class="lg:col-span-3 card bg-base-100 shadow">
        <div class="card-body p-4">
          <h3 class="card-title text-base">Groups</h3>
          <div class="grid gap-2 sm:grid-cols-3">
            {#each detail.groups as g (g.name)}
              <div class="rounded-box border border-base-300 p-3">
                <div class="font-medium">{g.name}</div>
                <div class="text-xs text-base-content/60">{g.description}</div>
              </div>
            {/each}
          </div>
        </div>
      </section>
    </div>
  {/if}
{/if}

<!-- ----- Modals ----- -->

<dialog class="modal" bind:this={createDlg}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submitCreateTenant}>
    <h3 class="text-lg font-bold">Create tenant</h3>
    <p class="text-sm text-base-content/60">Cluster-admin only. After creation, designate at least one tenant admin via Set tenant admin.</p>
    <label class="form-control mt-3">
      <span class="label-text text-xs">Name</span>
      <input class="input input-sm input-bordered" placeholder="acme" bind:value={createName} required />
    </label>
    <label class="form-control mt-2">
      <span class="label-text text-xs">Domain</span>
      <input class="input input-sm input-bordered" placeholder="acme.example" bind:value={createDomain} />
    </label>
    {#if createErr}<div class="mt-2 alert alert-error py-2 text-sm">{createErr}</div>{/if}
    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => createDlg.close()}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary">Create</button>
    </div>
  </form>
</dialog>

<dialog class="modal" bind:this={setAdminDlg}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submitSetAdmin}>
    <h3 class="text-lg font-bold">Set tenant admin</h3>
    <p class="text-sm text-base-content/60">
      Promote a user into <span class="font-mono">{selected}</span>'s admin group.
      They'll be able to add projects, add members, and grant roles within this tenant.
    </p>
    <label class="form-control mt-3">
      <span class="label-text text-xs">User email</span>
      <input class="input input-sm input-bordered" placeholder="someone@example.com"
        type="email" bind:value={setAdminEmail} required />
    </label>
    {#if setAdminErr}<div class="mt-2 alert alert-error py-2 text-sm">{setAdminErr}</div>{/if}
    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => setAdminDlg.close()}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary">Promote</button>
    </div>
  </form>
</dialog>

<dialog class="modal" bind:this={addProjectDlg}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submitAddProject}>
    <h3 class="text-lg font-bold">Create project</h3>
    <p class="text-sm text-base-content/60">In tenant <span class="font-mono">{selected}</span>.</p>
    <label class="form-control mt-3">
      <span class="label-text text-xs">Project name</span>
      <input class="input input-sm input-bordered" placeholder="my-project" bind:value={addProjectName} required />
    </label>
    {#if addProjectErr}<div class="mt-2 alert alert-error py-2 text-sm">{addProjectErr}</div>{/if}
    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => addProjectDlg.close()}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary">Create</button>
    </div>
  </form>
</dialog>

<dialog class="modal" bind:this={addMemberDlg}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submitAddMember}>
    <h3 class="text-lg font-bold">Add member</h3>
    <p class="text-sm text-base-content/60">In tenant <span class="font-mono">{selected}</span>.</p>
    <label class="form-control mt-3">
      <span class="label-text text-xs">User email</span>
      <input class="input input-sm input-bordered" type="email"
        placeholder="someone@example.com" bind:value={addMemberEmail} required />
    </label>
    <div class="mt-3">
      <span class="label-text text-xs">Groups</span>
      <div class="mt-1 flex flex-wrap gap-3">
        {#each (detail?.groups ?? []) as g (g.name)}
          <label class="flex cursor-pointer items-center gap-2">
            <input type="checkbox" class="checkbox checkbox-sm checkbox-primary"
              checked={addMemberGroups.includes(g.name)}
              onchange={() => toggleAddMemberGroup(g.name)} />
            <span class="text-sm">{g.name}</span>
          </label>
        {/each}
      </div>
    </div>
    {#if addMemberErr}<div class="mt-2 alert alert-error py-2 text-sm">{addMemberErr}</div>{/if}
    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => addMemberDlg.close()}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary">Add</button>
    </div>
  </form>
</dialog>

<dialog class="modal" bind:this={grantDlg}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submitGrant}>
    <h3 class="text-lg font-bold">Grant role on {grantProject}</h3>
    <label class="form-control mt-3">
      <span class="label-text text-xs">User email</span>
      <input class="input input-sm input-bordered" type="email"
        placeholder="someone@example.com" bind:value={grantEmail} required />
    </label>
    <label class="form-control mt-2">
      <span class="label-text text-xs">Role</span>
      <select class="select select-sm select-bordered" bind:value={grantRole}>
        <option value="owner">owner</option>
        <option value="editor">editor</option>
        <option value="viewer">viewer</option>
      </select>
    </label>
    {#if grantErr}<div class="mt-2 alert alert-error py-2 text-sm">{grantErr}</div>{/if}
    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => grantDlg.close()}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary">Grant</button>
    </div>
  </form>
</dialog>

<!-- Tenant quota cap edit (cluster admin). Plain number inputs : the
     cap is open-ended ; the only constraint is "≥ current allocated"
     which the server enforces. -->
<dialog class="modal" bind:this={tenantQuotaDlg}>
  <form method="dialog" class="modal-box max-w-2xl" onsubmit={submitTenantQuota}>
    <h3 class="text-lg font-bold">Edit tenant cap — {selected}</h3>
    <p class="text-sm text-base-content/60">
      Sets the hard upper bound that the sum of all project quotas may consume.
      Cannot be lowered below current allocation ; shrink the projects first.
    </p>
    <div class="mt-4 grid gap-2 sm:grid-cols-2">
      {#each QUOTA_DIMS as d (d.key)}
        <label class="form-control">
          <span class="label-text text-xs">{d.label}{d.unit ? ' (' + d.unit + ')' : ''}</span>
          <input type="number" min="0" class="input input-sm input-bordered tabular-nums"
            bind:value={tenantQuotaDraft[d.key]} />
        </label>
      {/each}
    </div>
    {#if tenantQuotaErr}<div class="mt-3 alert alert-error py-2 text-sm">{tenantQuotaErr}</div>{/if}
    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => tenantQuotaDlg.close()}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary">Save</button>
    </div>
  </form>
</dialog>

<!-- Project quota edit (tenant admin). Numeric inputs + a live
     "tenant remaining" overlay : the bar shows current sibling usage
     and overlays the requested delta in lighter text. Bars switch
     red when total exceeds cap ; server still validates on submit. -->
<dialog class="modal" bind:this={projectQuotaDlg}>
  <form method="dialog" class="modal-box max-w-3xl" onsubmit={submitProjectQuota}>
    <h3 class="text-lg font-bold">Project quotas — {projectQuotaProject}</h3>
    {#if projectQuotaView}
      <p class="text-sm text-base-content/60">
        Stays within the tenant cap below. The lighter "+ N" is what your draft adds on top of the other projects.
      </p>

      <div class="mt-3 rounded-box border border-base-300 bg-base-200/40 p-3">
        <div class="text-xs font-medium text-base-content/70 mb-1">Tenant remaining (siblings + this draft)</div>
        {#key projectDraftExtra}
          <QuotaBars
            bars={projectQuotaView.tenant_remaining}
            extra={projectExtra(projectQuotaView, projectQuotaDraft)}
            omit={['projects']}
            pulseOver={true}
          />
        {/key}
      </div>

      <div class="mt-4 grid gap-2 sm:grid-cols-2">
        {#each QUOTA_DIMS as d (d.key)}
          {#if !d.tenantOnly}
            <label class="form-control">
              <span class="label-text text-xs">{d.label}{d.unit ? ' (' + d.unit + ')' : ''}</span>
              <input type="number" min="0" class="input input-sm input-bordered tabular-nums"
                bind:value={projectQuotaDraft[d.key]} />
            </label>
          {/if}
        {/each}
      </div>
      {#if projectQuotaErr}<div class="mt-3 alert alert-error py-2 text-sm">{projectQuotaErr}</div>{/if}
    {/if}
    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => projectQuotaDlg.close()}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary">Save</button>
    </div>
  </form>
</dialog>
