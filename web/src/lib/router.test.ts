// Unit coverage for the hash-router. The route + routeParams stores
// derive from location.hash via a `readable` whose start callback
// installs a hashchange listener — the listener is only live while
// the store has at least one subscriber, so the tests subscribe
// manually before mutating location.hash, then unsubscribe.

import { afterEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { go, route, routeParams } from './router';

afterEach(() => {
  // Reset so tests don't cascade their hash state into each other.
  location.hash = '';
});

// withSub keeps the store subscribed so its hashchange listener is
// active for the duration of fn().
function withSub<T>(fn: () => T): T {
  // Subscribe to both stores so the underlying `parsed` readable's
  // start callback fires (it's shared, so one subscriber is enough,
  // but pinning both makes intent explicit).
  const unsub1 = route.subscribe(() => {});
  const unsub2 = routeParams.subscribe(() => {});
  try {
    return fn();
  } finally {
    unsub1();
    unsub2();
  }
}

describe('go', () => {
  it('writes #/<id> for a plain id', () => {
    go('microvms');
    expect(location.hash).toBe('#/microvms');
  });

  it('appends ?key=val for params', () => {
    go('microvms', { detail: 'web-1' });
    expect(location.hash).toBe('#/microvms?detail=web-1');
  });

  it('preserves multiple params', () => {
    go('volumes', { detail: 'data', state: 'attached' });
    expect(location.hash).toMatch(/^#\/volumes\?/);
    // URLSearchParams ordering is insertion-order — both keys must be
    // present, but we don't assert which one comes first.
    expect(location.hash).toContain('detail=data');
    expect(location.hash).toContain('state=attached');
  });

  it('encodes special characters in param values', () => {
    go('shares', { detail: 'team data/2026' });
    // URLSearchParams encodes / as %2F and space as +.
    expect(location.hash).toContain('detail=team+data%2F2026');
  });

  it('routes to home with empty id', () => {
    go('');
    expect(location.hash).toBe('#/');
  });
});

describe('route + routeParams stores', () => {
  it('parses #/<id> into route = "<id>" with empty params', () => {
    withSub(() => {
      location.hash = '#/networks';
      dispatchEvent(new Event('hashchange'));
      expect(get(route)).toBe('networks');
      expect(get(routeParams)).toEqual({});
    });
  });

  it('parses #/<id>?key=val into route + params', () => {
    withSub(() => {
      location.hash = '#/microvms?detail=web-1';
      dispatchEvent(new Event('hashchange'));
      expect(get(route)).toBe('microvms');
      expect(get(routeParams)).toEqual({ detail: 'web-1' });
    });
  });

  it('decodes percent-encoded chars', () => {
    withSub(() => {
      location.hash = '#/shares?detail=team+data%2F2026';
      dispatchEvent(new Event('hashchange'));
      expect(get(route)).toBe('shares');
      expect(get(routeParams).detail).toBe('team data/2026');
    });
  });
});
