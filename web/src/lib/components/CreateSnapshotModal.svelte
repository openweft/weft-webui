<script lang="ts">
  // Create-snapshot modal. Takes a parent volume row + a snapshot
  // name ; the server's dispatch on the parent's backend means the
  // caller doesn't have to worry about file/reflink vs block/driver.
  // Surfaces server errors verbatim (e.g. "name already in use") in
  // the inline alert.
  //
  // Used from VolumeDrawer's Snapshots tab. The drawer owns the
  // parent volume row ; this modal only consumes (uuid, name, project).
  import { createVolumeSnapshot, type Row } from '../api';

  let {
    open = $bindable(false),
    volume,
    onCreated,
  }: {
    open: boolean;
    volume: Row;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
    } else {
      dialog?.close();
    }
  });

  let volumeName = $derived(typeof volume?.name === 'string' ? volume.name : '');
  let volumeUUID = $derived(typeof volume?.uuid === 'string' ? volume.uuid : volumeName);
  let project = $derived(typeof volume?.project === 'string' ? volume.project : '');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!name.trim()) {
      error = 'name is required';
      return;
    }
    busy = true;
    try {
      await createVolumeSnapshot(volumeUUID, name.trim(), project || undefined);
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
    name = '';
    error = '';
  }
  function cancel() {
    open = false;
    reset();
  }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-md" onsubmit={submit}>
    <h3 class="text-lg font-bold">Snapshot volume</h3>
    <p class="text-sm text-base-content/60">
      Of <span class="font-mono">{volumeName}</span>
      {#if project}
        in <span class="font-mono">{project}</span>
      {/if}
      .
    </p>

    <label class="form-control mt-4">
      <span class="label-text text-xs">Snapshot name</span>
      <input
        class="input input-sm input-bordered"
        placeholder="before-upgrade"
        bind:value={name}
        required
      />
      <span class="label-text-alt text-xs text-base-content/50">
        Unique within this volume.
      </span>
    </label>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={cancel}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Snapshot
      </button>
    </div>
  </form>
</dialog>
