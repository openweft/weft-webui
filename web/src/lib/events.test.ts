// Unit coverage for the pure-logic exports of events.ts. The
// EventSource side (startEventsStream / openScopedEvents) needs a
// browser EventSource implementation and is exercised end-to-end in
// the Go-side suite — not duplicated here.

import { describe, expect, it } from 'vitest';
import { eventToResource } from './events';

describe('eventToResource', () => {
  it.each([
    ['vm.state.running',          'microvms'],
    ['vm.created',                'microvms'],
    ['microvm.exited',            'microvms'],
    ['volume.attached',           'volumes'],
    ['network.created',           'networks'],
    ['security-group.updated',    'security-groups'],
    ['lb.health.down',            'loadbalancers'],
    ['loadbalancer.created',      'loadbalancers'],
    ['router.peer.up',            'routers'],
    // dns.zone.* matches dns.zone. before dns. — order in the prefix
    // table matters. This case locks the precedence.
    ['dns.zone.created',          'dns-zones'],
    ['dns.record.added',          'dns-records'],
    ['dns.lookup-failed',         'dns-records'],
    ['floating-ip.allocated',     'floating-ips'],
    ['fip.released',              'floating-ips'],
    ['scheduling-rule.compliant', 'scheduling-rules'],
    ['tenant.created',            'tenants'],
    ['project.deleted',           'projects'],
    ['user.added',                'users'],
    ['share.published',           'shares'],
    ['host.heartbeat',            'hosts'],
  ])('maps %s → %s', (kind, want) => {
    expect(eventToResource(kind)).toBe(want);
  });

  it.each([
    [''],
    ['unrelated.event'],
    ['random'],
    ['agent.boot'],     // not in the table — system-level event
  ])('returns null for unmapped kind %s', (kind) => {
    expect(eventToResource(kind)).toBeNull();
  });
});
