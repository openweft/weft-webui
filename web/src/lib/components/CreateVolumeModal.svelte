<script lang="ts">
  // Create-volume modal. Project from the session scope ; the row
  // ends up in the volumes table after refresh().
  import { createVolume, getMe } from '../api';

  let {
    open = $bindable(false),
    onCreated,
  }: {
    open: boolean;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let sizeGiB = $state(10);
  let format = $state<'raw' | 'qcow2'>('raw');

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
    if (!name.trim() || sizeGiB <= 0) {
      error = 'name and a positive size are required';
      return;
    }
    busy = true;
    try {
      await createVolume({ name: name.trim(), size_gib: sizeGiB, format });
      onCreated();
      reset();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }

  function reset() { name = ''; sizeGiB = 10; format = 'raw'; error = ''; }
  function cancel() { open = false; reset(); }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submit}>
    <h3 class="text-lg font-bold">New volume</h3>
    <p class="text-sm text-base-content/60">
      In project <span class="font-mono">{project || '—'}</span>.
      Block storage, single-attach.
    </p>

    <label class="form-control mt-4">
      <span class="label-text text-xs">Name</span>
      <input class="input input-sm input-bordered" placeholder="pg-data-2" bind:value={name} required />
    </label>

    <div class="mt-3 grid gap-3 sm:grid-cols-2">
      <label class="form-control">
        <span class="label-text text-xs">Size (GiB)</span>
        <input type="number" min="1" class="input input-sm input-bordered tabular-nums" bind:value={sizeGiB} />
      </label>
      <label class="form-control">
        <span class="label-text text-xs">Format</span>
        <select class="select select-sm select-bordered" bind:value={format}>
          <option value="raw">raw</option>
          <option value="qcow2">qcow2</option>
        </select>
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
