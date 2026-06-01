<script lang="ts">
  // Iso3DView — axonometric placement view extracted from
  // InventoryMapPage. Each AZ is anchored to a SCREEN position
  // (no cumulative iso skew between AZs) and racks live on a 2D
  // grid local to each AZ. Container is resizable (resize: both),
  // SVG pans on drag, zooms on wheel, AZ tiles are draggable.

  import type { Row } from '../api';

  let {
    azs,
    racksByAZ,
    hostsByRack,
    vmsByHost,
  }: {
    azs: Row[];
    racksByAZ: Map<string, Row[]>;
    hostsByRack: Map<string, Row[]>;
    vmsByHost: Map<string, Row[]>;
  } = $props();

  // ---- iso math --------------------------------------------------

  const COS30 = Math.cos(Math.PI / 6);
  const SIN30 = Math.sin(Math.PI / 6);

  function iso(gx: number, gy: number, gz: number = 0): { sx: number; sy: number } {
    return { sx: (gx - gy) * COS30, sy: (gx + gy) * SIN30 - gz };
  }

  // Geometry constants — tuned for a 2-D rack grid inside each AZ.
  const AZ_HALF       = 180;
  const RACK_FOOT     = 40;
  const RACK_GAP      = 14;
  const RACK_BAND_H   = 22;

  // SVG canvas + default per-AZ screen anchor.
  const SVG_W = 1600;
  const SVG_H = 900;
  const AZ_SPACING_X = 520;
  const ROW_Y = 380;

  function defaultAZCenter(idx: number, count: number): { x: number; y: number } {
    const total = Math.max(1, count);
    const startX = SVG_W / 2 - ((total - 1) * AZ_SPACING_X) / 2;
    return { x: startX + idx * AZ_SPACING_X, y: ROW_Y };
  }

  function rackGridPos(idx: number, n: number): { gx: number; gy: number } {
    const cols = Math.max(1, Math.ceil(Math.sqrt(n)));
    const col = idx % cols;
    const row = Math.floor(idx / cols);
    const span = (cols - 1) * (RACK_FOOT + RACK_GAP);
    return {
      gx: col * (RACK_FOOT + RACK_GAP) - span / 2,
      gy: row * (RACK_FOOT + RACK_GAP) - span / 2,
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
      case 'running':  return '#3b82f6';
      case 'starting': return '#f59e0b';
      case 'stopped':  return '#9ca3af';
      case 'failed':   return '#ef4444';
      default:         return '#6b7280';
    }
  }

  // Arch tint for the host band's top rim — status owns the fill,
  // arch owns a thin rim stripe so heterogeneous fleets read at a
  // glance without losing the status signal.
  function archStroke(arch: string): string {
    switch (arch) {
      case 'amd64':   return '#0284c7';
      case 'arm64':   return '#059669';
      case 'riscv64': return '#7c3aed';
      case 'loong64': return '#d97706';
      default:        return 'oklch(60% 0.02 240)';
    }
  }

  // ---- pan / zoom / drag ----------------------------------------

  let scale = $state(1);
  let tx = $state(0);
  let ty = $state(0);
  let azOverrides = $state<Record<string, { x: number; y: number }>>({});

  function azCenter(az: Row, idx: number): { x: number; y: number } {
    const key = String(az.uuid ?? az.code ?? idx);
    return azOverrides[key] ?? defaultAZCenter(idx, azs.length);
  }

  let svgEl = $state<SVGSVGElement>();
  let mode = $state<'none' | 'pan' | 'drag'>('none');
  let dragKey = '';
  let start = { x: 0, y: 0, tx: 0, ty: 0 };

  function vb(e: PointerEvent | WheelEvent): { x: number; y: number } {
    const ctm = svgEl?.getScreenCTM();
    if (!svgEl || !ctm) return { x: 0, y: 0 };
    const p = svgEl.createSVGPoint();
    p.x = e.clientX; p.y = e.clientY;
    const u = p.matrixTransform(ctm.inverse());
    return { x: u.x, y: u.y };
  }
  function graph(u: { x: number; y: number }): { x: number; y: number } {
    return { x: (u.x - tx) / scale, y: (u.y - ty) / scale };
  }

  function startPan(e: PointerEvent) {
    if (mode === 'drag') return;
    mode = 'pan';
    const u = vb(e);
    start = { x: u.x, y: u.y, tx, ty };
    svgEl?.setPointerCapture(e.pointerId);
  }
  function startDrag(e: PointerEvent, key: string) {
    e.stopPropagation();
    mode = 'drag';
    dragKey = key;
    svgEl?.setPointerCapture(e.pointerId);
  }
  function onMove(e: PointerEvent) {
    if (mode === 'pan') {
      const u = vb(e);
      tx = start.tx + (u.x - start.x);
      ty = start.ty + (u.y - start.y);
    } else if (mode === 'drag') {
      azOverrides = { ...azOverrides, [dragKey]: graph(vb(e)) };
    }
  }
  function endMove(e: PointerEvent) {
    mode = 'none'; dragKey = '';
    try { svgEl?.releasePointerCapture(e.pointerId); } catch { /* no-op */ }
  }
  function onWheel(e: WheelEvent) {
    e.preventDefault();
    const u = vb(e);
    const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12;
    const ns = Math.min(3, Math.max(0.4, scale * factor));
    tx = u.x - (u.x - tx) * (ns / scale);
    ty = u.y - (u.y - ty) * (ns / scale);
    scale = ns;
  }
  function zoom(factor: number) {
    const cx = SVG_W / 2, cy = SVG_H / 2;
    const ns = Math.min(3, Math.max(0.4, scale * factor));
    tx = cx - (cx - tx) * (ns / scale);
    ty = cy - (cy - ty) * (ns / scale);
    scale = ns;
  }
  function reset() { scale = 1; tx = 0; ty = 0; azOverrides = {}; }
</script>

<div
  class="relative mt-4 overflow-hidden rounded-box border border-base-300 bg-base-100"
  style="resize: both; height: 640px; min-height: 360px; min-width: 420px;"
>
  <div class="absolute right-3 top-3 z-10 flex gap-1">
    <button class="btn btn-xs btn-ghost btn-circle bg-base-100/80" aria-label="Zoom out" onclick={() => zoom(1 / 1.2)}>−</button>
    <button class="btn btn-xs btn-ghost btn-circle bg-base-100/80" aria-label="Zoom in" onclick={() => zoom(1.2)}>+</button>
    <button class="btn btn-xs btn-ghost bg-base-100/80" onclick={reset}>Reset</button>
  </div>

  <svg
    bind:this={svgEl}
    viewBox="0 0 {SVG_W} {SVG_H}"
    preserveAspectRatio="xMidYMid meet"
    class="block h-full w-full touch-none select-none"
    class:cursor-grab={mode === 'none'}
    class:cursor-grabbing={mode === 'pan'}
    onpointerdown={startPan}
    onpointermove={onMove}
    onpointerup={endMove}
    onpointercancel={endMove}
    onwheel={onWheel}
    role="application"
    aria-label="Isometric cluster map"
  >
    <defs>
      <linearGradient id="iso-az-floor" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%"  stop-color="oklch(96% 0.01 240)"/>
        <stop offset="100%" stop-color="oklch(90% 0.02 240)"/>
      </linearGradient>
      <linearGradient id="iso-rack-left" x1="0" y1="0" x2="1" y2="0">
        <stop offset="0%" stop-color="oklch(70% 0.05 240)"/>
        <stop offset="100%" stop-color="oklch(82% 0.04 240)"/>
      </linearGradient>
      <linearGradient id="iso-rack-right" x1="0" y1="0" x2="1" y2="0">
        <stop offset="0%" stop-color="oklch(88% 0.03 240)"/>
        <stop offset="100%" stop-color="oklch(78% 0.04 240)"/>
      </linearGradient>
    </defs>

    <g transform="translate({tx} {ty}) scale({scale})">
      {#each azs as az, azIdx (az.uuid ?? az.code ?? azIdx)}
        {@const ctr = azCenter(az, azIdx)}
        {@const azKey = String(az.uuid ?? az.code ?? azIdx)}
        {@const corners = [
          iso(-AZ_HALF, -AZ_HALF),
          iso( AZ_HALF, -AZ_HALF),
          iso( AZ_HALF,  AZ_HALF),
          iso(-AZ_HALF,  AZ_HALF),
        ]}
        {@const azRacks = racksByAZ.get(String(az.code ?? '')) ?? []}
        {@const labelPos = iso(-AZ_HALF + 18, -AZ_HALF + 14)}

        <g transform="translate({ctr.x} {ctr.y})">
          <g class="cursor-move" role="img" aria-label={String(az.code ?? '')}
            onpointerdown={(e) => startDrag(e, azKey)}>
            <polygon
              points="{corners.map((p) => `${p.sx},${p.sy}`).join(' ')}"
              fill="url(#iso-az-floor)"
              stroke="oklch(60% 0.05 240)"
              stroke-width="1.5"/>
            <text x={labelPos.sx} y={labelPos.sy}
              class="pointer-events-none fill-base-content text-[15px] font-semibold">
              {az.code}
              <tspan class="fill-base-content/50 font-normal"> · {az.name}</tspan>
            </text>
            <text x={labelPos.sx} y={labelPos.sy + 14}
              class="pointer-events-none fill-base-content/40 text-[10px]">
              {az.region ?? ''} · {azRacks.length} racks
            </text>
          </g>

          {#each azRacks as rack, rkIdx (rack.uuid ?? rkIdx)}
            {@const rg = rackGridPos(rkIdx, azRacks.length)}
            {@const hostsInRack = hostsByRack.get(String(az.code ?? '') + '|' + String(rack.code ?? '')) ?? []}
            {@const rackH = Math.max(1, hostsInRack.length) * RACK_BAND_H}
            {@const rackVMs = hostsInRack.flatMap((h) => vmsByHost.get(String(h.name ?? '')) ?? [])}
            {@const bot00 = iso(rg.gx,             rg.gy,             0)}
            {@const bot10 = iso(rg.gx + RACK_FOOT, rg.gy,             0)}
            {@const bot11 = iso(rg.gx + RACK_FOOT, rg.gy + RACK_FOOT, 0)}
            {@const bot01 = iso(rg.gx,             rg.gy + RACK_FOOT, 0)}
            {@const top00 = iso(rg.gx,             rg.gy,             rackH)}
            {@const top10 = iso(rg.gx + RACK_FOOT, rg.gy,             rackH)}
            {@const top11 = iso(rg.gx + RACK_FOOT, rg.gy + RACK_FOOT, rackH)}
            {@const top01 = iso(rg.gx,             rg.gy + RACK_FOOT, rackH)}
            {@const rackLabel = iso(rg.gx + RACK_FOOT / 2, rg.gy + RACK_FOOT / 2, rackH + 8)}

            <g class="rack">
              <polygon
                points="{bot01.sx},{bot01.sy} {bot00.sx},{bot00.sy} {top00.sx},{top00.sy} {top01.sx},{top01.sy}"
                fill="url(#iso-rack-left)"
                stroke="oklch(55% 0.05 240)" stroke-width="0.8"/>
              <polygon
                points="{bot00.sx},{bot00.sy} {bot10.sx},{bot10.sy} {top10.sx},{top10.sy} {top00.sx},{top00.sy}"
                fill="url(#iso-rack-right)"
                stroke="oklch(55% 0.05 240)" stroke-width="0.8"/>
              <polygon
                points="{top00.sx},{top00.sy} {top10.sx},{top10.sy} {top11.sx},{top11.sy} {top01.sx},{top01.sy}"
                fill="oklch(96% 0.01 240)"
                stroke="oklch(55% 0.05 240)" stroke-width="0.8"/>

              {#each hostsInRack as host, hostIdx (host.uuid ?? host.name ?? hostIdx)}
                {@const bandZ0 = hostIdx       * RACK_BAND_H}
                {@const bandZ1 = (hostIdx + 1) * RACK_BAND_H}
                {@const bL0 = iso(rg.gx, rg.gy + RACK_FOOT, bandZ0)}
                {@const bL1 = iso(rg.gx, rg.gy + RACK_FOOT, bandZ1)}
                {@const bR1 = iso(rg.gx, rg.gy,             bandZ1)}
                {@const bR0 = iso(rg.gx, rg.gy,             bandZ0)}
                <polygon
                  points="{bL0.sx},{bL0.sy} {bL1.sx},{bL1.sy} {bR1.sx},{bR1.sy} {bR0.sx},{bR0.sy}"
                  class={statusColor(String(host.status ?? ''))}
                  stroke-width="0.6"
                  opacity="0.85">
                  <title>{host.name} · {host.arch} · {host.hypervisor} · {(vmsByHost.get(String(host.name ?? '')) ?? []).length} VMs</title>
                </polygon>
                <!-- Arch rim : a 1.4px stripe along the band's top
                     edge so amd64 / arm64 / riscv64 / loong64 are
                     distinguishable without disturbing the status fill. -->
                <line x1={bL1.sx} y1={bL1.sy} x2={bR1.sx} y2={bR1.sy}
                  stroke={archStroke(String(host.arch ?? ''))}
                  stroke-width="1.4" stroke-linecap="round"/>
              {/each}

              {#each rackVMs.slice(0, 16) as vm, vmIdx (vm.uuid ?? vm.name ?? vmIdx)}
                {@const col = vmIdx % 4}
                {@const row = Math.floor(vmIdx / 4)}
                {@const dot = iso(rg.gx + 4 + col * 8, rg.gy + 4 + row * 8, rackH + 1)}
                <circle cx={dot.sx} cy={dot.sy} r="2.6"
                  fill={vmColor(String(vm.state ?? ''))}
                  stroke="white" stroke-width="0.6">
                  <title>{vm.name} · {vm.state}</title>
                </circle>
              {/each}
              {#if rackVMs.length > 16}
                {@const ov = iso(rg.gx + RACK_FOOT - 4, rg.gy + 4, rackH + 2)}
                <text x={ov.sx} y={ov.sy} text-anchor="end"
                  class="pointer-events-none fill-base-content/70 text-[9px]">+{rackVMs.length - 16}</text>
              {/if}

              <text x={rackLabel.sx} y={rackLabel.sy} text-anchor="middle"
                class="pointer-events-none fill-base-content/70 text-[10px] font-medium">
                {rack.code} · {hostsInRack.length}h
              </text>
            </g>
          {/each}
        </g>
      {/each}
    </g>
  </svg>
</div>
