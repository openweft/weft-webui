<script lang="ts">
  // Tiny toast stack in the bottom-right showing the last N platform
  // events. Each toast auto-dismisses after ~5 s ; the operator can
  // click ✕ to dismiss earlier. The connection indicator above the
  // stack flips colour when the SSE stream errors out.
  import { onMount, onDestroy } from 'svelte';
  import { lastEvents, eventsConnection, startEventsStream, stopEventsStream, type PlatformEvent } from '../events';

  // Local display queue : we copy from the store so we can fade
  // individual toasts out without mutating the canonical history.
  interface Visible { id: number; e: PlatformEvent; dying: boolean }
  let visible = $state<Visible[]>([]);
  let nextId = 0;
  let connection = $state<'idle' | 'open' | 'error'>('idle');

  // Subscribe imperatively so we can react to *new* events only,
  // not the entire history.
  let seen = 0;
  let unsubEvents: () => void;
  let unsubConn: () => void;
  onMount(() => {
    startEventsStream();
    unsubEvents = lastEvents.subscribe((all) => {
      // Each new event lands at index 0 ; everything at [0..(len - seen)) is new.
      const newCount = all.length - seen;
      for (let i = newCount - 1; i >= 0; i--) {
        push(all[i]);
      }
      seen = all.length;
    });
    unsubConn = eventsConnection.subscribe((c) => (connection = c));
  });
  onDestroy(() => { unsubEvents?.(); unsubConn?.(); stopEventsStream(); });

  function push(e: PlatformEvent) {
    const id = nextId++;
    visible = [{ id, e, dying: false }, ...visible].slice(0, 5);
    // Mark for dismissal after 5 s ; the CSS class fades it.
    setTimeout(() => {
      visible = visible.map((v) => (v.id === id ? { ...v, dying: true } : v));
      setTimeout(() => { visible = visible.filter((v) => v.id !== id); }, 300);
    }, 5000);
  }

  function dismiss(id: number) {
    visible = visible.filter((v) => v.id !== id);
  }

  function kindClass(kind: string): string {
    if (kind.startsWith('vm.state.')) return 'border-success';
    if (kind.startsWith('lb.') || kind.startsWith('dns.')) return 'border-info';
    if (kind.startsWith('scheduling-rule.')) return 'border-primary';
    if (kind.includes('.error') || kind.includes('.failed')) return 'border-error';
    return 'border-base-300';
  }
</script>

<div class="pointer-events-none fixed bottom-4 right-4 z-30 flex w-80 flex-col gap-1.5">
  {#if connection === 'error'}
    <div class="pointer-events-auto rounded-box border border-error bg-base-100 px-3 py-1 text-xs text-error">
      events stream disconnected — retrying
    </div>
  {/if}

  {#each visible as v (v.id)}
    <div
      role="status"
      class="pointer-events-auto flex items-start gap-2 rounded-box border-l-4 bg-base-100 p-2 text-xs shadow transition-opacity {kindClass(v.e.kind)}"
      class:opacity-0={v.dying}
    >
      <div class="grow truncate">
        <div class="font-mono text-[11px]">{v.e.kind}</div>
        <div class="truncate text-base-content/70">
          {v.e.subject}
          {#if v.e.project}<span class="text-base-content/40">· {v.e.project}</span>{/if}
        </div>
      </div>
      <button class="opacity-60 hover:opacity-100" aria-label="Dismiss" onclick={() => dismiss(v.id)}>✕</button>
    </div>
  {/each}
</div>
