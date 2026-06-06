<script lang="ts">
  // Minimal canvas-2D line chart, designed for sub-minute polling
  // intervals (≤ 90 points) and one to three concurrent series. We
  // intentionally don't pull a chart library — these numbers are
  // small enough that drawing them by hand is shorter than wiring
  // up uPlot / Chart.js + their tree-shake quirks, and the result
  // ships zero extra KB.
  //
  // Inputs are time-aligned : each series is an array of values OR
  // the same length as the shared `points` array (we use index as
  // x — the polling cadence is constant, so an index axis renders
  // identically to a wall-clock axis at this resolution).
  //
  // Color cycles through the DaisyUI semantic palette so the chart
  // adapts to theme. Currently dark-mode optimised — light theme
  // looks fine too.

  let {
    series,
    yMax = undefined,
    yMin = 0,
    height = 90,
    yLabel = '',
    unit = '',
    formatY = (v: number) => `${v.toFixed(0)}`,
  }: {
    series: { name: string; values: number[]; color?: string }[];
    yMin?: number;
    yMax?: number; // undefined → auto-fit to data max
    height?: number;
    yLabel?: string;
    unit?: string;
    formatY?: (v: number) => string;
  } = $props();

  let canvas: HTMLCanvasElement | undefined = $state();
  let containerWidth = $state(600);

  // Compute the effective max once per render so the redraw loop
  // doesn't recompute it. `?? -Infinity` falls back to a sane value
  // for an empty data set — the chart renders a flat line.
  let effectiveMax = $derived.by(() => {
    if (yMax !== undefined) return yMax;
    let m = 0;
    for (const s of series) {
      for (const v of s.values) if (v > m) m = v;
    }
    // Pad 10% so the topmost point isn't glued to the frame.
    return m > 0 ? m * 1.1 : 1;
  });

  let dataLen = $derived(Math.max(0, ...series.map((s) => s.values.length)));

  // Default palette : matches DaisyUI's "info / success / warning /
  // error" semantic colors. Series can override via `color`.
  const palette = ['#3abff8', '#36d399', '#fbbd23', '#f87272'];

  function draw() {
    if (!canvas) return;
    const w = canvas.width = canvas.clientWidth;
    const h = canvas.height = height;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.clearRect(0, 0, w, h);

    // Background grid : three horizontal lines (0 / 50% / 100%) +
    // baseline. We don't draw vertical guides — the x-axis is
    // intentionally implicit (the chart label tells the story).
    ctx.strokeStyle = 'rgba(128,128,128,0.15)';
    ctx.lineWidth = 1;
    for (let i = 0; i <= 2; i++) {
      const y = (h * i) / 2 + 0.5;
      ctx.beginPath();
      ctx.moveTo(0, y);
      ctx.lineTo(w, y);
      ctx.stroke();
    }

    if (dataLen < 2) return;

    const range = effectiveMax - yMin;
    if (range <= 0) return;

    for (let si = 0; si < series.length; si++) {
      const s = series[si];
      if (s.values.length < 2) continue;
      ctx.strokeStyle = s.color ?? palette[si % palette.length];
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      for (let i = 0; i < s.values.length; i++) {
        const x = (i / (dataLen - 1)) * w;
        const y = h - ((s.values[i] - yMin) / range) * h;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      }
      ctx.stroke();
    }
  }

  // Resize observer so the canvas re-renders when the drawer
  // expands/shrinks. We also redraw on every props change via
  // $effect — the dependency tracker picks up `series` mutations.
  $effect(() => {
    if (!canvas) return;
    const ro = new ResizeObserver(() => {
      containerWidth = canvas?.clientWidth ?? containerWidth;
      draw();
    });
    ro.observe(canvas);
    return () => ro.disconnect();
  });

  $effect(() => {
    // depend on series content + max so updates re-paint.
    void series;
    void effectiveMax;
    void containerWidth;
    draw();
  });
</script>

<div class="space-y-1">
  <div class="flex items-baseline gap-3 text-xs">
    {#if yLabel}
      <span class="font-semibold">{yLabel}</span>
    {/if}
    <span class="text-base-content/50 tabular-nums">
      max ≈ {formatY(effectiveMax)}{unit}
    </span>
    <div class="ml-auto flex flex-wrap gap-2">
      {#each series as s, i (s.name)}
        <span class="inline-flex items-center gap-1 text-base-content/70">
          <span class="inline-block h-2 w-3 rounded-sm"
            style:background-color={s.color ?? palette[i % palette.length]}></span>
          <span>{s.name}</span>
          {#if s.values.length > 0}
            <span class="tabular-nums">{formatY(s.values[s.values.length - 1])}{unit}</span>
          {/if}
        </span>
      {/each}
    </div>
  </div>
  <canvas bind:this={canvas} class="w-full block rounded border border-base-300 bg-base-200/30" style:height={`${height}px`}></canvas>
</div>
