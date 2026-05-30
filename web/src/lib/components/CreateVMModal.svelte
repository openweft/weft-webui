<script lang="ts">
  // Create-microVM modal. Driven from ResourcePage's `+ New microVM`
  // button. Project comes from the session scope (Topbar selector) ;
  // a no-project state surfaces inline so the user knows what's
  // missing before submitting.
  //
  // Flavor list is loaded from /api/resources/flavors — picking one
  // pre-fills cpu / mem / disk_gb so the operator can either accept
  // the envelope or override per field.
  import { createVM, getMe, getFlavors, type Row } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  // Inputs.
  let name = $state('');
  let image = $state('alpine:3.21');
  let cpu = $state(2);
  let memMB = $state(4096);
  let diskGB = $state(10);
  let sshPub = $state('');
  let selectedFlavor = $state('');

  // Loaded once on mount : the user's current scope (for "project: X"
  // pill) and the cluster's flavor catalogue.
  let project = $state('');
  let flavors = $state<Row[]>([]);

  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      getMe().then((u) => (project = u.project));
      getFlavors().then((rs) => (flavors = rs)).catch(() => { /* ok if empty */ });
    } else {
      dialog?.close();
    }
  });

  function pickFlavor(f: Row) {
    selectedFlavor = (f.name as string) ?? '';
    if (typeof f.vcpu === 'number') cpu = f.vcpu;
    // RAM in the registry is "4Gi" — parse for prefill.
    if (typeof f.ram === 'string') {
      const m = f.ram.match(/^(\d+)\s*(M|G)i?$/);
      if (m) memMB = parseInt(m[1], 10) * (m[2] === 'G' ? 1024 : 1);
    }
    if (typeof f.ephemeral_gb === 'number') diskGB = f.ephemeral_gb;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!project) {
      error = 'no project in scope — pick one in the Topbar before creating a microVM';
      return;
    }
    if (!name.trim() || !image.trim()) {
      error = 'name and image are required';
      return;
    }
    busy = true;
    try {
      await createVM({
        Name: name.trim(),
        Image: image.trim(),
        CPU: cpu,
        MemMB: memMB,
        DiskGB: diskGB,
        SSHPub: sshPub.trim(),
      });
      onCreated();
      reset();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }

  function reset() {
    name = ''; image = 'alpine:3.21';
    cpu = 2; memMB = 4096; diskGB = 10;
    sshPub = ''; selectedFlavor = ''; error = '';
  }

  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-2xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">New microVM</h3>
    <p class="text-sm text-base-content/60">
      In project
      <span class="font-mono">{project || '—'}</span>.
      The scheduler picks a host honouring the flavor's placement rules.
    </p>

    {#if flavors.length > 0}
      <div class="mt-4">
        <span class="label-text text-xs">Flavor</span>
        <div class="mt-1 grid gap-2 sm:grid-cols-4">
          {#each flavors as f (f.name)}
            <button
              type="button"
              class="rounded-box border p-2 text-left text-sm hover:bg-base-200"
              class:border-primary={selectedFlavor === f.name}
              class:border-base-300={selectedFlavor !== f.name}
              onclick={() => pickFlavor(f)}
            >
              <div class="font-medium">{f.name}</div>
              <div class="text-xs text-base-content/60">
                {f.vcpu} vCPU · {f.ram} · {f.ephemeral_gb} GB
              </div>
            </button>
          {/each}
        </div>
      </div>
    {/if}

    <div class="mt-4 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">Name</span>
        <input class="input input-sm input-bordered" placeholder="web-2" bind:value={name} required />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Image (OCI / disk)</span>
        <input class="input input-sm input-bordered" placeholder="alpine:3.21" bind:value={image} required />
      </label>
    </div>

    <div class="mt-3 grid gap-3 sm:grid-cols-3">
      <label class="form-control">
        <span class="label-text text-xs">vCPU</span>
        <input type="number" min="1" class="input input-sm input-bordered tabular-nums" bind:value={cpu} />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Memory (MB)</span>
        <input type="number" min="128" step="128" class="input input-sm input-bordered tabular-nums" bind:value={memMB} />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Disk (GB)</span>
        <input type="number" min="1" class="input input-sm input-bordered tabular-nums" bind:value={diskGB} />
      </label>
    </div>

    <label class="form-control mt-3">
      <span class="label-text text-xs">SSH public key (optional, injected via cloud-init)</span>
      <textarea class="textarea textarea-sm textarea-bordered font-mono text-xs"
        rows="2" placeholder="ssh-ed25519 AAAA…" bind:value={sshPub}></textarea>
    </label>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={cancel}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Create
      </button>
    </div>
  </form>
</dialog>
