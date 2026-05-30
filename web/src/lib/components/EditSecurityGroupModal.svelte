<script lang="ts">
  // EditSecurityGroupModal — rename + description for one group.
  // Rules editing is done inline in SecurityPage's right pane.
  import { updateSecurityGroup, type Row } from '../api';

  let {
    open = $bindable(false),
    group,
    onSaved,
  }: {
    open: boolean;
    group: Row | null;
    onSaved: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  let name = $state('');
  let description = $state('');
  let enabled = $state(true);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      if (group) {
        name = String(group.name ?? '');
        description = String(group.description ?? '');
        enabled = group.enabled !== false; // missing = enabled
        error = '';
      }
      dialog?.showModal();
    } else {
      dialog?.close();
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!group) return;
    error = '';
    if (!name.trim()) { error = 'name is required'; return; }
    busy = true;
    try {
      await updateSecurityGroup(String(group.uuid), {
        name: name.trim(),
        description: description.trim(),
        enabled,
      });
      onSaved();
      open = false;
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-lg" onsubmit={submit}>
    <h3 class="text-lg font-bold">Edit security group</h3>
    <p class="text-sm text-base-content/60">
      Rename and re-describe. Rules are edited from the right pane.
    </p>

    <label class="form-control mt-4">
      <span class="label-text text-xs">Name</span>
      <input class="input input-sm input-bordered font-mono"
        placeholder="web" bind:value={name} required />
    </label>

    <label class="form-control mt-3">
      <span class="label-text text-xs">Description</span>
      <textarea class="textarea textarea-sm textarea-bordered" rows="3"
        placeholder="HTTP/HTTPS ingress for the web tier"
        bind:value={description}></textarea>
    </label>

    <label class="mt-3 flex items-center gap-2 text-sm">
      <input type="checkbox" class="toggle toggle-sm" bind:checked={enabled} />
      Enabled
      <span class="text-xs text-base-content/50">
        — disabled groups stay in the catalogue but their rules don't apply.
      </span>
    </label>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={() => (open = false)}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Save
      </button>
    </div>
  </form>
</dialog>
