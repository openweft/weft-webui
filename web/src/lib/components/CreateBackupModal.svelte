<script lang="ts">
  // Create-backup modal. Driver-side only ; the parent volume MUST
  // be block-backed (the caller is responsible for gating this — see
  // VolumeDrawer's Backups tab, which hides the button on file
  // backends and surfaces a tooltip explaining why).
  //
  // Targets the form accepts (validated server-side) :
  //
  //   oci://<registry>/<repo>:<tag>       recommended
  //   s3://<bucket>@<region>/<prefix>     versitygw / CubeFS objectnode
  //   sftp://<user>@<host>:<port>/<path>  sftpgo
  //   fs:///<absolute_path>               dev / tests
  //
  // Encryption + incremental chains are daemon-owned ; the operator
  // sets WEFT_BACKUP_PASSPHRASE on the agent and never enters one
  // here. The form mentions this explicitly so the operator isn't
  // surprised by the absence of a passphrase field.
  import { createVolumeBackup, type VolumeSnapshotRow } from '../api';

  let {
    open = $bindable(false),
    snapshot,
    project,
    onCreated,
  }: {
    open: boolean;
    snapshot: VolumeSnapshotRow;
    project?: string;
    onCreated: () => void;
  } = $props();

  let dialog: HTMLDialogElement;

  // Default scheme is oci:// per openweft policy (content-addressed,
  // cosign-signable, mirrors via standard tooling). Operators with
  // an S3 or sftpgo stack flip the scheme dropdown.
  let scheme = $state<'oci' | 's3' | 'sftp' | 'fs'>('oci');
  let target = $state('');
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      dialog?.showModal();
      // Seed the URL field with a scheme-appropriate placeholder
      // so the operator only has to fill in the right-hand side.
      if (!target) target = seedFor(scheme, snapshot);
    } else {
      dialog?.close();
    }
  });

  function seedFor(s: typeof scheme, snap: VolumeSnapshotRow): string {
    const stem = `${snap.volume_uuid ?? 'vol'}/${snap.name ?? snap.uuid ?? 'snap'}`;
    switch (s) {
      case 'oci':
        return `oci://ghcr.io/<org>/backups:${stem}`;
      case 's3':
        return `s3://backups@us-east-1/${stem}/`;
      case 'sftp':
        return `sftp://backupbot@backup.example.com:2022/backups/${stem}/`;
      case 'fs':
        return `fs:///var/lib/weft-block-backups/${stem}`;
    }
  }

  function onSchemeChange(next: typeof scheme) {
    scheme = next;
    // Replace the prefix only if the operator hasn't customised it.
    // Heuristic : if the URL starts with one of our known schemes
    // followed by "://", we own it and can rewrite ; otherwise leave
    // alone so we don't trample a hand-typed target.
    const knownPrefixes = ['oci://', 's3://', 'sftp://', 'fs:///'];
    if (knownPrefixes.some((p) => target.startsWith(p))) {
      target = seedFor(next, snapshot);
    }
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (!target.trim()) {
      error = 'target URL is required';
      return;
    }
    if (!/^[a-z]+:\/\//.test(target.trim())) {
      error = 'target must be a URL (oci:// / s3:// / sftp:// / fs:///)';
      return;
    }
    busy = true;
    try {
      await createVolumeBackup(snapshot.uuid ?? '', target.trim(), project);
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
    scheme = 'oci';
    target = '';
    error = '';
  }
  function cancel() {
    open = false;
    reset();
  }
</script>

<dialog class="modal" bind:this={dialog} onclose={() => (open = false)}>
  <form method="dialog" class="modal-box max-w-xl" onsubmit={submit}>
    <h3 class="text-lg font-bold">Ship backup to target</h3>
    <p class="text-sm text-base-content/60">
      From snapshot <span class="font-mono">{snapshot?.name ?? snapshot?.uuid ?? ''}</span>
      (volume <span class="font-mono">{snapshot?.volume_uuid ?? ''}</span>).
    </p>

    <div class="mt-4 grid grid-cols-[8rem_1fr] items-center gap-3">
      <label class="text-sm font-medium" for="bk-scheme">Scheme</label>
      <select
        id="bk-scheme"
        class="select select-sm select-bordered"
        value={scheme}
        onchange={(e) => onSchemeChange(e.currentTarget.value as typeof scheme)}
      >
        <option value="oci">oci:// — recommended (content-addressed)</option>
        <option value="s3">s3:// — versitygw / CubeFS objectnode</option>
        <option value="sftp">sftp:// — sftpgo / OpenSSH</option>
        <option value="fs">fs:// — local filesystem (dev only)</option>
      </select>

      <label class="text-sm font-medium" for="bk-target">Target URL</label>
      <input
        id="bk-target"
        class="input input-sm input-bordered font-mono"
        placeholder={seedFor(scheme, snapshot)}
        bind:value={target}
        required
      />
    </div>

    <div class="mt-3 text-xs text-base-content/60">
      Encryption (ChaCha20-Poly1305 / AES-256-GCM) and incremental
      chains are handled by the daemon when
      <span class="font-mono">WEFT_BACKUP_PASSPHRASE</span> is set on
      the agent. The dashboard never sees the passphrase.
    </div>

    {#if error}<div class="mt-3 alert alert-error py-2 text-sm">{error}</div>{/if}

    <div class="modal-action">
      <button type="button" class="btn btn-sm btn-ghost" onclick={cancel}>Cancel</button>
      <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
        {#if busy}<span class="loading loading-spinner loading-xs"></span>{/if}
        Ship backup
      </button>
    </div>
  </form>
</dialog>
