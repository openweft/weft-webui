import { describe, it, expect, beforeEach, vi } from 'vitest';
import { get } from 'svelte/store';

// endpoints.ts reads window.__WEFT_ENDPOINTS__ at import time and keeps
// module-level state, so each test resets the module registry and
// re-imports after seeding (or clearing) the injected config.
async function loadModule(quarantineMs = 1000) {
  (window as any).__WEFT_ENDPOINTS__ = {
    quarantineMs,
    endpoints: [
      { name: 'DC-A', url: 'http://a.test' },
      { name: 'DC-B', url: 'http://b.test' },
      { name: 'DC-C', url: 'http://c.test' },
    ],
  };
  vi.resetModules();
  return await import('./endpoints');
}

describe('endpoints failover supervisor', () => {
  beforeEach(() => {
    delete (window as any).__WEFT_ENDPOINTS__;
    vi.resetModules();
  });

  it('is inert with no injected endpoints (browser mode)', async () => {
    const m = await import('./endpoints');
    expect(m.endpointsEnabled).toBe(false);
    expect(m.activeBase()).toBe('');
    expect(m.withBase('/api/x')).toBe('/api/x');
    expect(m.rotate(0)).toBe(false);
  });

  it('prefixes the active DC origin onto API paths', async () => {
    const m = await loadModule();
    expect(m.endpointsEnabled).toBe(true);
    expect(m.activeBase()).toBe('http://a.test');
    expect(m.withBase('/api/events')).toBe('http://a.test/api/events');
  });

  it('rotates to the next healthy DC on failure and raises the banner', async () => {
    const m = await loadModule();
    expect(m.rotate(0)).toBe(true);
    expect(m.activeBase()).toBe('http://b.test');
    const s = get(m.failover);
    expect(s.switched).toBe(true);
    expect(s.fromName).toBe('DC-A');
    expect(s.toName).toBe('DC-B');
  });

  it('quarantines a failed DC (anti-flap) until the hold-down elapses', async () => {
    const m = await loadModule(1000);
    // A fails at t=0 -> B ; B fails at t=100 -> C ; C fails at t=200.
    expect(m.rotate(0)).toBe(true); // -> B
    expect(m.rotate(100)).toBe(true); // -> C
    // At t=200 all three are quarantined (A until 1000, B until 1100,
    // C until 1200) -> nowhere new, allDown.
    expect(m.rotate(200)).toBe(false);
    expect(get(m.failover).allDown).toBe(true);
    // A's hold-down (1000ms from t=0) has elapsed by t=1050 -> A is
    // promotable again, so we don't keep whipsawing.
    expect(m.rotate(1050)).toBe(true);
    expect(m.activeBase()).toBe('http://a.test');
  });

  it('clears the banner state on a success', async () => {
    const m = await loadModule();
    m.rotate(0); // switched -> B, also quarantines A
    m.noteSuccess('http://b.test');
    // allDown stays false; switched stays until dismissed.
    expect(get(m.failover).allDown).toBe(false);
    m.dismissFailover();
    expect(get(m.failover).switched).toBe(false);
  });
});
