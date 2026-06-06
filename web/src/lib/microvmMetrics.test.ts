// Unit coverage for the pure-store half of microvmMetrics : ring
// buffer push, capacity trim, per-VM isolation, clearVM. The
// polling loop itself isn't tested here — that path uses real
// timers + the network client ; e2e covers the wiring at the
// integration layer.

import { describe, expect, it, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import {
  metricsByVM, pushSample, clearVM, seriesForVM,
  METRICS_RING_CAPACITY,
} from './microvmMetrics';
import type { MetricsSnapshot } from './api';

function snap(at: number, cpu = 50): MetricsSnapshot {
  return {
    sampled_at_unix: at,
    cpu_percent: cpu,
    mem_used_mib: 512,
    mem_total_mib: 1024,
    net_rx_bps: 1000,
    net_tx_bps: 1000,
    disk_read_bps: 500,
    disk_write_bps: 500,
    uptime_seconds: at - 1_700_000_000,
    mock: true,
  };
}

describe('metricsByVM', () => {
  beforeEach(() => {
    metricsByVM.set({});
  });

  it('pushSample appends to the named VM ring', () => {
    pushSample('vm-a', snap(1_700_000_000, 10));
    pushSample('vm-a', snap(1_700_000_005, 12));
    const all = get(metricsByVM);
    expect(all['vm-a']).toBeDefined();
    expect(all['vm-a'].length).toBe(2);
    expect(all['vm-a'][0].cpu_percent).toBe(10);
    expect(all['vm-a'][1].cpu_percent).toBe(12);
  });

  it('keeps separate rings per VM', () => {
    pushSample('vm-a', snap(1, 10));
    pushSample('vm-b', snap(1, 20));
    pushSample('vm-a', snap(2, 11));
    const all = get(metricsByVM);
    expect(all['vm-a'].length).toBe(2);
    expect(all['vm-b'].length).toBe(1);
    expect(all['vm-a'][0].cpu_percent).toBe(10);
    expect(all['vm-b'][0].cpu_percent).toBe(20);
  });

  it('trims the ring at METRICS_RING_CAPACITY (oldest dropped)', () => {
    // Push capacity + 5 entries with monotonically increasing
    // sampled_at_unix ; the head should be the 6th push (capacity-th
    // oldest dropped).
    for (let i = 0; i < METRICS_RING_CAPACITY + 5; i++) {
      pushSample('vm-x', snap(i, i));
    }
    const ring = get(metricsByVM)['vm-x'];
    expect(ring.length).toBe(METRICS_RING_CAPACITY);
    // Oldest kept is i=5 ; newest is i=cap+4.
    expect(ring[0].cpu_percent).toBe(5);
    expect(ring[ring.length - 1].cpu_percent).toBe(METRICS_RING_CAPACITY + 4);
  });

  it('clearVM drops only the named VM', () => {
    pushSample('vm-a', snap(1));
    pushSample('vm-b', snap(1));
    clearVM('vm-a');
    const all = get(metricsByVM);
    expect(all['vm-a']).toBeUndefined();
    expect(all['vm-b']).toBeDefined();
  });

  it('clearVM is a no-op when the VM never had samples', () => {
    pushSample('vm-a', snap(1));
    const before = get(metricsByVM);
    clearVM('vm-never-seen');
    const after = get(metricsByVM);
    expect(after).toEqual(before);
  });

  it('seriesForVM exposes the ring as a derived store', () => {
    const s = seriesForVM('vm-derived');
    // Initially empty.
    expect(get(s)).toEqual([]);
    pushSample('vm-derived', snap(1, 42));
    expect(get(s).length).toBe(1);
    expect(get(s)[0].cpu_percent).toBe(42);
  });
});
