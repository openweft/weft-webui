// events.ts — singleton EventSource bridge to /api/events. Components
// subscribe via the exported store ; one connection per browser tab,
// reconnect handled by EventSource itself.

import { writable, type Writable } from 'svelte/store';

export interface PlatformEvent {
  ts: string;        // RFC-3339
  kind: string;      // e.g. "vm.state.running"
  subject: string;   // e.g. VM name
  project: string;   // project UUID
  meta?: Record<string, string>;
}

// Keep the last N events for the toast bar. Older entries drop off the
// tail.
const KEEP = 20;
export const lastEvents: Writable<PlatformEvent[]> = writable([]);

// State of the underlying connection ; the SPA can show a small
// indicator if it ever flips to `error`.
export const eventsConnection: Writable<'idle' | 'open' | 'error'> = writable('idle');

let es: EventSource | null = null;

export function startEventsStream() {
  if (es) return; // already open
  eventsConnection.set('idle');
  // Default = no filter ; future caller can pass kindPrefixes.
  es = new EventSource('/api/events');
  es.onopen = () => eventsConnection.set('open');
  es.onerror = () => eventsConnection.set('error');
  es.onmessage = (ev) => {
    try {
      const e = JSON.parse(ev.data) as PlatformEvent;
      lastEvents.update((xs) => {
        const next = [e, ...xs];
        if (next.length > KEEP) next.length = KEEP;
        return next;
      });
    } catch { /* ignore malformed frames */ }
  };
}

export function stopEventsStream() {
  if (!es) return;
  es.close();
  es = null;
  eventsConnection.set('idle');
}
