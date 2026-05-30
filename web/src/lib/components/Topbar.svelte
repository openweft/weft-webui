<script lang="ts">
  let { title }: { title: string } = $props();

  // DaisyUI theme via data-theme on <html>. Persisted in localStorage.
  let theme = $state<'light' | 'dark'>(
    (localStorage.getItem('weft-theme') as 'light' | 'dark') ?? 'light',
  );

  $effect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('weft-theme', theme);
  });

  const projects = ['team-alpha', 'team-beta', 'research'];
  let project = $state(projects[0]);
</script>

<header class="flex h-16 shrink-0 items-center gap-3 border-b border-base-300 bg-base-100 px-6">
  <h1 class="text-base font-semibold">{title}</h1>

  <div class="ml-auto flex items-center gap-2">
    <!-- Project / tenant scope -->
    <div class="dropdown dropdown-end">
      <div tabindex="0" role="button" class="btn btn-sm btn-ghost gap-1">
        <span class="text-base-content/60">project:</span>
        <span class="font-medium">{project}</span>
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
          <path d="m6 9 6 6 6-6" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </div>
      <ul class="menu dropdown-content z-10 mt-2 w-44 rounded-box bg-base-100 p-2 shadow">
        {#each projects as p (p)}
          <li><button class:menu-active={p === project} onclick={() => (project = p)}>{p}</button></li>
        {/each}
      </ul>
    </div>

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
            <span class="text-xs">YF</span>
          </div>
        </div>
        <span class="hidden sm:inline">yann</span>
      </div>
      <ul class="menu dropdown-content z-10 mt-2 w-44 rounded-box bg-base-100 p-2 shadow">
        <li><a href="#/users">Profile</a></li>
        <li><a href="#/">API tokens</a></li>
        <li><a href="#/">Sign out</a></li>
      </ul>
    </div>
  </div>
</header>
