<script lang="ts">
  // InventoryMapPage — isometric placement map of the cluster.
  //
  // Layout :
  //   AZ      → "ground plane" tile, side-by-side along X
  //   Rack    → vertical stack rising out of the AZ tile (Y)
  //   Host    → "card" slot inside the rack stack
  //   microVM → coloured dot inside the host card, packed in a grid
  //
  // Projection is classic axonometric : the iso transforms map
  // (gx, gy, gz) ∈ grid space to screen (sx, sy) via
  //
  //   sx =  (gx - gy) * cos(30°)
  //   sy =  (gx + gy) * sin(30°) - gz
  //
  // We pre-compute the projection per element. SVG transforms each
  // group rather than computing path data because re-rendering on
  // every event-stream tick is cheaper that way.
  //
  // Real-time signal : the page polls /api/resources every 5 s and
  // re-renders. SSE event toasts already exist for live updates ;
  // adding a per-VM subscriber here would be ideal — kept simple for
  // the initial cut.

  import { onMount, onDestroy } from 'svelte';
  import { getRowsPage, type Row, type ResourceMeta } from '../api';

  let { meta }: { meta: ResourceMeta } = $props();

  let azs   = $state<Row[]>([]);
  let racks = $state<Row[]>([]);
  let hosts = $state<Row[]>([]);
  let vms   = $state<Row[]>([]);
  let loadErr = $state('');
  let lastRefresh = $state<string>('');

  let pollTimer: ReturnType<typeof setInterval> | undefined;

  async function refresh() {
    try {
      const [a, r, h, v] = await Promise.all([
        getRowsPage('azs',      { limit: 500 }),
        getRowsPage('racks',    { limit: 500 }),
        getRowsPage('hosts',    { limit: 500 }),
        getRowsPage('microvms', { limit: 5000 }),
      ]);
      azs   = a.rows ?? [];
      racks = r.rows ?? [];
      hosts = h.rows ?? [];
      vms   = v.rows ?? [];
      loadErr = '';
      lastRefresh = new Date().toLocaleTimeString();
    } catch (e) {
      loadErr = String(e);
    }
  }

  onMount(() => {
    refresh();
    pollTimer = setInterval(refresh, 5000);
  });
  onDestroy(() => { if (pollTimer) clearInterval(pollTimer); });

  // ---- iso projection -------------------------------------------

  // Sizes in grid units ; the SVG viewBox is large enough to fit
  // 3 AZs × 3 racks × 2 hosts comfortably.
  const AZ_W    = 320;   // tile half-width in grid units
  const AZ_D    = 240;   // tile half-depth
  const AZ_GAP  = 80;
  const RACK_W  = 70;
  const RACK_D  = 50;
  const RACK_H  = 28;    // per-host band height
  const RACK_GAP_X = 20; // gap between racks along the AZ X-axis
  const RACK_GAP_Y = 20;

  const COS30 = Math.cos(Math.PI / 6);
  const SIN30 = Math.sin(Math.PI / 6);

  function iso(gx: number, gy: number, gz: number = 0): { sx: number; sy: number } {
    return {
      sx: (gx - gy) * COS30,
      sy: (gx + gy) * SIN30 - gz,
    };
  }

  // viewBox computed to fit 3 AZs side-by-side.
  const SVG_W = 1600;
  const SVG_H = 900;
  const ORIGIN_X = SVG_W / 2;
  const ORIGIN_Y = 200; // headroom for tall rack stacks

  // ---- joins ----------------------------------------------------

  let racksByAZ = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const r of racks) {
      const az = String(r.az ?? '');
      const arr = m.get(az) ?? [];
      arr.push(r);
      m.set(az, arr);
    }
    return m;
  });

  let hostsByRack = $derived.by(() => {
    // Hosts list their az + rack as string codes (not UUIDs in the
    // current seed) ; join via the composite "<az>|<rack>" key.
    const m = new Map<string, Row[]>();
    for (const h of hosts) {
      const k = String(h.az ?? '') + '|' + String(h.rack ?? '');
      const arr = m.get(k) ?? [];
      arr.push(h);
      m.set(k, arr);
    }
    return m;
  });

  let vmsByHost = $derived.by(() => {
    const m = new Map<string, Row[]>();
    for (const v of vms) {
      const host = String(v.host ?? '');
      if (!host) continue;
      const arr = m.get(host) ?? [];
      arr.push(v);
      m.set(host, arr);
    }
    return m;
  });

  let totalVMs = $derived(vms.length);

  // ---- per-AZ position ------------------------------------------

  // Lay AZs along the screen-X axis, equally spaced.
  function azOriginGrid(idx: number): { gx: number; gy: number } {
    // Center the row of AZs by shifting the iso-projected midpoint
    // back to ORIGIN_X via a simple symmetric offset. Each AZ tile
    // is 2·AZ_W wide on the grid, plus gap.
    const colSpan = 2 * AZ_W + AZ_GAP;
    const total = colSpan * Math.max(1, azs.length);
    return {
      // Position the AZ origin so its center maps to a unique screen
      // x. We start from a left-most offset and step right.
      gx: -total / 2 + idx * colSpan + AZ_W,
      gy: 0,
    };
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'active':       return 'fill-success/30 stroke-success';
      case 'draining':     return 'fill-warning/30 stroke-warning';
      case 'down':         return 'fill-error/30 stroke-error';
      case 'provisioning': return 'fill-info/30 stroke-info';
      default:             return 'fill-base-200 stroke-base-300';
    }
  }

  function vmColor(state: string): string {
    switch (state) {
      case 'running':  return '#3b82f6'; // blue
      case 'starting': return '#f59e0b'; // amber
      case 'stopped':  return '#9ca3af'; // gray
      case 'failed':   return '#ef4444'; // red
      default:         return '#6b7280';
    }
  }
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">{meta.label}</h2>
    <p class="text-sm text-base-content/60">
      Isometric placement map · {azs.length} AZ · {racks.length} racks ·
      {hosts.length} hosts · {totalVMs} microVMs
      {#if lastRefresh}
        · <span class="text-xs text-base-content/40">refreshed {lastRefresh}</span>
      {/if}
    </p>
  </div>
  <div class="ml-auto flex items-center gap-2">
    <button class="btn btn-sm btn-ghost gap-1" onclick={refresh} title="Force refresh">
      <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M3 12a9 9 0 1 0 3-6.7M3 4v5h5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      Refresh
    </button>
  </div>
</div>

{#if loadErr}
  <div class="alert alert-error mt-4 py-2 text-sm">{loadErr}</div>
{/if}

<div class="mt-4 rounded-box border border-base-300 bg-base-100 p-2 overflow-x-auto">
  <svg viewBox="0 0 {SVG_W} {SVG_H}" class="w-full" style="min-width: 800px;" role="img"
    aria-label="Isometric cluster map">
    <defs>
      <!-- Gradient for AZ tiles : subtle vertical fade so the tiles
           read as 3D planes rather than flat parallelograms. -->
      <linearGradient id="az-floor" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%"  stop-color="oklch(96% 0.01 240)"/>
        <stop offset="100%" stop-color="oklch(92% 0.02 240)"/>
      </linearGradient>
    </defs>

    <!-- Translate world origin to ORIGIN_X / ORIGIN_Y so all
         iso coordinates centre around it. -->
    <g transform="translate({ORIGIN_X} {ORIGIN_Y})">
      {#each azs as az, azIdx (az.uuid ?? az.code ?? azIdx)}
        {@const azOrigin = azOriginGrid(azIdx)}
        {@const azCorners = [
          iso(azOrigin.gx - AZ_W, azOrigin.gy - AZ_D),
          iso(azOrigin.gx + AZ_W, azOrigin.gy - AZ_D),
          iso(azOrigin.gx + AZ_W, azOrigin.gy + AZ_D),
          iso(azOrigin.gx - AZ_W, azOrigin.gy + AZ_D),
        ]}

        <!-- AZ ground tile + label. Const declarations must sit
             directly under the {#each azs} block, not inside the
             <g> element, per Svelte's @const placement rule. -->
        {@const azLabelPos = iso(azOrigin.gx - AZ_W + 20, azOrigin.gy - AZ_D + 12)}
        <g class="az">
          <polygon
            points="{azCorners.map((p) => `${p.sx},${p.sy}`).join(' ')}"
            fill="url(#az-floor)"
            stroke="oklch(70% 0.03 240)"
            stroke-width="1.5"/>
          <text x={azLabelPos.sx} y={azLabelPos.sy}
            class="fill-base-content/80 text-[14px] font-semibold">
            {az.code} <tspan class="fill-base-content/40 font-normal">· {az.name}</tspan>
          </text>
        </g>

        <!-- Racks inside the AZ -->
        {#each racksByAZ.get(String(az.code ?? '')) ?? [] as rack, rkIdx (rack.uuid ?? rkIdx)}
          {@const hostsInRack = hostsByRack.get(String(az.code ?? '') + '|' + String(rack.code ?? '')) ?? []}
          {@const rkGridX = azOrigin.gx - AZ_W + 60 + rkIdx * (RACK_W + RACK_GAP_X)}
          {@const rkGridY = azOrigin.gy - AZ_D + 60}
          {@const rackHeight = Math.max(1, hostsInRack.length) * RACK_H}
          {@const rkTopFront = iso(rkGridX,           rkGridY,           rackHeight)}
          {@const rkTopBack  = iso(rkGridX,           rkGridY + RACK_D,  rackHeight)}
          {@const rkTopRight = iso(rkGridX + RACK_W,  rkGridY + RACK_D,  rackHeight)}
          {@const rkTopLeft  = iso(rkGridX + RACK_W,  rkGridY,           rackHeight)}
          {@const rkBotFront = iso(rkGridX,           rkGridY,           0)}
          {@const rkBotBack  = iso(rkGridX,           rkGridY + RACK_D,  0)}
          {@const rkBotRight = iso(rkGridX + RACK_W,  rkGridY + RACK_D,  0)}
          {@const rkBotLeft  = iso(rkGridX + RACK_W,  rkGridY,           0)}
          {@const rackLabelPos = iso(rkGridX + RACK_W / 2, rkGridY + RACK_D / 2, rackHeight + 8)}

          <g class="rack">
            <!-- Left face (gx side, dimmer) -->
            <polygon
              points="{rkBotFront.sx},{rkBotFront.sy} {rkBotBack.sx},{rkBotBack.sy} {rkTopBack.sx},{rkTopBack.sy} {rkTopFront.sx},{rkTopFront.sy}"
              fill="oklch(85% 0.03 240)" stroke="oklch(60% 0.05 240)" stroke-width="1"/>
            <!-- Right face (gy side, brighter) -->
            <polygon
              points="{rkBotBack.sx},{rkBotBack.sy} {rkBotRight.sx},{rkBotRight.sy} {rkTopRight.sx},{rkTopRight.sy} {rkTopBack.sx},{rkTopBack.sy}"
              fill="oklch(90% 0.02 240)" stroke="oklch(60% 0.05 240)" stroke-width="1"/>
            <!-- Top face -->
            <polygon
              points="{rkTopFront.sx},{rkTopFront.sy} {rkTopBack.sx},{rkTopBack.sy} {rkTopRight.sx},{rkTopRight.sy} {rkTopLeft.sx},{rkTopLeft.sy}"
              fill="oklch(95% 0.01 240)" stroke="oklch(60% 0.05 240)" stroke-width="1"/>

            <!-- Per-host band : a thin slice of the rack's left face,
                 one per host. Color follows host status. -->
            {#each hostsInRack as host, hostIdx (host.uuid ?? host.name ?? hostIdx)}
              {@const bandBL = iso(rkGridX, rkGridY,          hostIdx       * RACK_H)}
              {@const bandTL = iso(rkGridX, rkGridY,          (hostIdx + 1) * RACK_H)}
              {@const bandTR = iso(rkGridX, rkGridY + RACK_D, (hostIdx + 1) * RACK_H)}
              {@const bandBR = iso(rkGridX, rkGridY + RACK_D, hostIdx       * RACK_H)}
              <polygon
                points="{bandBL.sx},{bandBL.sy} {bandTL.sx},{bandTL.sy} {bandTR.sx},{bandTR.sy} {bandBR.sx},{bandBR.sy}"
                class={statusColor(String(host.status ?? ''))}
                stroke-width="0.5"
                opacity="0.85">
                <title>
                  {host.name} · {host.arch} · {host.hypervisor}
                  · {(vmsByHost.get(String(host.name ?? '')) ?? []).length} VMs
                </title>
              </polygon>

              <!-- microVM dots on the band's top face. Pack them in a
                   row of up to N — wraps automatically if more. -->
              {@const hostVMs = vmsByHost.get(String(host.name ?? '')) ?? []}
              {@const perRow = 4}
              {#each hostVMs.slice(0, 8) as vm, vmIdx (vm.uuid ?? vm.name ?? vmIdx)}
                {@const col = vmIdx % perRow}
                {@const dotPos = iso(
                  rkGridX + 6 + col * 6,
                  rkGridY + 6 + Math.floor(vmIdx / perRow) * 8,
                  hostIdx * RACK_H + RACK_H - 4,
                )}
                <circle cx={dotPos.sx} cy={dotPos.sy} r="3"
                  fill={vmColor(String(vm.state ?? ''))}
                  stroke="white" stroke-width="0.5">
                  <title>{vm.name} · {vm.state} · {vm.image}</title>
                </circle>
              {/each}
              <!-- Overflow indicator when there are more than 8 VMs. -->
              {#if hostVMs.length > 8}
                {@const ovPos = iso(rkGridX + 30, rkGridY + 6, hostIdx * RACK_H + RACK_H - 4)}
                <text x={ovPos.sx} y={ovPos.sy + 4}
                  class="fill-base-content/70 text-[9px]" text-anchor="end">
                  +{hostVMs.length - 8}
                </text>
              {/if}
            {/each}

            <!-- Rack label on top — uses rackLabelPos hoisted above
                 the <g class="rack"> wrapper so the @const sits as
                 a direct child of {#each}. -->
            <text x={rackLabelPos.sx} y={rackLabelPos.sy} text-anchor="middle"
              class="fill-base-content/70 text-[10px] font-medium">
              {rack.code} · {hostsInRack.length}h
            </text>
          </g>
        {/each}
      {/each}
    </g>
  </svg>
</div>

<!-- Legend -->
<div class="mt-4 grid grid-cols-2 gap-4 sm:grid-cols-4">
  <div class="rounded-box border border-base-300 bg-base-100 p-3 text-xs">
    <div class="font-semibold mb-2 text-base-content/70">Host status</div>
    <div class="flex items-center gap-2"><span class="inline-block w-3 h-3 rounded bg-success/30 border border-success"></span> active</div>
    <div class="flex items-center gap-2"><span class="inline-block w-3 h-3 rounded bg-warning/30 border border-warning"></span> draining</div>
    <div class="flex items-center gap-2"><span class="inline-block w-3 h-3 rounded bg-error/30 border border-error"></span> down</div>
  </div>
  <div class="rounded-box border border-base-300 bg-base-100 p-3 text-xs">
    <div class="font-semibold mb-2 text-base-content/70">microVM state</div>
    <div class="flex items-center gap-2"><span class="inline-block w-3 h-3 rounded-full" style="background:#3b82f6"></span> running</div>
    <div class="flex items-center gap-2"><span class="inline-block w-3 h-3 rounded-full" style="background:#f59e0b"></span> starting</div>
    <div class="flex items-center gap-2"><span class="inline-block w-3 h-3 rounded-full" style="background:#9ca3af"></span> stopped</div>
    <div class="flex items-center gap-2"><span class="inline-block w-3 h-3 rounded-full" style="background:#ef4444"></span> failed</div>
  </div>
  <div class="rounded-box border border-base-300 bg-base-100 p-3 text-xs">
    <div class="font-semibold mb-2 text-base-content/70">Geometry</div>
    <div class="text-base-content/60">AZ = ground tile</div>
    <div class="text-base-content/60">Rack = vertical stack</div>
    <div class="text-base-content/60">Host = colored band</div>
    <div class="text-base-content/60">VM = dot on top</div>
  </div>
  <div class="rounded-box border border-base-300 bg-base-100 p-3 text-xs">
    <div class="font-semibold mb-2 text-base-content/70">Refresh</div>
    <div class="text-base-content/60">Polls /api/resources every 5 s.</div>
    <div class="text-base-content/60">Hover any element for tooltip.</div>
    <div class="text-base-content/60">8 VMs per host shown ; +N if more.</div>
  </div>
</div>
