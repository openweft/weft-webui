<script lang="ts">
  // Inline SVG variant of public/weft-wordmark.svg + weft-icon.svg.
  // Warp + wordmark use currentColor so the logo inherits the parent's
  // text color — flips automatically with DaisyUI's data-theme. The
  // weft thread (#2563eb) stays brand-blue ; visible on both light and
  // dark themes.
  //
  // Replaces the <img src="/weft-wordmark.svg"> tag that wasn't
  // theme-aware (SVGs loaded as separate documents don't inherit
  // currentColor from the consuming page).

  let {
    variant = 'wordmark',
    class: klass = '',
  }: {
    /** 'wordmark' = icon + "Weft" text ; 'icon' = icon only. */
    variant?: 'wordmark' | 'icon';
    class?: string;
  } = $props();
</script>

{#if variant === 'wordmark'}
  <svg
    xmlns="http://www.w3.org/2000/svg"
    viewBox="0 0 480 128"
    role="img"
    aria-label="Weft"
    class={klass}
  >
    <title>Weft</title>
    <g transform="translate(16,16) scale(0.375)" stroke-linecap="round">
      <line x1="68" y1="40" x2="68" y2="216" stroke="currentColor" stroke-width="22" />
      <line x1="188" y1="40" x2="188" y2="216" stroke="currentColor" stroke-width="22" />
      <line x1="36" y1="128" x2="220" y2="128" stroke="#2563eb" stroke-width="22" />
      <line x1="128" y1="40" x2="128" y2="216" stroke="currentColor" stroke-width="22" />
    </g>
    <text
      x="136"
      y="92"
      font-family="Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif"
      font-size="88"
      font-weight="700"
      letter-spacing="-2"
      fill="currentColor">Weft</text>
  </svg>
{:else}
  <svg
    xmlns="http://www.w3.org/2000/svg"
    viewBox="0 0 256 256"
    role="img"
    aria-label="Weft logo A"
    class={klass}
  >
    <title>Weft</title>
    <g fill="none" stroke-linecap="round">
      <line x1="68" y1="40" x2="68" y2="216" stroke="currentColor" stroke-width="22" />
      <line x1="188" y1="40" x2="188" y2="216" stroke="currentColor" stroke-width="22" />
      <line x1="36" y1="128" x2="220" y2="128" stroke="#2563eb" stroke-width="22" />
      <line x1="128" y1="40" x2="128" y2="216" stroke="currentColor" stroke-width="22" />
    </g>
  </svg>
{/if}
