<script lang="ts">
  // InventoryFormModal — single modal driving create + edit of AZ /
  // Rack / Host rows. Same shape as the inventory huma POST/PUT
  // bodies so the form just hands them off to api.ts wrappers.
  //
  // Parent provides the kind (az|rack|host), mode (create|edit),
  // optional initial row (for edit), and the parent rows needed for
  // the AZ / Rack selectors. The modal calls onSave({ body }) once
  // the form is valid and is closed via onClose.

  import { createAZ, updateAZ, createRack, updateRack, createHost, updateHost,
    type AZBody, type RackBody, type HostBody, type Row } from '../api';

  type Kind = 'az' | 'rack' | 'host';

  let {
    open,
    mode,
    kind,
    initial,
    azs,
    racks,
    onClose,
    onSave,
  }: {
    open: boolean;
    mode: 'create' | 'edit';
    kind: Kind;
    initial?: Row | null;
    azs: Row[];
    racks: Row[];
    onClose: () => void;
    onSave: () => void;
  } = $props();

  let err = $state('');
  let saving = $state(false);

  // Field state — covers the union of all three kinds. Each branch
  // reads the fields it cares about.
  let f_code   = $state('');
  let f_name   = $state('');
  let f_region = $state('');
  let f_az     = $state('');
  let f_rack   = $state('');
  let f_position = $state('');
  let f_arch     = $state('arm64');
  let f_hyper    = $state('qemu-kvm');
  let f_gpu      = $state('');
  let f_status   = $state('active');
  // U-occupancy fields. Rack carries total height ; host carries
  // top-of-unit position + chassis height. Defaults : 42U rack,
  // 1U host placed at U1 (top). Zero means "auto-pack" for hosts.
  let f_rack_height_u = $state(42);
  let f_position_u    = $state(0);
  let f_height_u      = $state(1);

  // Reset the form whenever the modal opens or switches target.
  $effect(() => {
    if (!open) return;
    err = '';
    saving = false;
    const r = initial ?? {};
    f_code   = String(r.code ?? '');
    f_name   = String(r.name ?? '');
    f_region = String(r.region ?? '');
    f_az     = String(r.az ?? (azs[0]?.code ?? ''));
    f_rack   = String(r.rack ?? '');
    f_position = String(r.position ?? '');
    f_arch     = String(r.arch ?? 'arm64');
    f_hyper    = String(r.hypervisor ?? 'qemu-kvm');
    f_gpu      = String(r.gpu ?? '');
    f_status   = String(r.status ?? 'active');
    f_rack_height_u = Number(r.height_u ?? 42) || 42;
    f_position_u    = Number(r.position_u ?? 0) || 0;
    f_height_u      = Number(r.height_u ?? 1) || 1;
  });

  // Racks limited to the currently-selected AZ (only used for host
  // creation ; rack creation lets the operator pick any AZ).
  let racksForAZ = $derived(racks.filter((r) => String(r.az ?? '') === f_az));

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    err = '';
    saving = true;
    try {
      // The state vars are plain strings ; the body enums are
      // a closed union. Cast on assembly — the <select> options
      // are the only sources, so the cast is safe.
      type Status = AZBody['status'];
      type Arch = HostBody['arch'];
      type Hyper = HostBody['hypervisor'];
      switch (kind) {
        case 'az': {
          const body: AZBody = {
            code: f_code, name: f_name, region: f_region,
            status: f_status as Status, uuid: '',
          };
          if (mode === 'create') await createAZ(body);
          else await updateAZ(String(initial?.uuid ?? ''), body);
          break;
        }
        case 'rack': {
          const body: RackBody = {
            code: f_code, az: f_az, position: f_position,
            height_u: f_rack_height_u,
            status: f_status as Status, uuid: '',
          };
          if (mode === 'create') await createRack(body);
          else await updateRack(String(initial?.uuid ?? ''), body);
          break;
        }
        case 'host': {
          const body: HostBody = {
            name: f_name, az: f_az, rack: f_rack,
            arch: f_arch as Arch, hypervisor: f_hyper as Hyper,
            gpu: f_gpu,
            position_u: f_position_u,
            height_u: f_height_u,
            status: f_status as Status, uuid: '',
          };
          if (mode === 'create') await createHost(body);
          else await updateHost(String(initial?.uuid ?? ''), body);
          break;
        }
      }
      onSave();
    } catch (e) {
      err = String(e);
    } finally {
      saving = false;
    }
  }
</script>

{#if open}
  <div
    class="modal modal-open"
    role="dialog"
    aria-modal="true"
    aria-label="Inventory form"
  >
    <div class="modal-box max-w-md">
      <h3 class="text-lg font-bold capitalize">
        {mode} {kind === 'az' ? 'availability zone' : kind}
      </h3>

      <form class="mt-4 space-y-3" onsubmit={submit}>
        {#if kind === 'az'}
          <label class="form-control w-full">
            <span class="label-text">Code</span>
            <input class="input input-bordered input-sm" required
              bind:value={f_code} disabled={mode === 'edit'} placeholder="DC-D"/>
            {#if mode === 'edit'}
              <span class="label-text-alt text-base-content/40">immutable</span>
            {/if}
          </label>
          <label class="form-control w-full">
            <span class="label-text">Name</span>
            <input class="input input-bordered input-sm" bind:value={f_name}
              placeholder="Datacenter Delta"/>
          </label>
          <label class="form-control w-full">
            <span class="label-text">Region</span>
            <input class="input input-bordered input-sm" bind:value={f_region}
              placeholder="eu-central-1"/>
          </label>
        {:else if kind === 'rack'}
          <label class="form-control w-full">
            <span class="label-text">AZ</span>
            <select class="select select-bordered select-sm" required
              bind:value={f_az} disabled={mode === 'edit'}>
              {#each azs as az (az.uuid ?? az.code)}
                <option value={String(az.code ?? '')}>{az.code}</option>
              {/each}
            </select>
          </label>
          <div class="grid grid-cols-2 gap-3">
            <label class="form-control w-full">
              <span class="label-text">Code</span>
              <input class="input input-bordered input-sm" required
                bind:value={f_code} disabled={mode === 'edit'} placeholder="R4"/>
            </label>
            <label class="form-control w-full">
              <span class="label-text">Height (U)</span>
              <input class="input input-bordered input-sm tabular-nums" type="number"
                min="1" max="60"
                bind:value={f_rack_height_u}/>
              <span class="label-text-alt text-base-content/40">42 = open frame · 24 = half height</span>
            </label>
          </div>
          <label class="form-control w-full">
            <span class="label-text">Position</span>
            <input class="input input-bordered input-sm" bind:value={f_position}
              placeholder="row3-col2"/>
          </label>
        {:else if kind === 'host'}
          <label class="form-control w-full">
            <span class="label-text">Hostname</span>
            <input class="input input-bordered input-sm" required
              bind:value={f_name} disabled={mode === 'edit'} placeholder="dc-a-r1-h3"/>
          </label>
          <div class="grid grid-cols-2 gap-3">
            <label class="form-control w-full">
              <span class="label-text">AZ</span>
              <select class="select select-bordered select-sm" required
                bind:value={f_az} disabled={mode === 'edit'}>
                {#each azs as az (az.uuid ?? az.code)}
                  <option value={String(az.code ?? '')}>{az.code}</option>
                {/each}
              </select>
            </label>
            <label class="form-control w-full">
              <span class="label-text">Rack</span>
              <select class="select select-bordered select-sm" required
                bind:value={f_rack} disabled={mode === 'edit'}>
                {#each racksForAZ as r (r.uuid ?? r.code)}
                  <option value={String(r.code ?? '')}>{r.code}</option>
                {/each}
              </select>
            </label>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <label class="form-control w-full">
              <span class="label-text">Arch</span>
              <select class="select select-bordered select-sm" bind:value={f_arch}>
                <option value="amd64">amd64</option>
                <option value="arm64">arm64</option>
                <option value="riscv64">riscv64</option>
                <option value="loong64">loong64</option>
              </select>
            </label>
            <label class="form-control w-full">
              <span class="label-text">Hypervisor</span>
              <select class="select select-bordered select-sm" bind:value={f_hyper}>
                <option value="qemu-kvm">qemu-kvm</option>
                <option value="apple-vz">apple-vz</option>
              </select>
            </label>
          </div>
          <label class="form-control w-full">
            <span class="label-text">GPU</span>
            <input class="input input-bordered input-sm" bind:value={f_gpu}
              placeholder="2×H200-141G or empty"/>
          </label>
          <div class="grid grid-cols-2 gap-3">
            <label class="form-control w-full">
              <span class="label-text">Top U slot</span>
              <input class="input input-bordered input-sm tabular-nums" type="number"
                min="0" max="60"
                bind:value={f_position_u}/>
              <span class="label-text-alt text-base-content/40">1 = top · 0 = auto-pack</span>
            </label>
            <label class="form-control w-full">
              <span class="label-text">Chassis size (U)</span>
              <input class="input input-bordered input-sm tabular-nums" type="number"
                min="1" max="20"
                bind:value={f_height_u}/>
              <span class="label-text-alt text-base-content/40">1U / 2U / 4U DGX-class …</span>
            </label>
          </div>
        {/if}

        <label class="form-control w-full">
          <span class="label-text">Status</span>
          <select class="select select-bordered select-sm" bind:value={f_status}>
            <option value="active">active</option>
            <option value="draining">draining</option>
            <option value="down">down</option>
            <option value="provisioning">provisioning</option>
          </select>
        </label>

        {#if err}
          <div class="alert alert-error py-2 text-xs">{err}</div>
        {/if}

        <div class="modal-action">
          <button type="button" class="btn btn-sm btn-ghost" onclick={onClose}>Cancel</button>
          <button type="submit" class="btn btn-sm btn-primary" disabled={saving}>
            {#if saving}
              <span class="loading loading-spinner loading-xs"></span>
            {/if}
            {mode === 'create' ? 'Create' : 'Save'}
          </button>
        </div>
      </form>
    </div>

    <button type="button" class="modal-backdrop" onclick={onClose} aria-label="Close">close</button>
  </div>
{/if}
