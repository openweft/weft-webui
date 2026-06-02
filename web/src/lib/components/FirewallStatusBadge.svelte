<!--
  FirewallStatusBadge — compact pill rendering the live nftables
  state for one VM. Reads firewallStatusByVM (derived from the
  singleton SSE stream) and falls back to a "waiting…" pill until
  the first agent tick lands (≤ 10 s after VM boot, or 35 s of
  silence to be classed "stale").

  Usage : <FirewallStatusBadge vmUUID={row.uuid} />

  Visual contract :
    * Healthy + N user rules : green pill "Firewall: N rules"
    * Healthy + 0 user rules : amber pill "Firewall: default-deny"
                               (only ct established + lo accept)
    * No table installed yet  : grey pill "Firewall: pending"
    * Degraded                : red pill "Firewall: error" (LastError on hover)
    * Stale publish > 35 s    : grey pill "Firewall: stale"
    * Never seen              : hidden (component renders nothing)

  Kept dep-free of icons + daisy themes ; uses semantic colour
  classes the rest of the SPA already styles.
-->
<script lang="ts">
  import { firewallStatusByVM, isStale, FIREWALL_DEFAULT_RULE_COUNT, type FirewallStatus } from '../firewallStatus';

  let { vmUUID }: { vmUUID: string } = $props();

  // Re-tick every 5 s so the "stale" badge transitions on time
  // even when no new event lands.
  let now = $state(Math.floor(Date.now() / 1000));
  let timer: ReturnType<typeof setInterval> | null = null;
  $effect(() => {
    timer = setInterval(() => { now = Math.floor(Date.now() / 1000); }, 5_000);
    return () => { if (timer) clearInterval(timer); };
  });

  let status = $derived<FirewallStatus | undefined>($firewallStatusByVM[vmUUID]);
  let stale = $derived(isStale(status, now));

  let cls = $derived(
    !status ? 'hidden' :
    stale ? 'badge badge-ghost' :
    status.overall === 'Degraded' ? 'badge badge-error' :
    !status.tableInstalled ? 'badge badge-ghost' :
    userRuleCount(status) === 0 ? 'badge badge-warning' :
    'badge badge-success'
  );

  let label = $derived(
    !status ? '' :
    stale ? 'Firewall: stale' :
    status.overall === 'Degraded' ? 'Firewall: error' :
    !status.tableInstalled ? 'Firewall: pending' :
    userRuleCount(status) === 0 ? 'Firewall: default-deny' :
    `Firewall: ${userRuleCount(status)} rule${userRuleCount(status) === 1 ? '' : 's'}`
  );

  function userRuleCount(s: FirewallStatus): number {
    return Math.max(0, s.rulesInstalled - FIREWALL_DEFAULT_RULE_COUNT);
  }
</script>

{#if status}
  <span class={cls} title={status.lastError || `Last seen: ${ageHuman(status.publishedAtUnix, now)}`}>
    {label}
  </span>
{/if}

<script module lang="ts">
  function ageHuman(stampUnix: number, nowUnix: number): string {
    if (stampUnix === 0) return 'never';
    const sec = Math.max(0, nowUnix - stampUnix);
    if (sec < 60) return `${sec}s ago`;
    if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
    return `${Math.floor(sec / 3600)}h ago`;
  }
</script>
