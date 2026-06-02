<script lang="ts">
  // PluginInstallDrawer — right-side drawer that generates an install
  // form from a catalogue entry's inputs[] schema, validates required
  // fields, and POSTs to /api/plugins/install. The returned instance
  // UUID is displayed back to the operator with a copy-to-clipboard
  // affordance so they can reference it in `weft plugin status <uuid>`.
  //
  // Input types map to HTML inputs as follows :
  //   - string  → <input type="text">
  //   - number  → <input type="number">
  //   - bool    → <input type="checkbox">
  //   - secret  → <input type="password">
  //
  // Validation : required inputs must be non-empty before the submit
  // button enables. Numbers must parse cleanly. The server runs the
  // same required-check as a defence in depth.
  import { installPluginWithInputs, type PluginCatalogueEntry } from '../api';

  let {
    entry,
    onClose,
    onInstalled,
  }: {
    entry: PluginCatalogueEntry;
    onClose: () => void;
    onInstalled: (uuid: string) => void;
  } = $props();

  // Form state — keyed by input name. Booleans are stored as the
  // literal strings "true" / "false" because the wire shape is
  // map[string]string (the agent decodes per-input by Type).
  let values = $state<Record<string, string>>(initValues());
  let project = $state('default');
  let busy = $state(false);
  let submitErr = $state('');
  let resultUUID = $state('');

  function initValues(): Record<string, string> {
    const v: Record<string, string> = {};
    for (const i of entry.inputs ?? []) {
      if (i.type === 'secret') {
        v[i.name] = '';
      } else if (i.type === 'bool') {
        v[i.name] = i.default === 'true' ? 'true' : 'false';
      } else {
        v[i.name] = i.default ?? '';
      }
    }
    return v;
  }

  // Per-field validation : empty-when-required. Numbers parse-cleanly
  // is checked at submit time so the operator isn't bombarded with
  // errors while typing.
  let missingRequired = $derived.by(() => {
    const missing: string[] = [];
    for (const i of entry.inputs ?? []) {
      if (!i.required) continue;
      const v = values[i.name];
      if (v === undefined || v === null) {
        missing.push(i.name);
        continue;
      }
      if (i.type === 'bool') continue; // checkboxes are never "missing"
      if (String(v).trim() === '') missing.push(i.name);
    }
    return missing;
  });

  let projectMissing = $derived(project.trim() === '');
  let canSubmit = $derived(!busy && !projectMissing && missingRequired.length === 0 && !resultUUID);

  async function submit() {
    submitErr = '';
    // Re-check number parses at submit time.
    for (const i of entry.inputs ?? []) {
      if (i.type !== 'number') continue;
      const v = values[i.name];
      if (v === undefined || v === '') continue; // empty + non-required is fine
      if (Number.isNaN(Number(v))) {
        submitErr = `Input "${i.label || i.name}" must be a number.`;
        return;
      }
    }
    busy = true;
    try {
      const uuid = await installPluginWithInputs(entry.name, project.trim(), { ...values });
      resultUUID = uuid;
      // Hold the drawer open with the success banner ; the operator
      // copies the UUID, then dismisses via the Close button which
      // fires onInstalled to refresh the parent.
    } catch (e) {
      submitErr = String(e);
    } finally {
      busy = false;
    }
  }

  function dismiss() {
    if (resultUUID) {
      onInstalled(resultUUID);
    } else {
      onClose();
    }
  }

  async function copyUUID() {
    if (!resultUUID) return;
    try {
      await navigator.clipboard.writeText(resultUUID);
    } catch {
      // Clipboard may be denied in dev / iframe ; the operator can
      // still select-copy the badge text.
    }
  }
</script>

<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={dismiss}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col bg-base-100 shadow-2xl">
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div class="min-w-0">
      <h2 class="truncate text-lg font-bold">Install · {entry.name}</h2>
      <p class="truncate text-xs text-base-content/60">
        <span class="badge badge-xs badge-ghost">{entry.kind}</span>
        · {(entry.inputs?.length ?? 0)} input{(entry.inputs?.length ?? 0) === 1 ? '' : 's'}
      </p>
    </div>
    <button class="btn btn-ghost btn-sm ml-auto" onclick={dismiss} aria-label="Close">
      <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M6 6l12 12M6 18L18 6" stroke-linecap="round" />
      </svg>
    </button>
  </header>

  <div class="flex-1 overflow-y-auto px-5 py-4">
    <p class="text-sm text-base-content/70">{entry.description}</p>

    {#if resultUUID}
      <div class="alert alert-success mt-5">
        <div class="flex flex-col">
          <span class="font-semibold">Installed.</span>
          <span class="text-xs">Instance UUID :</span>
          <div class="mt-1 flex items-center gap-2">
            <span class="badge badge-lg font-mono">{resultUUID}</span>
            <button class="btn btn-ghost btn-xs" onclick={copyUUID} title="Copy UUID">copy</button>
          </div>
          <span class="mt-1 text-[10px] text-base-content/60">
            Reference this UUID in <span class="font-mono">weft plugin status {resultUUID}</span>.
          </span>
        </div>
      </div>
    {:else}
      <form class="mt-5 flex flex-col gap-4" onsubmit={(e) => { e.preventDefault(); void submit(); }}>
        <label class="form-control w-full">
          <div class="label py-1">
            <span class="label-text text-sm">Project<span class="text-error"> *</span></span>
          </div>
          <input type="text" class="input input-sm input-bordered"
            class:input-error={projectMissing}
            bind:value={project} placeholder="default" required />
          <div class="label py-1">
            <span class="label-text-alt text-xs text-base-content/50">
              Project the instance is installed under.
            </span>
          </div>
        </label>

        {#each entry.inputs ?? [] as input (input.name)}
          <div class="form-control w-full">
            <div class="label py-1">
              <span class="label-text text-sm">
                {input.label || input.name}
                {#if input.required}<span class="text-error"> *</span>{/if}
              </span>
              <span class="label-text-alt text-[10px] text-base-content/40">{input.type}</span>
            </div>
            {#if input.type === 'bool'}
              <label class="flex cursor-pointer items-center gap-2">
                <input type="checkbox" class="toggle toggle-sm"
                  checked={values[input.name] === 'true'}
                  onchange={(e) => (values[input.name] = (e.currentTarget as HTMLInputElement).checked ? 'true' : 'false')} />
                <span class="text-sm">{values[input.name] === 'true' ? 'enabled' : 'disabled'}</span>
              </label>
            {:else if input.type === 'secret'}
              <input type="password" class="input input-sm input-bordered"
                class:input-error={input.required && missingRequired.includes(input.name)}
                bind:value={values[input.name]}
                autocomplete="new-password" />
            {:else if input.type === 'number'}
              <input type="number" class="input input-sm input-bordered"
                class:input-error={input.required && missingRequired.includes(input.name)}
                bind:value={values[input.name]} />
            {:else}
              <input type="text" class="input input-sm input-bordered"
                class:input-error={input.required && missingRequired.includes(input.name)}
                bind:value={values[input.name]} />
            {/if}
            {#if input.description}
              <div class="label py-1">
                <span class="label-text-alt text-xs text-base-content/50">{input.description}</span>
              </div>
            {/if}
          </div>
        {/each}

        {#if submitErr}
          <div class="alert alert-error py-2 text-sm">{submitErr}</div>
        {/if}

        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-ghost btn-sm" onclick={onClose}>Cancel</button>
          <button type="submit" class="btn btn-primary btn-sm ml-auto" disabled={!canSubmit}>
            {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
            Install
          </button>
        </div>
      </form>
    {/if}
  </div>

  {#if resultUUID}
    <footer class="flex shrink-0 items-center justify-end gap-2 border-t border-base-300 px-5 py-3">
      <button class="btn btn-primary btn-sm" onclick={dismiss}>Done</button>
    </footer>
  {/if}
</aside>
