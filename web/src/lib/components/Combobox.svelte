<script lang="ts" generics="T">
  // Combobox.svelte — generic filter-as-you-type picker, designed to
  // replace plain <select>s wherever the source list outgrows the
  // dozen-entry comfort zone.
  //
  // Design notes :
  //  - Native <input> + a floating <ul> ; no third-party JS, no
  //    portal magic. The dropdown sits absolutely-positioned below
  //    the input so the parent doesn't need overflow:visible.
  //  - Filter matches the label AND the optional sub-label (so a
  //    flavor named "small" still ranks when the user types "vcpu 2").
  //  - Optgroups when getGroup is provided and yields ≥ 2 distinct
  //    groups — same scaling story as native <select>.
  //  - Keyboard : ↓/↑ navigate, Enter picks, Esc closes. No focus-trap
  //    drama — the input keeps focus while the dropdown is open.
  //  - onmousedown + preventDefault on list rows : clicking an item
  //    doesn't blur the input, so we control the open/close cycle.
  //  - The bound `value` is the *id* (string) returned by getId, not
  //    the object. Lets callers stay key-based across rerenders.

  let {
    items,
    value = $bindable(''),
    getId,
    getLabel,
    getSub,
    getGroup,
    placeholder = '',
    disabled = false,
    size = 'sm',
  }: {
    items: T[];
    value?: string;
    getId: (item: T) => string;
    getLabel: (item: T) => string;
    getSub?: (item: T) => string;
    getGroup?: (item: T) => string;
    placeholder?: string;
    disabled?: boolean;
    /** matches DaisyUI's input-* / select-* size tokens */
    size?: 'xs' | 'sm' | 'md';
  } = $props();

  // q is the live filter text. When the dropdown is closed, the input
  // shows the selected item's label (sync'd via the $effect below) so
  // it reads like a populated <select>. When open, q reflects whatever
  // the user has typed.
  let q = $state('');
  let open = $state(false);
  let active = $state(0);

  // Unique listbox id so aria-controls on the input can point at it.
  // Math.random is fine for an in-DOM identifier — no security need.
  const listboxId = `cb-${Math.random().toString(36).slice(2, 10)}`;

  let selected = $derived(items.find((i) => getId(i) === value) ?? null);

  // Sync the input text to the selected label whenever the dropdown
  // closes — handles both "user picked an item" and "user blurred
  // without picking". When opening (via focus), clear q so the user
  // can search from a blank slate ; if they want to keep the current
  // label they can re-pick it.
  $effect(() => {
    if (open) return;
    q = selected ? getLabel(selected) : '';
  });

  let matches = $derived.by<T[]>(() => {
    const needle = q.trim().toLowerCase();
    if (!needle || (selected && q === getLabel(selected))) {
      // Either nothing typed, or q still matches the selected label
      // verbatim (just opened the dropdown) — show everything.
      return items;
    }
    return items.filter((it) => {
      const l = getLabel(it).toLowerCase();
      const s = getSub ? getSub(it).toLowerCase() : '';
      return l.includes(needle) || s.includes(needle);
    });
  });

  // Group split is only honoured when getGroup is set AND would yield
  // ≥ 2 distinct groups — a single-group catalogue stays flat.
  let groups = $derived.by<Array<[string, T[]]>>(() => {
    if (!getGroup) return [];
    const m = new Map<string, T[]>();
    for (const it of matches) {
      const g = getGroup(it);
      if (!m.has(g)) m.set(g, []);
      m.get(g)!.push(it);
    }
    return m.size > 1 ? [...m] : [];
  });

  // Reset the keyboard cursor whenever the matches change so we don't
  // point past the end of the list.
  $effect(() => {
    matches; // dependency
    active = 0;
  });

  function pick(it: T) {
    value = getId(it);
    open = false;
  }

  function onFocus() {
    open = true;
    q = ''; // fresh search ; the input still LOOKS empty for a beat,
            // matching "click select → type to filter" muscle memory.
  }

  // Blur is debounced so a mousedown on a list item lands before we
  // close. The list items also call preventDefault on mousedown to
  // keep focus on the input, but Tab/click-outside still rely on this
  // path.
  let blurTimer: ReturnType<typeof setTimeout> | null = null;
  function onBlur() {
    blurTimer = setTimeout(() => { open = false; }, 120);
  }
  function cancelBlur() {
    if (blurTimer) { clearTimeout(blurTimer); blurTimer = null; }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      open = true;
      active = Math.min(active + 1, Math.max(matches.length - 1, 0));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      active = Math.max(active - 1, 0);
    } else if (e.key === 'Enter') {
      if (open && matches[active]) {
        e.preventDefault();
        pick(matches[active]);
      }
    } else if (e.key === 'Escape') {
      open = false;
      (e.currentTarget as HTMLInputElement).blur();
    }
  }
</script>

<div class="relative">
  <input
    class="input input-{size} input-bordered w-full"
    type="text"
    role="combobox"
    aria-controls={listboxId}
    aria-expanded={open}
    aria-autocomplete="list"
    autocomplete="off"
    {placeholder}
    {disabled}
    bind:value={q}
    onfocus={onFocus}
    onblur={onBlur}
    onkeydown={onKey}
  />
  {#if open && !disabled}
    <ul
      id={listboxId}
      class="absolute z-20 mt-1 max-h-72 w-full overflow-auto rounded-box border border-base-300 bg-base-100 shadow-lg"
      onmousedown={cancelBlur}
      role="listbox"
    >
      {#if matches.length === 0}
        <li class="px-3 py-2 text-xs text-base-content/50">No match.</li>
      {:else if groups.length > 0}
        {#each groups as [gname, arr] (gname)}
          <li class="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wider text-base-content/40">
            {gname}
          </li>
          {#each arr as it (getId(it))}
            <li>
              <button
                type="button"
                class="block w-full px-3 py-1.5 text-left hover:bg-base-200"
                class:bg-base-200={getId(it) === value}
                onmousedown={(e) => { e.preventDefault(); pick(it); }}
              >
                <div class="text-sm font-medium">{getLabel(it)}</div>
                {#if getSub}
                  <div class="text-xs text-base-content/60">{getSub(it)}</div>
                {/if}
              </button>
            </li>
          {/each}
        {/each}
      {:else}
        {#each matches as it, i (getId(it))}
          <li>
            <button
              type="button"
              class="block w-full px-3 py-1.5 text-left hover:bg-base-200"
              class:bg-base-200={getId(it) === value || i === active}
              onmousedown={(e) => { e.preventDefault(); pick(it); }}
            >
              <div class="text-sm font-medium">{getLabel(it)}</div>
              {#if getSub}
                <div class="text-xs text-base-content/60">{getSub(it)}</div>
              {/if}
            </button>
          </li>
        {/each}
      {/if}
    </ul>
  {/if}
</div>
