<script lang="ts">
  // BucketPolicyModal — edit the trimmed S3 policy attached to a
  // bucket. Statements are flat rows : Effect | Principal | Action |
  // Resource, with Add / Remove / Save. The vocabulary mirrors the
  // server's closed validation set (see internal/server/objectstorage.go) ;
  // widening the picker requires the matching server-side bump.
  import {
    getBucketPolicy, setBucketPolicy,
    type BucketPolicy, type PolicyStatement, type PolicyEffect, type PolicyAction,
  } from '../api';

  let {
    bucket,
    open = $bindable(false),
    onSaved,
  }: {
    bucket: string;
    open: boolean;
    onSaved?: () => void;
  } = $props();

  let dialog: HTMLDialogElement;
  let statements = $state<PolicyStatement[]>([]);
  let loading = $state(false);
  let saving = $state(false);
  let error = $state('');

  const effects: PolicyEffect[] = ['Allow', 'Deny'];
  const actions: PolicyAction[] = [
    's3:GetObject', 's3:PutObject', 's3:DeleteObject', 's3:ListBucket',
  ];

  // Refresh whenever the modal opens (and on bucket change while open).
  // A closed-then-reopened modal should never leak stale rows.
  $effect(() => {
    if (!open) {
      dialog?.close();
      return;
    }
    dialog?.showModal();
    if (!bucket) return;
    loading = true;
    error = '';
    getBucketPolicy(bucket)
      .then((p: BucketPolicy) => { statements = p.statements ?? []; })
      .catch((e) => { error = String(e); })
      .finally(() => { loading = false; });
  });

  function addStatement() {
    statements = [
      ...statements,
      { effect: 'Allow', principal: '*', action: 's3:GetObject', resource: '*' },
    ];
  }
  function removeStatement(i: number) {
    statements = statements.filter((_, j) => j !== i);
  }

  async function save() {
    saving = true;
    error = '';
    try {
      await setBucketPolicy(bucket, { version: '2012-10-17', statements });
      open = false;
      onSaved?.();
    } catch (e) {
      error = String(e);
    } finally {
      saving = false;
    }
  }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <div class="modal-box max-w-3xl">
    <h3 class="text-lg font-bold">Bucket policy · <code>{bucket}</code></h3>
    <p class="mt-1 text-sm text-base-content/60">
      Statements are evaluated top-to-bottom ; a matching Deny wins over
      an Allow. Principal accepts an OIDC subject (typically the user's
      email) or <code>*</code> for every authenticated user. Resource is
      a key prefix (<code>datasets/*</code>) or <code>*</code> for the
      whole bucket. The vocabulary is intentionally narrow — wire the
      conditions / wildcards your environment needs by extending both
      sides in lockstep.
    </p>

    {#if loading}
      <div class="mt-4 flex justify-center py-10"><span class="loading loading-spinner loading-lg"></span></div>
    {:else}
      <div class="mt-4 overflow-x-auto">
        <table class="table table-sm">
          <thead>
            <tr>
              <th class="w-24">Effect</th>
              <th>Principal</th>
              <th class="w-44">Action</th>
              <th>Resource</th>
              <th class="w-12"></th>
            </tr>
          </thead>
          <tbody>
            {#each statements as s, i (i)}
              <tr>
                <td>
                  <select class="select select-xs select-bordered w-full" bind:value={s.effect}>
                    {#each effects as e (e)}<option value={e}>{e}</option>{/each}
                  </select>
                </td>
                <td>
                  <input
                    class="input input-xs input-bordered w-full font-mono"
                    placeholder="alice@… or *"
                    bind:value={s.principal}
                  />
                </td>
                <td>
                  <select class="select select-xs select-bordered w-full" bind:value={s.action}>
                    {#each actions as a (a)}<option value={a}>{a}</option>{/each}
                  </select>
                </td>
                <td>
                  <input
                    class="input input-xs input-bordered w-full font-mono"
                    placeholder="datasets/* or *"
                    bind:value={s.resource}
                  />
                </td>
                <td>
                  <button
                    class="btn btn-xs btn-ghost text-error"
                    title="Remove statement"
                    onclick={() => removeStatement(i)}
                  >×</button>
                </td>
              </tr>
            {:else}
              <tr><td colspan="5" class="text-center text-base-content/50">
                No statements — every authenticated user in the project
                can read and write.
              </td></tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="mt-3">
        <button class="btn btn-xs btn-ghost gap-1" onclick={addStatement}>
          <span class="text-base leading-none">+</span> Add statement
        </button>
      </div>
    {/if}

    {#if error}
      <div class="mt-3 alert alert-error py-2 text-sm">{error}</div>
    {/if}

    <div class="modal-action">
      <button class="btn btn-sm btn-ghost" onclick={() => (open = false)}>Cancel</button>
      <button class="btn btn-sm btn-primary" disabled={saving || loading} onclick={save}>
        {#if saving}<span class="loading loading-spinner loading-xs"></span>{/if}
        Save
      </button>
    </div>
  </div>
  <form method="dialog" class="modal-backdrop"><button>close</button></form>
</dialog>
