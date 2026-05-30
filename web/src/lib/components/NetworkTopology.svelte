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

  interface Hub {
    net: TopoNetwork;
    x: number;
    y: number;
    angle: number;
  }
  interface Placed {
    node: TopoNode;
    x: number;
    y: number;
    hx: number;
    hy: number;
  }

  let layout = $derived.by(() => {
    const n = networks.length;
    const hubR = n <= 1 ? 0 : 210;
    const hubs: Hub[] = networks.map((net, i) => {
      const angle = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(n, 1);
      return { net, x: CX + hubR * Math.cos(angle), y: CY + hubR * Math.sin(angle), angle };
    });
    const hubByName = new Map(hubs.map((h) => [h.net.id, h]));

    const placed: Placed[] = [];
    for (const h of hubs) {
      const mine = nodes.filter((nd) => nd.network === h.net.id);
      const m = mine.length;
      const spread = Math.min(Math.PI * 1.1, 0.6 + m * 0.22);
      mine.forEach((node, j) => {
        const a = h.angle + (m === 1 ? 0 : (j / (m - 1) - 0.5) * spread);
        const r = 96 + (j % 2) * 26;
        placed.push({ node, x: h.x + r * Math.cos(a), y: h.y + r * Math.sin(a), hx: h.x, hy: h.y });
      });
    }

    // WireGuard mesh : every hub peers with every other hub.
    const links: { x1: number; y1: number; x2: number; y2: number }[] = [];
    for (let i = 0; i < hubs.length; i++)
      for (let j = i + 1; j < hubs.length; j++)
        links.push({ x1: hubs[i].x, y1: hubs[i].y, x2: hubs[j].x, y2: hubs[j].y });

    return { hubs, placed, links, hubByName };
  });
</script>

<div>
  <h2 class="text-2xl font-bold">Topology</h2>
  <p class="text-sm text-base-content/60">
    Overlay networks meshed over WireGuard, with the microVMs &amp; VMs attached to each.
  </p>
</div>

<div class="relative mt-4 overflow-hidden rounded-box border border-base-300 bg-base-100">
  {#if loading}
    <div class="flex justify-center py-24"><span class="loading loading-spinner loading-lg"></span></div>
  {:else if error}
    <div class="alert alert-error m-4">{error}</div>
  {:else}
    <svg viewBox={`0 0 ${W} ${H}`} class="block w-full">
      <!-- mesh links between network hubs -->
      {#each layout.links as l (`${l.x1}-${l.y1}-${l.x2}-${l.y2}`)}
        <line x1={l.x1} y1={l.y1} x2={l.x2} y2={l.y2}
          stroke="#22d3ee" stroke-opacity="0.5" stroke-width="1.5" stroke-dasharray="5 5" />
      {/each}

      <!-- spokes : node → its hub -->
      {#each layout.placed as p (p.node.id)}
        <line x1={p.x} y1={p.y} x2={p.hx} y2={p.hy}
          stroke="currentColor" stroke-opacity="0.18" stroke-width="1" />
      {/each}

      <!-- hubs -->
      {#each layout.hubs as h (h.net.id)}
        <g>
          <circle cx={h.x} cy={h.y} r="28" fill="#0e7490" fill-opacity="0.14"
            stroke="#22d3ee" stroke-width="1.6" />
          <text x={h.x} y={h.y - 1} text-anchor="middle" class="fill-base-content text-[12px] font-semibold">
            {h.net.name}
          </text>
          <text x={h.x} y={h.y + 12} text-anchor="middle" class="fill-base-content/60 text-[9px]">
            {h.net.az}
          </text>
          <text x={h.x} y={h.y + 46} text-anchor="middle" class="fill-base-content/50 font-mono text-[9px]">
            {h.net.cidr}
          </text>
        </g>
      {/each}

      <!-- nodes -->
      {#each layout.placed as p (p.node.id)}
        <g
          role="img"
          aria-label={p.node.name}
          onmouseenter={() => (hovered = p.node)}
          onmouseleave={() => (hovered = null)}
          class="cursor-pointer"
        >
          <circle cx={p.x} cy={p.y} r={hovered?.id === p.node.id ? 11 : 8}
            fill={kindColor[p.node.kind] ?? '#94a3b8'}
            stroke={statusStroke(p.node.status)} stroke-width="2.5" />
          <text x={p.x} y={p.y + 20} text-anchor="middle" class="fill-base-content/70 text-[9px]">
            {p.node.name}
          </text>
        </g>
      {/each}
    </svg>

    <!-- hover info -->
    {#if hovered}
      <div class="absolute right-3 top-3 w-56 rounded-box border border-base-300 bg-base-100/95 p-3 text-sm shadow">
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

    <!-- legend -->
    <div class="absolute bottom-3 left-3 flex flex-wrap items-center gap-x-4 gap-y-1 rounded-box border border-base-300 bg-base-100/90 px-3 py-2 text-xs">
      <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-full" style="background:#38bdf8"></span>microVM</span>
      <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-full" style="background:#a855f7"></span>VM</span>
      <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-full" style="background:#6366f1"></span>infra</span>
      <span class="flex items-center gap-1"><span class="inline-block h-0.5 w-5" style="background:#22d3ee"></span>WireGuard mesh</span>
    </div>
  {/if}
</div>
