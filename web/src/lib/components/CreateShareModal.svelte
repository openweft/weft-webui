<script lang="ts">
  // Create-share modal — tenant-admin gated server-side. Project comes
  // from the session scope ; the operator picks one in the topbar
  // before creating. No daemon RPC backs this yet (CubeFS volumes are
  // provisioned out-of-band) ; the share row goes into the in-memory
  // store with status="provisioning" until reconciled.
  import { createShare, getMe } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let sizeGB = $state(100);
  let readonly = $state(false);

  let project = $state('');
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      getMe().then((u) => (project = u.project));
    } else {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!project) {
      error = 'no project in scope — pick one in the Topbar';
      return;
    }
    if (!name.trim() || sizeGB <= 0) {
      error = 'name and a positive size are required';
      return;
    }
    busy = true;
    try {
      await createShare({ name: name.trim(), size_gb: sizeGB, read_only: readonly });
      onCreated();
      reset();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }

  function reset() { name = ''; sizeGB = 100; readonly = false; error = ''; }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submit}>
    <h3 class="text-lg font-bold">New share</h3>
    <p class="text-sm text-base-content/60">
      CubeFS POSIX share, mountable by every microVM in project
      <span class="font-mono">{project || '—'}</span>.
      Tenant admin only.
    </p>

    <label class="form-control mt-4">
      <span class="label-text text-xs">Name</span>
      <input class="input input-sm input-bordered" placeholder="team-data-2" bind:value={name} required />
    </label>

    <div class="mt-3 grid gap-3 sm:grid-cols-[1fr_auto]">
      <label class="form-control">
        <span class="label-text text-xs">Size (GB)</span>
        <input type="number" min="1" class="input input-sm input-bordered tabular-nums" bind:value={sizeGB} />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Read-only</span>
        <input type="checkbox" class="toggle toggle-sm mt-1" bind:checked={readonly} />
      </label>
    </div>

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
