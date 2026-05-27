<script lang="ts">
  import { getTopology, type TopoNetwork, type TopoNode } from '../api';

  let networks = $state<TopoNetwork[]>([]);
  let nodes = $state<TopoNode[]>([]);
  let loading = $state(true);
  let error = $state('');
  let hovered = $state<TopoNode | null>(null);

  $effect(() => {
    getTopology()
      .then((t) => {
        networks = t.networks;
        nodes = t.nodes;
      })
      .catch((e) => (error = String(e)))
      .finally(() => (loading = false));
  });

  const W = 1000;
  const H = 600;
  const CX = W / 2;
  const CY = 290;

  const kindColor: Record<string, string> = {
    microvm: '#38bdf8',
    instance: '#a855f7',
    infra: '#6366f1',
  };
  function statusStroke(s: string): string {
    if (['running', 'active', 'up'].includes(s)) return '#22c55e';
    if (['stopped', 'draining', 'disabled'].includes(s)) return '#f59e0b';
    return '#94a3b8';
  }

  // ---- base (computed) layout, keyed by id ----
  interface Pt {
    x: number;
    y: number;
  }
  let base = $derived.by(() => {
    const n = networks.length;
    const hubR = n <= 1 ? 0 : 210;
    const pts = new Map<string, Pt>();
    const hubAngle = new Map<string, number>();
    networks.forEach((net, i) => {
      const a = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(n, 1);
      pts.set(net.id, { x: CX + hubR * Math.cos(a), y: CY + hubR * Math.sin(a) });
      hubAngle.set(net.id, a);
    });
    for (const net of networks) {
      const mine = nodes.filter((nd) => nd.network === net.id);
      const m = mine.length;
      const spread = Math.min(Math.PI * 1.1, 0.6 + m * 0.22);
      const h = pts.get(net.id)!;
      const base = hubAngle.get(net.id)!;
      mine.forEach((node, j) => {
        const a = base + (m === 1 ? 0 : (j / (m - 1) - 0.5) * spread);
        const r = 96 + (j % 2) * 26;
        pts.set(node.id, { x: h.x + r * Math.cos(a), y: h.y + r * Math.sin(a) });
      });
    }
    return pts;
  });

  // ---- user overrides (dragged elements) ----
  let overrides = $state<Record<string, Pt>>({});
  function pos(id: string): Pt {
    return overrides[id] ?? base.get(id) ?? { x: CX, y: CY };
  }

  // ---- pan / zoom transform ----
  let scale = $state(1);
  let tx = $state(0);
  let ty = $state(0);

  let svgEl = $state<SVGSVGElement>();
  let mode = $state<'none' | 'pan' | 'drag'>('none');
  let dragId = '';
  let start = { x: 0, y: 0, tx: 0, ty: 0 };

  // pointer → viewBox coords
  function vb(e: PointerEvent | WheelEvent): Pt {
    const ctm = svgEl?.getScreenCTM();
    if (!svgEl || !ctm) return { x: 0, y: 0 };
    const p = svgEl.createSVGPoint();
    p.x = e.clientX;
    p.y = e.clientY;
    const u = p.matrixTransform(ctm.inverse());
    return { x: u.x, y: u.y };
  }
  // viewBox → graph (pre-transform) coords
  function graph(u: Pt): Pt {
    return { x: (u.x - tx) / scale, y: (u.y - ty) / scale };
  }

  function startPan(e: PointerEvent) {
    mode = 'pan';
    const u = vb(e);
    start = { x: u.x, y: u.y, tx, ty };
    svgEl?.setPointerCapture(e.pointerId);
  }
  function startDrag(e: PointerEvent, id: string) {
    e.stopPropagation();
    mode = 'drag';
    dragId = id;
    svgEl?.setPointerCapture(e.pointerId);
  }
  function onMove(e: PointerEvent) {
    if (mode === 'pan') {
      const u = vb(e);
      tx = start.tx + (u.x - start.x);
      ty = start.ty + (u.y - start.y);
    } else if (mode === 'drag') {
      overrides = { ...overrides, [dragId]: graph(vb(e)) };
    }
  }
  function endMove(e: PointerEvent) {
    mode = 'none';
    dragId = '';
    try {
      svgEl?.releasePointerCapture(e.pointerId);
    } catch {
      /* no-op */
    }
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
    const ns = Math.min(3, Math.max(0.4, scale * factor));
    // zoom around the centre of the viewBox
    tx = CX - (CX - tx) * (ns / scale);
    ty = CY - (CY - ty) * (ns / scale);
    scale = ns;
  }
  function reset() {
    scale = 1;
    tx = 0;
    ty = 0;
    overrides = {};
  }

  // mesh links between hub pairs
  let meshLinks = $derived.by(() => {
    const out: [string, string][] = [];
    for (let i = 0; i < networks.length; i++)
      for (let j = i + 1; j < networks.length; j++) out.push([networks[i].id, networks[j].id]);
    return out;
  });
</script>

<div class="flex items-center gap-3">
  <div>
    <h2 class="text-2xl font-bold">Topology</h2>
    <p class="text-sm text-base-content/60">
      Overlay networks meshed over WireGuard, with the microVMs &amp; VMs attached to each.
      Drag to pan, drag a node to rearrange, scroll to zoom.
    </p>
  </div>
</div>

<div
  class="relative mt-4 overflow-hidden rounded-box border border-base-300 bg-base-100"
  style="resize: vertical; height: 540px; min-height: 360px;"
>
  {#if loading}
    <div class="flex justify-center py-24"><span class="loading loading-spinner loading-lg"></span></div>
  {:else if error}
    <div class="alert alert-error m-4">{error}</div>
  {:else}
    <!-- toolbar -->
    <div class="absolute right-3 top-3 z-10 flex gap-1">
      <button class="btn btn-xs btn-ghost btn-circle bg-base-100/80" aria-label="Zoom out" onclick={() => zoom(1 / 1.2)}>−</button>
      <button class="btn btn-xs btn-ghost btn-circle bg-base-100/80" aria-label="Zoom in" onclick={() => zoom(1.2)}>+</button>
      <button class="btn btn-xs btn-ghost bg-base-100/80" onclick={reset}>Reset</button>
    </div>

    <svg
      bind:this={svgEl}
      viewBox={`0 0 ${W} ${H}`}
      class="block h-full w-full touch-none select-none"
      class:cursor-grab={mode === 'none'}
      class:cursor-grabbing={mode === 'pan'}
      onpointerdown={startPan}
      onpointermove={onMove}
      onpointerup={endMove}
      onpointercancel={endMove}
      onwheel={onWheel}
      role="application"
      aria-label="Network topology"
    >
      <g transform={`translate(${tx} ${ty}) scale(${scale})`}>
        <!-- WireGuard mesh between hubs -->
        {#each meshLinks as [a, b] (a + b)}
          {@const pa = pos(a)}
          {@const pb = pos(b)}
          <line x1={pa.x} y1={pa.y} x2={pb.x} y2={pb.y}
            stroke="#22d3ee" stroke-opacity="0.5" stroke-width="1.5" stroke-dasharray="5 5" />
        {/each}

        <!-- spokes -->
        {#each nodes as nd (nd.id)}
          {@const p = pos(nd.id)}
          {@const h = pos(nd.network)}
          <line x1={p.x} y1={p.y} x2={h.x} y2={h.y} stroke="currentColor" stroke-opacity="0.18" stroke-width="1" />
        {/each}

        <!-- hubs -->
        {#each networks as net (net.id)}
          {@const p = pos(net.id)}
          <g class="cursor-move" onpointerdown={(e) => startDrag(e, net.id)} role="img" aria-label={net.name}>
            <circle cx={p.x} cy={p.y} r="28" fill="#0e7490" fill-opacity="0.14" stroke="#22d3ee" stroke-width="1.6" />
            <text x={p.x} y={p.y - 1} text-anchor="middle" class="pointer-events-none fill-base-content text-[12px] font-semibold">{net.name}</text>
            <text x={p.x} y={p.y + 12} text-anchor="middle" class="pointer-events-none fill-base-content/60 text-[9px]">{net.az}</text>
            <text x={p.x} y={p.y + 46} text-anchor="middle" class="pointer-events-none fill-base-content/50 font-mono text-[9px]">{net.cidr}</text>
          </g>
        {/each}

        <!-- nodes -->
        {#each nodes as nd (nd.id)}
          {@const p = pos(nd.id)}
          <g class="cursor-move" role="img" aria-label={nd.name}
            onpointerdown={(e) => startDrag(e, nd.id)}
            onmouseenter={() => (hovered = nd)} onmouseleave={() => (hovered = null)}>
            <circle cx={p.x} cy={p.y} r={hovered?.id === nd.id ? 11 : 8}
              fill={kindColor[nd.kind] ?? '#94a3b8'} stroke={statusStroke(nd.status)} stroke-width="2.5" />
            <text x={p.x} y={p.y + 20} text-anchor="middle" class="pointer-events-none fill-base-content/70 text-[9px]">{nd.name}</text>
          </g>
        {/each}
      </g>
    </svg>

    {#if hovered}
      <div class="absolute left-3 top-3 z-10 w-56 rounded-box border border-base-300 bg-base-100/95 p-3 text-sm shadow">
        <div class="font-semibold">{hovered.name}</div>
        <dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <dt class="text-base-content/50">kind</dt><dd>{hovered.kind}</dd>
          <dt class="text-base-content/50">network</dt><dd>{hovered.network}</dd>
          <dt class="text-base-content/50">project</dt><dd>{hovered.project}</dd>
          <dt class="text-base-content/50">host</dt><dd>{hovered.host}</dd>
          <dt class="text-base-content/50">status</dt><dd>{hovered.status}</dd>
        </dl>
      </div>
    {/if}

    <div class="absolute bottom-3 left-3 z-10 flex flex-wrap items-center gap-x-4 gap-y-1 rounded-box border border-base-300 bg-base-100/90 px-3 py-2 text-xs">
      <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-full" style="background:#38bdf8"></span>microVM</span>
      <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-full" style="background:#a855f7"></span>VM</span>
      <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-full" style="background:#6366f1"></span>infra</span>
      <span class="flex items-center gap-1"><span class="inline-block h-0.5 w-5" style="background:#22d3ee"></span>WireGuard mesh</span>
    </div>
  {/if}
</div>
