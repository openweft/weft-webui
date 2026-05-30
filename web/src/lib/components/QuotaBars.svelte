<script lang="ts">
  // Visual : one row per quota dimension with a used/cap progress bar.
  // Used by the tenant quotas card (read-only summary), by the
  // edit-project-quota modal (to show tenant remaining as the user
  // slides), and by the tenant-quota modal (no remaining visible —
  // the cap IS the cap).
  //
  // `extra` overlays a second value on top of used (e.g. "+ requested")
  // so the modal can show "current used + my new value" against the
  // tenant cap. Bars switch to warning at ≥75% and error at ≥90%.
  import { QUOTA_DIMS, type QuotaBars as Bars, type Quotas, type QuotaDimMeta } from '../api';

  let {
    bars,
    extra = {},
    omit = [],
    pulseOver = false,
    dims: dimsProp,
  }: {
    bars: Bars;
    extra?: Partial<Record<keyof Quotas, number>>;
    omit?: (keyof Quotas)[];
    pulseOver?: boolean; // pulse the bar red when over capacity
    // Override the dimension list. Pass a plugin-filtered slice when
    // some dims depend on a plugin that isn't installed (e.g. drop
    // shares_gib when no shares plugin is active). Defaults to the
    // full QUOTA_DIMS for compatibility.
    dims?: QuotaDimMeta[];
  } = $props();

  const dims = $derived((dimsProp ?? QUOTA_DIMS).filter((d) => !omit.includes(d.key)));

  function color(used: number, cap: number): string {
    if (cap === 0) return 'progress-ghost';
    const r = used / cap;
    if (r >= 1) return 'progress-error';
    if (r >= 0.9) return 'progress-error';
    if (r >= 0.75) return 'progress-warning';
    return 'progress-success';
  }
  function pct(used: number, cap: number): number {
    return cap === 0 ? 0 : Math.min(100, Math.round((used / cap) * 100));
  }
  function fmt(v: number, d: QuotaDimMeta): string {
    return d.unit ? `${v} ${d.unit}` : `${v}`;
  }
</script>

<div class="grid gap-2 sm:grid-cols-2">
  {#each dims as d (d.key)}
    {@const b = bars[d.key as string] ?? { used: 0, cap: 0, free: 0 }}
    {@const x = (extra[d.key] ?? 0) as number}
    {@const total = b.used + x}
    {@const over = total > b.cap}
    <div class="rounded-box border border-base-300 p-2">
      <div class="flex items-baseline gap-2 text-xs">
        <span class="font-medium">{d.label}</span>
        <span class="ml-auto tabular-nums {over ? 'text-error font-medium' : 'text-base-content/60'}">
          {fmt(total, d)} / {fmt(b.cap, d)}
          {#if x > 0}<span class="text-base-content/40">(+{fmt(x, d)})</span>{/if}
        </span>
      </div>
      <progress
        class="progress mt-1 h-1.5 {color(total, b.cap)} {pulseOver && over ? 'animate-pulse' : ''}"
        value={pct(total, b.cap)}
        max="100"
      ></progress>
    </div>
  {/each}
</div>
