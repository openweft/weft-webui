<script lang="ts">
  // Right-side drawer that opens when a Volume row is clicked or via
  // the page-header Edit button. Three tabs :
  //
  //   General — read-only summary + editable name + free-form
  //     description.
  //   Mount   — guest-side mount point + filesystem hint the host
  //     uses for mkfs when the guest claims a fresh volume.
  //   Properties — k/v annotations the orchestration layer reads to
  //     make placement / lifecycle decisions. Mirrors VMProperty
  //     except with no guest_readable flag (volume properties drive
  //     host-side behaviour, not guest-side).
  //
  // All write operations are admin-gated server-side ; the drawer
  // surfaces the affordances unconditionally and lets the server's
  // 403 fall through to the inline alert if a non-admin reaches them.
  import {
    getVolumeMetadata, setVolumeMetadata, renameVolumeByKey,
    listVolumeProperties, setVolumeProperty, deleteVolumeProperty,
    listVolumeSnapshots, revertVolumeSnapshot, deleteVolumeSnapshot, restoreVolumeSnapshot,
    listVolumeBackups, deleteVolumeBackup, restoreVolumeBackup,
    type VolumeMetadata, type VolumeProperty, type Row,
    type VolumeSnapshotRow, type VolumeBackupRow,
  } from '../api';
  import CreateSnapshotModal from './CreateSnapshotModal.svelte';
  import CreateBackupModal from './CreateBackupModal.svelte';

  let {
    row,
    onClose,
    onChanged,
  }: {
    row: Row;
    onClose: () => void;
    onChanged: () => void;
  } = $props();

  let key = $derived(String(row.name));
  let sizeGiB = $derived(typeof row.size_gib === 'number' ? row.size_gib : 0);
  let format = $derived(typeof row.format === 'string' ? row.format : '—');
  let attachedTo = $derived(typeof row.attached_to === 'string' ? row.attached_to : '');
  let project = $derived(typeof row.project === 'string' ? row.project : '—');
  let created = $derived(typeof row.created === 'string' ? row.created : '—');
  // backend gates Revert + Backup affordances on the Snapshots tab.
  // Empty string is treated as "file" (older agents that don't yet
  // project the field). Block-backed volumes get the full action set ;
  // file-backed volumes hide the disabled actions with a tooltip
  // explaining why.
  let backend = $derived(typeof row.backend === 'string' && row.backend !== '' ? row.backend : 'file');
  let isBlock = $derived(backend === 'block');

  type Tab = 'general' | 'mount' | 'properties' | 'snapshots' | 'backups';
  let tab = $state<Tab>('general');

  // ---- General tab : name + description ----
  let editName = $state('');
  let editDescription = $state('');
  let metadata = $state<VolumeMetadata | null>(null);
  let metaLoading = $state(true);
  let metaErr = $state('');
  let metaBusy = $state(false);
  let metaSaved = $state(false);

  async function refreshMetadata() {
    metaLoading = true; metaErr = '';
    try {
      const m = await getVolumeMetadata(key);
      metadata = m;
      editDescription = m.description ?? '';
      editMountPoint = m.mount_point ?? '';
      editFilesystem = (m.filesystem ?? '') as typeof editFilesystem;
    } catch (e) {
      metaErr = String(e);
    } finally {
      metaLoading = false;
    }
  }

  $effect(() => {
    // Re-fetch when the drawer rebinds to a new volume.
    key; // dep
    editName = String(row.name ?? '');
    refreshMetadata();
    refreshProperties();
  });

  let nameDirty = $derived(editName !== String(row.name ?? ''));
  let descriptionDirty = $derived(metadata !== null && editDescription !== (metadata?.description ?? ''));

  async function saveGeneral() {
    if (!nameDirty && !descriptionDirty) return;
    metaBusy = true; metaErr = ''; metaSaved = false;
    try {
      let currentKey = key;
      if (nameDirty) {
        const newName = editName.trim();
        if (!newName) throw new Error('name is required');
        await renameVolumeByKey(currentKey, newName);
        currentKey = newName;
      }
      if (descriptionDirty || nameDirty) {
        await setVolumeMetadata(currentKey, {
          description: editDescription,
          mount_point: metadata?.mount_point ?? '',
          filesystem: (metadata?.filesystem ?? '') as '' | 'ext4' | 'xfs' | 'btrfs' | 'ext3' | 'zfs',
        });
      }
      metaSaved = true;
      onChanged();
      // Drawer's row binding still points at the old name ; the page
      // will refresh + re-mount us with the new row on the next tick.
    } catch (e) {
      metaErr = String(e);
    } finally {
      metaBusy = false;
    }
  }

  // ---- Mount tab : mount_point + filesystem ----
  let editMountPoint = $state('');
  let editFilesystem = $state<'' | 'ext4' | 'xfs' | 'btrfs' | 'ext3' | 'zfs'>('');
  let mountBusy = $state(false);
  let mountErr = $state('');
  let mountSaved = $state(false);

  let mountDirty = $derived(metadata !== null && (
    editMountPoint !== (metadata?.mount_point ?? '')
    || editFilesystem !== ((metadata?.filesystem ?? '') as typeof editFilesystem)
  ));

  async function saveMount() {
    if (!mountDirty) return;
    mountBusy = true; mountErr = ''; mountSaved = false;
    try {
      const saved = await setVolumeMetadata(key, {
        description: metadata?.description ?? '',
        mount_point: editMountPoint.trim(),
        filesystem: editFilesystem,
      });
      metadata = saved;
      mountSaved = true;
      onChanged();
    } catch (e) {
      mountErr = String(e);
    } finally {
      mountBusy = false;
    }
  }

  // ---- Properties tab : k/v list ----
  let properties = $state<VolumeProperty[]>([]);
  let propsLoading = $state(true);
  let propsErr = $state('');
  let newPropKey = $state('');
  let newPropValue = $state('');
  let addBusy = $state(false);
  // busy-by-key flags so two parallel mutations on different rows
  // each show their own spinner without blocking the whole list.
  let busyKeys = $state<Record<string, boolean>>({});

  async function refreshProperties() {
    propsLoading = true; propsErr = '';
    try {
      properties = await listVolumeProperties(key);
    } catch (e) {
      propsErr = String(e);
    } finally {
      propsLoading = false;
    }
  }

  async function addProperty() {
    const k = newPropKey.trim();
    if (!k) return;
    addBusy = true; propsErr = '';
    try {
      await setVolumeProperty(key, k, newPropValue);
      newPropKey = '';
      newPropValue = '';
      await refreshProperties();
    } catch (e) {
      propsErr = String(e);
    } finally {
      addBusy = false;
    }
  }

  async function updateProperty(p: VolumeProperty, newValue: string) {
    busyKeys = { ...busyKeys, [p.key]: true };
    propsErr = '';
    try {
      await setVolumeProperty(key, p.key, newValue);
      await refreshProperties();
    } catch (e) {
      propsErr = String(e);
    } finally {
      busyKeys = { ...busyKeys, [p.key]: false };
    }
  }

  async function removeProperty(p: VolumeProperty) {
    if (!confirm(`Remove property "${p.key}" ?`)) return;
    busyKeys = { ...busyKeys, [p.key]: true };
    propsErr = '';
    try {
      await deleteVolumeProperty(key, p.key);
      await refreshProperties();
    } catch (e) {
      propsErr = String(e);
    } finally {
      busyKeys = { ...busyKeys, [p.key]: false };
    }
  }

  function fmtUpdated(ts: string): string {
    if (!ts) return '—';
    return ts.slice(0, 19).replace('T', ' ');
  }

  // ---- Snapshots tab : per-volume snapshot list + actions ----
  //
  // The dispatch on parent.Backend lives in the agent ; from the
  // dashboard's perspective the affordances are uniform. Revert /
  // restore-as-backup that don't apply to a file-backend parent
  // surface the agent's "block-only" error in the inline alert
  // when triggered, rather than being hidden a priori (the parent's
  // backend field isn't yet projected into the row).
  let volumeUUID = $derived(typeof row.uuid === 'string' ? row.uuid : key);
  let projectKey = $derived(typeof row.project === 'string' && row.project !== '—' ? row.project : '');

  let snapshots = $state<VolumeSnapshotRow[]>([]);
  let snapsLoading = $state(false);
  let snapsErr = $state('');
  let snapBusyUUID = $state<string>('');
  let createSnapshotOpen = $state(false);

  async function refreshSnapshots() {
    snapsLoading = true;
    snapsErr = '';
    try {
      snapshots = await listVolumeSnapshots(volumeUUID, projectKey || undefined);
    } catch (e) {
      snapsErr = String(e);
    } finally {
      snapsLoading = false;
    }
  }

  async function revertSnap(s: VolumeSnapshotRow) {
    if (!s.uuid) return;
    const ok = confirm(
      `Revert ${key} to snapshot ${s.name ?? s.uuid} ?\n\n` +
      `This rolls back the parent volume's contents in place.\n` +
      `Only supported on block-backend volumes (file-backed ones reject).\n` +
      `The volume should be detached first.`,
    );
    if (!ok) return;
    snapBusyUUID = s.uuid;
    snapsErr = '';
    try {
      await revertVolumeSnapshot(s.uuid);
      onChanged();
    } catch (e) {
      snapsErr = String(e);
    } finally {
      snapBusyUUID = '';
    }
  }

  async function restoreSnap(s: VolumeSnapshotRow) {
    if (!s.uuid) return;
    const newName = prompt(
      `Restore snapshot ${s.name ?? s.uuid} into a new volume — name ?`,
      `${s.name ?? 'restored'}-${Date.now().toString(36)}`,
    );
    if (!newName) return;
    snapBusyUUID = s.uuid;
    snapsErr = '';
    try {
      await restoreVolumeSnapshot(s.uuid, newName.trim(), projectKey || undefined);
      onChanged();
    } catch (e) {
      snapsErr = String(e);
    } finally {
      snapBusyUUID = '';
    }
  }

  async function deleteSnap(s: VolumeSnapshotRow) {
    if (!s.uuid) return;
    if (!confirm(`Delete snapshot ${s.name ?? s.uuid} ? This is permanent.`)) return;
    snapBusyUUID = s.uuid;
    snapsErr = '';
    try {
      await deleteVolumeSnapshot(s.uuid);
      await refreshSnapshots();
    } catch (e) {
      snapsErr = String(e);
    } finally {
      snapBusyUUID = '';
    }
  }

  // backupFromSnap opens the CreateBackupModal pre-filled with the
  // chosen snapshot. The modal handles target URL entry + the create
  // call ; on success we refresh the backup list if we're on the
  // Backups tab.
  let backupSnapshot = $state<VolumeSnapshotRow | null>(null);
  let createBackupOpen = $state(false);

  function openBackupModal(s: VolumeSnapshotRow) {
    backupSnapshot = s;
    createBackupOpen = true;
  }

  // ---- Backups tab : list by target + per-backup actions ----
  //
  // Backups are stored at an operator-chosen target URL ; the list
  // can't be enumerated without one. The tab asks for the target,
  // remembers the last one used during this drawer session, then
  // lists the rows scoped to the current volume's UUID.
  let backupTarget = $state('');
  let backups = $state<VolumeBackupRow[]>([]);
  let backupsLoading = $state(false);
  let backupsErr = $state('');
  let backupBusyURL = $state('');

  async function refreshBackups() {
    if (!backupTarget.trim()) {
      backups = [];
      return;
    }
    backupsLoading = true;
    backupsErr = '';
    try {
      backups = await listVolumeBackups(backupTarget.trim(), volumeUUID, projectKey || undefined);
    } catch (e) {
      backupsErr = String(e);
    } finally {
      backupsLoading = false;
    }
  }

  async function restoreBackup(b: VolumeBackupRow) {
    if (!b.url) return;
    const newName = prompt(
      `Restore backup into a new volume — name ?`,
      `restored-${Date.now().toString(36)}`,
    );
    if (!newName) return;
    if (!projectKey) {
      backupsErr = 'project is required to restore a backup ; select a project at the top of the page';
      return;
    }
    backupBusyURL = b.url;
    backupsErr = '';
    try {
      await restoreVolumeBackup(b.url, newName.trim(), projectKey);
      onChanged();
    } catch (e) {
      backupsErr = String(e);
    } finally {
      backupBusyURL = '';
    }
  }

  async function deleteBackup(b: VolumeBackupRow) {
    if (!b.url) return;
    if (!confirm(`Delete backup ${b.url} ?\n\nThis removes it from the target store.`)) return;
    backupBusyURL = b.url;
    backupsErr = '';
    try {
      await deleteVolumeBackup(b.url);
      await refreshBackups();
    } catch (e) {
      backupsErr = String(e);
    } finally {
      backupBusyURL = '';
    }
  }

  function bytesHuman(n: number): string {
    if (!n) return '0 B';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    let i = 0;
    let v = n;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i += 1;
    }
    return v.toFixed(v >= 100 || i === 0 ? 0 : 1) + ' ' + units[i];
  }

  // Lazy-load snapshots / backups when the operator first lands on
  // each tab — avoids unnecessary RPCs on volumes the operator only
  // wanted to rename.
  $effect(() => {
    if (tab === 'snapshots' && snapshots.length === 0 && !snapsErr) {
      refreshSnapshots();
    }
  });
</script>

<button class="fixed inset-0 z-40 bg-base-300/40" aria-label="Close drawer" onclick={onClose}></button>

<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-3xl flex-col bg-base-100 shadow-2xl">
  <header class="flex shrink-0 items-center gap-3 border-b border-base-300 px-5 py-3">
    <div>
      <h2 class="text-lg font-bold">{key}</h2>
      <p class="text-xs text-base-content/60">
        {sizeGiB} GiB · {format} · {backend} · {attachedTo ? `attached to ${attachedTo}` : 'unattached'} · project {project}
      </p>
    </div>
    <button class="ml-auto btn btn-sm btn-ghost" aria-label="Close" onclick={onClose}>✕</button>
  </header>

  <div role="tablist" class="tabs tabs-border shrink-0 px-5">
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'general'}
      onclick={() => (tab = 'general')}>General</button>
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'mount'}
      onclick={() => (tab = 'mount')}>Mount</button>
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'properties'}
      onclick={() => (tab = 'properties')}>Properties</button>
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'snapshots'}
      onclick={() => (tab = 'snapshots')}>Snapshots</button>
    <button type="button" role="tab" class="tab" class:tab-active={tab === 'backups'}
      onclick={() => (tab = 'backups')}>Backups</button>
  </div>

  <div class="min-h-0 flex-1 overflow-y-auto p-5">
    {#if tab === 'general'}
      {#if metaLoading}
        <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
      {:else}
        <div class="grid gap-3">
          <label class="form-control">
            <span class="label-text mb-1 text-xs">Name</span>
            <input class="input input-sm input-bordered font-mono" bind:value={editName} />
            <span class="mt-1 text-xs text-base-content/50">
              Attached VMs reference the volume by uuid ; this is the dashboard label.
            </span>
          </label>

          <label class="form-control">
            <span class="label-text mb-1 text-xs">Description</span>
            <textarea class="textarea textarea-sm textarea-bordered" rows="4"
              placeholder="What this volume is for, retention notes, who owns it…"
              bind:value={editDescription}></textarea>
          </label>

          <dl class="mt-2 grid grid-cols-2 gap-2 text-xs">
            <div><dt class="text-base-content/50">Size</dt><dd class="font-mono">{sizeGiB} GiB</dd></div>
            <div><dt class="text-base-content/50">Format</dt><dd class="font-mono">{format}</dd></div>
            <div><dt class="text-base-content/50">Attached to</dt><dd class="font-mono">{attachedTo || '—'}</dd></div>
            <div><dt class="text-base-content/50">Created</dt><dd class="font-mono">{created}</dd></div>
            {#if metadata?.updated_by}
              <div class="col-span-2"><dt class="text-base-content/50">Last edit</dt>
                <dd class="font-mono">{fmtUpdated(metadata.updated_at)} · {metadata.updated_by}</dd></div>
            {/if}
          </dl>

          {#if metaErr}<div class="alert alert-error py-2 text-sm">{metaErr}</div>{/if}
          {#if metaSaved}<div class="alert alert-success py-2 text-sm">Saved.</div>{/if}

          <div class="mt-2 flex">
            <button class="ml-auto btn btn-sm btn-primary"
              disabled={(!nameDirty && !descriptionDirty) || metaBusy}
              onclick={saveGeneral}>
              {#if metaBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              Save
            </button>
          </div>
        </div>
      {/if}

    {:else if tab === 'mount'}
      {#if metaLoading}
        <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
      {:else}
        <div class="grid gap-3">
          <label class="form-control">
            <span class="label-text mb-1 text-xs">Mount point</span>
            <input class="input input-sm input-bordered font-mono"
              placeholder="/mnt/data" bind:value={editMountPoint} />
            <span class="mt-1 text-xs text-base-content/50">
              Guest-side path the weft-vm-agent honours when attaching this volume.
              Absolute path ; the agent <code>mkdir -p</code> if needed.
            </span>
          </label>

          <label class="form-control">
            <span class="label-text mb-1 text-xs">Filesystem (mkfs target)</span>
            <select class="select select-sm select-bordered" bind:value={editFilesystem}>
              <option value="">— (preserve existing)</option>
              <option value="ext4">ext4</option>
              <option value="xfs">xfs</option>
              <option value="btrfs">btrfs</option>
              <option value="ext3">ext3</option>
              <option value="zfs">zfs</option>
            </select>
            <span class="mt-1 text-xs text-base-content/50">
              Only takes effect on a <em>fresh</em> volume claimed by the guest for the first time.
              Existing filesystems are left alone.
            </span>
          </label>

          {#if mountErr}<div class="alert alert-error py-2 text-sm">{mountErr}</div>{/if}
          {#if mountSaved}<div class="alert alert-success py-2 text-sm">Saved.</div>{/if}

          <div class="mt-2 flex">
            <button class="ml-auto btn btn-sm btn-primary"
              disabled={!mountDirty || mountBusy}
              onclick={saveMount}>
              {#if mountBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
              Save
            </button>
          </div>
        </div>
      {/if}

    {:else if tab === 'properties'}
      <!-- Properties tab -->
      {#if propsErr}<div class="mb-3 alert alert-error py-2 text-sm">{propsErr}</div>{/if}

      <div class="rounded-box border border-base-300 bg-base-100">
        <table class="table table-sm">
          <thead><tr><th>Key</th><th>Value</th><th>Updated</th><th class="w-0"></th></tr></thead>
          <tbody>
            {#if propsLoading}
              <tr><td colspan="4" class="py-6 text-center">
                <span class="loading loading-spinner"></span>
              </td></tr>
            {:else if properties.length === 0}
              <tr><td colspan="4" class="py-6 text-center text-base-content/50">
                No properties yet. Add one below.
              </td></tr>
            {:else}
              {#each properties as p (p.key)}
                <tr>
                  <td class="font-mono">{p.key}</td>
                  <td>
                    <input class="input input-xs input-bordered w-full"
                      value={p.value}
                      onblur={(e) => {
                        const v = (e.currentTarget as HTMLInputElement).value;
                        if (v !== p.value) updateProperty(p, v);
                      }} />
                  </td>
                  <td class="text-xs text-base-content/60">{fmtUpdated(p.updated_at)}</td>
                  <td>
                    <button class="btn btn-ghost btn-xs text-error"
                      disabled={busyKeys[p.key]}
                      onclick={() => removeProperty(p)}>
                      {#if busyKeys[p.key]}<span class="loading loading-spinner loading-xs"></span>{:else}✕{/if}
                    </button>
                  </td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>

      <div class="mt-3 grid grid-cols-[1fr_2fr_auto] items-end gap-2">
        <label class="form-control">
          <span class="label-text text-xs">New key</span>
          <input class="input input-sm input-bordered font-mono"
            placeholder="workload" bind:value={newPropKey} />
        </label>
        <label class="form-control">
          <span class="label-text text-xs">Value</span>
          <input class="input input-sm input-bordered"
            placeholder="database" bind:value={newPropValue}
            onkeydown={(e) => e.key === 'Enter' && addProperty()} />
        </label>
        <button class="btn btn-sm btn-primary"
          disabled={!newPropKey.trim() || addBusy}
          onclick={addProperty}>
          {#if addBusy}<span class="loading loading-spinner loading-xs"></span>{/if}
          Add
        </button>
      </div>

      <p class="mt-3 text-xs text-base-content/50">
        Properties drive host-side orchestration decisions —
        <code>workload</code>, <code>backup-policy</code>,
        <code>iops-class</code>, etc. The guest filesystem doesn't
        see these directly ; they're read by weft-agent when
        scheduling / replicating / backing up.
      </p>
    {:else if tab === 'snapshots'}
      <div class="mb-3 flex items-center gap-2">
        <h3 class="text-sm font-semibold">Snapshots</h3>
        <span class="text-xs text-base-content/50">{snapshots.length} on this volume</span>
        <div class="ml-auto flex gap-2">
          <button type="button" class="btn btn-xs btn-ghost"
            onclick={refreshSnapshots} disabled={snapsLoading}>
            {#if snapsLoading}<span class="loading loading-spinner loading-xs"></span>{/if}
            Refresh
          </button>
          <button type="button" class="btn btn-xs btn-primary"
            onclick={() => (createSnapshotOpen = true)}>
            Snapshot now
          </button>
        </div>
      </div>

      {#if snapsErr}<div class="mb-3 alert alert-error py-2 text-sm">{snapsErr}</div>{/if}

      {#if snapsLoading && snapshots.length === 0}
        <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
      {:else if snapshots.length === 0}
        <p class="text-sm text-base-content/60">No snapshots yet. Click <em>Snapshot now</em> to freeze the current state.</p>
      {:else}
        <table class="table table-sm">
          <thead>
            <tr>
              <th>Name</th>
              <th class="text-right">Size</th>
              <th>Created</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each snapshots as s (s.uuid)}
              <tr>
                <td class="font-mono text-xs">
                  {s.name || s.uuid}
                  {#if s.name}
                    <div class="text-xs text-base-content/40">{s.uuid}</div>
                  {/if}
                </td>
                <td class="text-right tabular-nums">{s.size_gib} GiB</td>
                <td class="text-xs">{fmtUpdated(s.created ?? '')}</td>
                <td class="text-right">
                  <div class="join">
                    <button type="button" class="btn btn-xs join-item"
                      title={isBlock
                        ? "Revert this volume's contents to the snapshot (volume must be detached first)"
                        : `Revert requires backend=block ; this volume is ${backend}`}
                      onclick={() => revertSnap(s)}
                      disabled={snapBusyUUID === s.uuid || !isBlock}>Revert</button>
                    <button type="button" class="btn btn-xs join-item"
                      title="Clone the snapshot into a fresh volume in the same project"
                      onclick={() => restoreSnap(s)} disabled={snapBusyUUID === s.uuid}>Restore</button>
                    <button type="button" class="btn btn-xs join-item"
                      title={isBlock
                        ? "Ship the snapshot to a backup target (oci/s3/sftp/fs)"
                        : `Backup requires backend=block ; this volume is ${backend}`}
                      onclick={() => openBackupModal(s)}
                      disabled={snapBusyUUID === s.uuid || !isBlock}>Backup</button>
                    <button type="button" class="btn btn-xs btn-error join-item"
                      onclick={() => deleteSnap(s)} disabled={snapBusyUUID === s.uuid}>Delete</button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}

      <p class="mt-3 text-xs text-base-content/50">
        Snapshots dispatch on the parent volume's backend : file
        parents do a reflink clone ; block parents take a controller
        snapshot through weft-block. {#if isBlock}This volume is
        <span class="font-mono">block</span>-backed, so Revert and
        Backup are available.{:else}This volume is
        <span class="font-mono">{backend}</span>-backed, so Revert
        and Backup are disabled — they require <span class="font-mono"
        >block</span>.{/if}
      </p>

    {:else if tab === 'backups'}
      {#if !isBlock}
        <div class="mb-3 alert alert-warning py-2 text-sm">
          This volume is <span class="font-mono">{backend}</span>-backed.
          Off-host backups require <span class="font-mono">block</span> ;
          listing remote backups still works (handy when restoring
          into a new block volume), but new backups can't be shipped
          from this volume's snapshots.
        </div>
      {/if}
      <div class="mb-3 flex flex-wrap items-end gap-2">
        <label class="form-control flex-1 min-w-[18rem]">
          <span class="label-text text-xs">Backup target URL</span>
          <input class="input input-sm input-bordered font-mono"
            placeholder="oci://ghcr.io/[org]/backups:vol-x"
            bind:value={backupTarget} />
        </label>
        <button type="button" class="btn btn-sm btn-primary"
          onclick={refreshBackups} disabled={backupsLoading || !backupTarget.trim()}>
          {#if backupsLoading}<span class="loading loading-spinner loading-xs"></span>{/if}
          List backups
        </button>
      </div>

      {#if backupsErr}<div class="mb-3 alert alert-error py-2 text-sm">{backupsErr}</div>{/if}

      {#if !backupTarget.trim()}
        <p class="text-sm text-base-content/60">
          Enter a target URL above to list backups. Supported schemes :
          <code>oci://</code> (recommended), <code>s3://</code>
          (versitygw / CubeFS objectnode), <code>sftp://</code> (sftpgo),
          <code>fs:///</code> (dev only).
        </p>
      {:else if backupsLoading && backups.length === 0}
        <div class="flex justify-center py-10"><span class="loading loading-spinner"></span></div>
      {:else if backups.length === 0}
        <p class="text-sm text-base-content/60">No backups for this volume at <code>{backupTarget}</code>.</p>
      {:else}
        <table class="table table-sm">
          <thead>
            <tr>
              <th>URL / Snapshot</th>
              <th class="text-right">Size</th>
              <th>State</th>
              <th>Created</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each backups as b (b.url)}
              <tr>
                <td class="font-mono text-xs">
                  <div class="break-all">{b.url}</div>
                  <div class="text-xs text-base-content/40">snapshot {b.snapshot_uuid}</div>
                </td>
                <td class="text-right tabular-nums">{bytesHuman(b.size_bytes ?? 0)}</td>
                <td>
                  <span class="badge badge-xs"
                    class:badge-success={b.state === 'complete'}
                    class:badge-warning={b.state === 'in-progress'}
                    class:badge-error={b.state === 'error'}>
                    {b.state || '—'}
                  </span>
                  {#if b.error}
                    <div class="text-xs text-error">{b.error}</div>
                  {/if}
                </td>
                <td class="text-xs">{fmtUpdated(b.created ?? '')}</td>
                <td class="text-right">
                  <div class="join">
                    <button type="button" class="btn btn-xs join-item"
                      title="Restore the backup into a fresh block volume (size discovered from sidecar metadata)"
                      onclick={() => restoreBackup(b)} disabled={backupBusyURL === b.url}>Restore</button>
                    <button type="button" class="btn btn-xs btn-error join-item"
                      onclick={() => deleteBackup(b)} disabled={backupBusyURL === b.url}>Delete</button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}

      <p class="mt-3 text-xs text-base-content/50">
        Backups are block-only. Encryption +
        incremental chains are honoured by the daemon when
        <code>WEFT_BACKUP_PASSPHRASE</code> is configured on the
        agent. The dashboard never sees the passphrase.
      </p>
    {/if}
  </div>

  <CreateSnapshotModal
    bind:open={createSnapshotOpen}
    volume={row}
    onCreated={() => {
      createSnapshotOpen = false;
      refreshSnapshots();
      onChanged();
    }}
  />

  {#if backupSnapshot}
    <CreateBackupModal
      bind:open={createBackupOpen}
      snapshot={backupSnapshot}
      project={projectKey || undefined}
      onCreated={() => {
        createBackupOpen = false;
        backupSnapshot = null;
        if (tab === 'backups') refreshBackups();
      }}
    />
  {/if}
</aside>
