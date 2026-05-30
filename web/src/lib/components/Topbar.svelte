<script lang="ts">
  import { getMe, setScope, logout, onAdminUI, type Me, type ScopeEntry } from '../api';
  import HelpModal from './HelpModal.svelte';

  let helpOpen = $state(false);

  let { title }: { title: string } = $props();

  // DaisyUI theme via data-theme on <html>. Persisted in localStorage.
  let theme = $state<'light' | 'dark'>(
    (localStorage.getItem('weft-theme') as 'light' | 'dark') ?? 'light',
  );

  $effect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('weft-theme', theme);
  });

  let me = $state<Me | null>(null);
  let adminUI = $state(false);          // which listener served us

  // Selected scope mirrors me.tenant / me.project once /api/me lands.
  // Empty strings are intentional :
  //   tenant=""             → "(all tenants)" — cluster admin only
  //   tenant set, project="" → tenant-aggregate
  //   both set              → project-scoped
  let tenant = $state('');
  let project = $state('');

  $effect(() => {
    getMe()
      .then((u) => {
        me = u;
        tenant = u.tenant;
        project = u.project;
      })
      .catch(() => { /* api.ts already triggered the login redirect */ });
    onAdminUI().then((v) => { adminUI = v; });
  });

  // The user's tenants from /api/me. Cluster admins additionally see
  // "(all)" so they can keep their global view.
  let tenantOptions = $derived<{ value: string; label: string }[]>(
    [
      ...(adminUI || me?.cluster_admin ? [{ value: '', label: '(all tenants)' }] : []),
      ...(me?.scopes ?? []).map((s) => ({ value: s.name, label: s.name })),
    ],
  );

  // Projects for the selected tenant, plus an "(all projects)" entry
  // (tenant-aggregate view). Empty when no tenant is selected.
  let projectOptions = $derived.by<{ value: string; label: string }[]>(() => {
    if (!tenant) return [];
    const scope = me?.scopes.find((s) => s.name === tenant);
    if (!scope) return [];
    return [
      { value: '', label: '(all projects)' },
      ...scope.projects.map((p) => ({ value: p, label: p })),
    ];
  });

  async function chooseTenant(t: string) {
    tenant = t;
    // Picking a different tenant invalidates the project (unless empty,
    // meaning "all tenants" where project must also be empty).
    project = '';
    if (me?.dev) return; // dev mode : no persistent cookie
    try { await setScope(tenant, project); } catch { /* surface elsewhere */ }
  }
  async function chooseProject(p: string) {
    project = p;
    if (me?.dev) return;
    try { await setScope(tenant, project); } catch { /* surface elsewhere */ }
  }

  // Badge = persona-then-role.
  let badge = $derived(
    adminUI ? 'superadmin'
      : me?.cluster_admin || me?.tenant_admin ? 'admin'
      : null,
  );

  $effect(() => {
    document.title = badge ? `Weft · ${badge}` : 'Weft';
  });
</script>

<header class="flex h-16 shrink-0 items-center gap-3 border-b border-base-300 bg-base-100 px-6">
  <h1 class="text-base font-semibold">{title}</h1>

  {#if badge === 'superadmin'}
    <span class="badge badge-error badge-sm uppercase tracking-wide" title="Cluster-wide UI">superadmin</span>
  {:else if badge === 'admin'}
    <span class="badge badge-warning badge-sm uppercase tracking-wide" title="Tenant administrator">admin</span>
  {/if}
  {#if me?.dev}
    <span class="badge badge-info badge-sm">dev</span>
  {/if}

  <div class="ml-auto flex items-center gap-2">
    <!-- Search hint : ⌘K opens the SearchPalette (registered in App.svelte). -->
    <button class="hidden items-center gap-2 rounded-box border border-base-300 px-2 py-1 text-xs text-base-content/50 hover:text-base-content sm:flex"
      onclick={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))}
      title="Search (⌘K)"
    >
      <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3-3" stroke-linecap="round" />
      </svg>
      <span>Search</span>
      <kbd class="kbd kbd-xs">⌘K</kbd>
    </button>

    <!-- Tenant selector (cascades into project). -->
    <div class="dropdown dropdown-end">
      <div tabindex="0" role="button" class="btn btn-sm btn-ghost gap-1">
        <span class="text-base-content/60">tenant:</span>
        <span class="font-medium">{tenant || (tenantOptions[0]?.label ?? '—')}</span>
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
          <path d="m6 9 6 6 6-6" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </div>
      <ul class="menu dropdown-content z-10 mt-2 w-52 rounded-box bg-base-100 p-2 shadow">
        {#each tenantOptions as opt (opt.value)}
          <li>
            <button class:menu-active={opt.value === tenant}
              onclick={() => chooseTenant(opt.value)}>{opt.label}</button>
          </li>
        {/each}
        {#if tenantOptions.length === 0}
          <li class="disabled px-2 py-1 text-xs text-base-content/50">no tenants</li>
        {/if}
      </ul>
    </div>

    <!-- Project selector (disabled when tenant is empty). -->
    <div class="dropdown dropdown-end">
      <div tabindex="0" role="button"
        class="btn btn-sm btn-ghost gap-1"
        class:btn-disabled={!tenant}
        aria-disabled={!tenant}
      >
        <span class="text-base-content/60">project:</span>
        <span class="font-medium">{project || (tenant ? '(all)' : '—')}</span>
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
          <path d="m6 9 6 6 6-6" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </div>
      {#if tenant}
        <ul class="menu dropdown-content z-10 mt-2 w-52 rounded-box bg-base-100 p-2 shadow">
          {#each projectOptions as opt (opt.value)}
            <li>
              <button class:menu-active={opt.value === project}
                onclick={() => chooseProject(opt.value)}>{opt.label}</button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    <!-- Help / shortcuts -->
    <button
      class="btn btn-sm btn-ghost btn-circle"
      aria-label="Help"
      title="Help (?)"
      onclick={() => (helpOpen = true)}
    >
      <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="9" />
        <path d="M9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.7.4-1 1-1 1.7v.5" />
        <circle cx="12" cy="17" r="0.5" fill="currentColor" stroke="none" />
      </svg>
    </button>

    <!-- Theme toggle -->
    <button
      class="btn btn-sm btn-ghost btn-circle"
      aria-label="Toggle theme"
      onclick={() => (theme = theme === 'light' ? 'dark' : 'light')}
    >
      {#if theme === 'light'}
        <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z" />
        </svg>
      {:else}
        <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M19.1 4.9l-1.4 1.4M6.3 17.7l-1.4 1.4" />
        </svg>
      {/if}
    </button>

    <!-- User menu -->
    <div class="dropdown dropdown-end">
      <div tabindex="0" role="button" class="btn btn-sm btn-ghost gap-2">
        <div class="avatar avatar-placeholder">
          <div class="w-7 rounded-full bg-neutral text-neutral-content">
            <span class="text-xs">{me?.initials ?? '··'}</span>
          </div>
        </div>
        <span class="hidden sm:inline">{me?.name || me?.email || '…'}</span>
      </div>
      <ul class="menu dropdown-content z-10 mt-2 w-52 rounded-box bg-base-100 p-2 shadow">
        {#if me}
          <li class="menu-title text-xs opacity-60">
            <span>{me.email || me.sub}</span>
          </li>
        {/if}
        <li><a href="#/users">Profile</a></li>
        <li><button onclick={logout}>Sign out</button></li>
      </ul>
    </div>
  </div>
</header>

<HelpModal bind:open={helpOpen} />
