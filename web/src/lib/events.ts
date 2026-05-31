// events.ts — singleton EventSource bridge to /api/events. Components
// subscribe via the exported store ; one connection per browser tab,
// reconnect handled by EventSource itself.

import { writable, type Writable } from 'svelte/store';
import { withBase, rotate, endpointsEnabled } from './endpoints';

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

// Longer-lived buffer for the Activity feed view. Capped at FEED_KEEP
// to avoid unbounded growth in a long-running tab ; the operator can
// click "Clear" in the Activity page to drop the buffer.
const FEED_KEEP = 500;
export const eventFeed: Writable<PlatformEvent[]> = writable([]);
export function clearEventFeed() { eventFeed.set([]); }

// State of the underlying connection ; the SPA can show a small
// indicator if it ever flips to `error`.
export const eventsConnection: Writable<'idle' | 'open' | 'error'> = writable('idle');

let es: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

export function startEventsStream() {
  if (es) return; // already open
  eventsConnection.set('idle');
  openStream();
}

// openStream (re)opens the singleton EventSource against the currently
// active DC. In a plain browser this is just '/api/events' and the
// browser's own reconnect logic handles transient drops. Inside a
// native shell with multiple DC origins, EventSource won't chase an
// origin change on its own — so on error we rotate to the next healthy
// DC and reopen there, mirroring the REST client's failover.
function openStream() {
  if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
  // Default = no filter ; future caller can pass kindPrefixes.
  es = new EventSource(withBase('/api/events'));
  es.onopen = () => eventsConnection.set('open');
  es.onerror = () => {
    eventsConnection.set('error');
    if (!endpointsEnabled || !es) return; // browser self-reconnects
    es.close();
    es = null;
    rotate(); // advance to next healthy DC (anti-flap aware)
    reconnectTimer = setTimeout(openStream, 1_000);
  };
  es.onmessage = (ev) => {
    try {
      const e = JSON.parse(ev.data) as PlatformEvent;
      lastEvents.update((xs) => {
        const next = [e, ...xs];
        if (next.length > KEEP) next.length = KEEP;
        return next;
      });
      eventFeed.update((xs) => {
        const next = [e, ...xs];
        if (next.length > FEED_KEEP) next.length = FEED_KEEP;
        return next;
      });
    } catch { /* ignore malformed frames */ }
  };
}

export function stopEventsStream() {
  if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
  if (!es) return;
  es.close();
  es = null;
  eventsConnection.set('idle');
}

// eventToResource maps a platform-event kind to the resource id whose
// table should refresh when it lands. nil → unrelated event (just
// surface as a toast). The match is by prefix on the canonical
// `<noun>.<verb>` shape weft-agent uses.
const KIND_TO_RESOURCE: { prefix: string; id: string }[] = [
  { prefix: 'vm.',               id: 'microvms' },
  { prefix: 'microvm.',          id: 'microvms' },
  { prefix: 'volume.',           id: 'volumes' },
  { prefix: 'network.',          id: 'networks' },
  { prefix: 'security-group.',   id: 'security-groups' },
  { prefix: 'lb.',               id: 'loadbalancers' },
  { prefix: 'loadbalancer.',     id: 'loadbalancers' },
  { prefix: 'router.',           id: 'routers' },
  { prefix: 'dns.zone.',         id: 'dns-zones' },
  { prefix: 'dns.record.',       id: 'dns-records' },
  { prefix: 'dns.',              id: 'dns-records' },
  { prefix: 'floating-ip.',      id: 'floating-ips' },
  { prefix: 'fip.',              id: 'floating-ips' },
  { prefix: 'scheduling-rule.',  id: 'scheduling-rules' },
  { prefix: 'tenant.',           id: 'tenants' },
  { prefix: 'project.',          id: 'projects' },
  { prefix: 'user.',             id: 'users' },
  { prefix: 'share.',            id: 'shares' },
  { prefix: 'host.',             id: 'hosts' },
];

export function eventToResource(kind: string): string | null {
  for (const m of KIND_TO_RESOURCE) {
    if (kind.startsWith(m.prefix)) return m.id;
  }
  return null;
}

// openScopedEvents : independent EventSource, NOT the singleton.
// Used by drawer components that want a per-subject stream alongside
// the global toast feed. Caller is responsible for close().
export function openScopedEvents(opts: { kindPrefix?: string; subject?: string; project?: string }): {
  source: EventSource;
  close: () => void;
} {
  const params = new URLSearchParams();
  if (opts.kindPrefix) params.append('kind', opts.kindPrefix);
  if (opts.subject) params.set('subject', opts.subject);
  if (opts.project) params.set('project', opts.project);
  const url = withBase(`/api/events${params.toString() ? `?${params}` : ''}`);
  const source = new EventSource(url);
  return { source, close: () => source.close() };
}
