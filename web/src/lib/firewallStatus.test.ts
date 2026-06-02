// Unit coverage for the pure derived store + helpers in
// firewallStatus.ts. The derived store is exercised by pushing
// synthetic PlatformEvents onto eventFeed and asserting the
// resulting map.

import { describe, expect, it, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { eventFeed, type PlatformEvent } from './events';
import {
  firewallStatusByVM,
  isStale,
  FIREWALL_DEFAULT_RULE_COUNT,
} from './firewallStatus';

function ev(subject: string, meta: Record<string, string>): PlatformEvent {
  return {
    ts: '2026-06-02T12:00:00Z',
    kind: 'firewall.status',
    subject,
    project: '',
    meta,
  };
}

describe('firewallStatusByVM', () => {
  beforeEach(() => {
    eventFeed.set([]);
  });

  it('builds vm → status map from firewall.status events', () => {
    // eventFeed is prepend-on-arrival : newer entries at index 0.
    eventFeed.set([
      ev('vm-1', { Overall: 'Healthy', RulesInstalled: '7', TableInstalled: 'true', PublishedAtUnix: '1700000010' }),
      ev('vm-2', { Overall: 'Degraded', RulesInstalled: '0', TableInstalled: 'false', PublishedAtUnix: '1700000005', LastError: 'netlink EAGAIN' }),
    ]);
    const m = get(firewallStatusByVM);
    expect(Object.keys(m).sort()).toEqual(['vm-1', 'vm-2']);
    expect(m['vm-1'].overall).toBe('Healthy');
    expect(m['vm-1'].rulesInstalled).toBe(7);
    expect(m['vm-1'].tableInstalled).toBe(true);
    expect(m['vm-2'].lastError).toBe('netlink EAGAIN');
    expect(m['vm-2'].tableInstalled).toBe(false);
  });

  it('newer status overwrites older for the same vm', () => {
    eventFeed.set([
      ev('vm-1', { Overall: 'Healthy', RulesInstalled: '12', PublishedAtUnix: '1700000020' }),
      ev('vm-1', { Overall: 'Healthy', RulesInstalled: '7',  PublishedAtUnix: '1700000010' }),
    ]);
    expect(get(firewallStatusByVM)['vm-1'].rulesInstalled).toBe(12);
  });

  it('ignores non-firewall.status events', () => {
    eventFeed.set([
      { ts: '', kind: 'vm.state.running', subject: 'vm-1', project: '', meta: {} },
      ev('vm-2', { Overall: 'Healthy', RulesInstalled: '3', PublishedAtUnix: '1700000010' }),
    ]);
    const m = get(firewallStatusByVM);
    expect(m['vm-1']).toBeUndefined();
    expect(m['vm-2']).toBeDefined();
  });

  it('ignores firewall.status without subject', () => {
    eventFeed.set([ev('', { Overall: 'Healthy' })]);
    expect(get(firewallStatusByVM)).toEqual({});
  });

  it('defaults rule count + stamp to 0 on missing meta', () => {
    eventFeed.set([ev('vm-x', { Overall: 'Healthy' })]);
    const s = get(firewallStatusByVM)['vm-x'];
    expect(s.rulesInstalled).toBe(0);
    expect(s.publishedAtUnix).toBe(0);
    expect(s.tableInstalled).toBe(false);
  });
});

describe('isStale', () => {
  it('treats missing status as stale', () => {
    expect(isStale(undefined, 1_000)).toBe(true);
  });
  it('treats publishedAtUnix === 0 as stale', () => {
    expect(isStale({ overall: 'Healthy', tableInstalled: true, rulesInstalled: 1, lastError: '', publishedAtUnix: 0 }, 1_000)).toBe(true);
  });
  it('fresh status (10 s ago) is not stale', () => {
    expect(isStale({ overall: 'Healthy', tableInstalled: true, rulesInstalled: 1, lastError: '', publishedAtUnix: 1_000 }, 1_010)).toBe(false);
  });
  it('40 s old status is stale (>35 s default)', () => {
    expect(isStale({ overall: 'Healthy', tableInstalled: true, rulesInstalled: 1, lastError: '', publishedAtUnix: 1_000 }, 1_040)).toBe(true);
  });
});

describe('FIREWALL_DEFAULT_RULE_COUNT', () => {
  it('matches reconciler defaults (ct + lo)', () => {
    // Reconciler installs `ct state established,related accept` +
    // `iifname "lo" accept` in the input chain unconditionally.
    expect(FIREWALL_DEFAULT_RULE_COUNT).toBe(2);
  });
});
