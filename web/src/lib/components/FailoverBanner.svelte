<script lang="ts">
  // FailoverBanner — surfaces multi-DC failover to the user, but only
  // when failover stops being invisible. In the common case a DC loss
  // is absorbed silently by the API client (it retries the next DC and
  // the page never reloads), so this banner shows nothing.
  //
  // It lights up in two cases :
  //   * `switched`  — the active DC changed (either the SPA rotated, or
  //     the native shell re-pointed its transport and called
  //     window.__weftFailoverNotice). A dismissible info banner.
  //   * `allDown`   — every known DC is unreachable. A persistent error
  //     banner; clears itself once any call succeeds again.
  //
  // In a plain browser (no native shell) `failover` never leaves its
  // idle state, so this renders nothing at all.
  import { failover, dismissFailover } from '../endpoints';
</script>

{#if $failover.allDown}
  <div class="fixed inset-x-0 top-0 z-50 flex justify-center px-4 pt-2">
    <div role="alert" class="alert alert-error w-full max-w-xl shadow-lg">
      <span class="loading loading-spinner loading-sm"></span>
      <span>All datacenters are unreachable — retrying…</span>
    </div>
  </div>
{:else if $failover.switched}
  <div class="fixed inset-x-0 top-0 z-50 flex justify-center px-4 pt-2">
    <div role="status" class="alert alert-info w-full max-w-xl shadow-lg">
      <span>
        Connection switched
        {#if $failover.fromName}from <strong>{$failover.fromName}</strong>{/if}
        to <strong>{$failover.toName}</strong> — a datacenter became unavailable.
      </span>
      <button class="btn btn-ghost btn-xs" onclick={dismissFailover} aria-label="Dismiss">✕</button>
    </div>
  </div>
{/if}
