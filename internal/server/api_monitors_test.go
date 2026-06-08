// api_monitors_test.go — covers the offline + override paths of
// /api/monitors. Live etcd is mocked via a tiny in-memory source so
// the test stays hermetic.

package server

import (
	"context"
	"testing"
)

type fakeMonitorsSource struct {
	hosts   []MonitorHost
	members int
	err     error
}

func (f *fakeMonitorsSource) Hosts(_ context.Context) ([]MonitorHost, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.hosts, nil
}

func (f *fakeMonitorsSource) MemberCount(_ context.Context) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.members, nil
}

func TestListMonitors_OfflineWhenSourceNil(t *testing.T) {
	prev := monitorsSrc
	t.Cleanup(func() { monitorsSrc = prev })
	monitorsSrc = nil
	SetExpectedMonitorsOverride(0)

	got := listMonitors(t.Context())
	if got.Count != 0 {
		t.Fatalf("Count : got %d, want 0", got.Count)
	}
	if got.ExpectedCount != 0 {
		t.Fatalf("ExpectedCount : got %d, want 0", got.ExpectedCount)
	}
	if len(got.Monitors) != 0 {
		t.Fatalf("Monitors : got %v, want empty", got.Monitors)
	}
}

func TestListMonitors_HealthyClusterDefaultsExpectedToMemberCount(t *testing.T) {
	prev := monitorsSrc
	t.Cleanup(func() { monitorsSrc = prev })
	monitorsSrc = &fakeMonitorsSource{
		hosts: []MonitorHost{
			{HostUUID: "a", Hostname: "dc1-r1-h1", Hypervisor: "qemu", Version: "v0.4.1"},
			{HostUUID: "b", Hostname: "dc2-r1-h1", Hypervisor: "qemu", Version: "v0.4.1"},
			{HostUUID: "c", Hostname: "dc3-r1-h1", Hypervisor: "qemu", Version: "v0.4.1"},
		},
		members: 3,
	}
	SetExpectedMonitorsOverride(0)

	got := listMonitors(t.Context())
	if got.Count != 3 {
		t.Fatalf("Count : got %d, want 3", got.Count)
	}
	if got.ExpectedCount != 3 {
		t.Fatalf("ExpectedCount : got %d, want 3 (member count fallback)", got.ExpectedCount)
	}
}

func TestListMonitors_OverrideBeatsMemberCount(t *testing.T) {
	prev := monitorsSrc
	t.Cleanup(func() { monitorsSrc = prev })
	monitorsSrc = &fakeMonitorsSource{
		hosts:   []MonitorHost{{HostUUID: "a", Hostname: "dc1"}},
		members: 5, // 5-member etcd quorum serving a smaller agent fleet
	}
	SetExpectedMonitorsOverride(3)
	t.Cleanup(func() { SetExpectedMonitorsOverride(0) })

	got := listMonitors(t.Context())
	if got.ExpectedCount != 3 {
		t.Fatalf("ExpectedCount : got %d, want 3 (pinned override)", got.ExpectedCount)
	}
}

func TestListMonitors_OverrideAppliesOffline(t *testing.T) {
	prev := monitorsSrc
	t.Cleanup(func() { monitorsSrc = prev })
	monitorsSrc = nil
	SetExpectedMonitorsOverride(3)
	t.Cleanup(func() { SetExpectedMonitorsOverride(0) })

	got := listMonitors(t.Context())
	if got.ExpectedCount != 3 {
		t.Fatalf("ExpectedCount : got %d, want 3 (override survives offline source)", got.ExpectedCount)
	}
	if got.Count != 0 {
		t.Fatalf("Count : got %d, want 0", got.Count)
	}
}

func TestDecodeMonitorHost_ConvertsUnixNsToRFC3339(t *testing.T) {
	// 2026-06-08T19:36:42Z = 1781451402 unix seconds
	// = 1781451402000000000 unix ns
	raw := []byte(`{
		"host_uuid":"a777bdcf",
		"hostname":"dc1-r1-h1",
		"hypervisor":"qemu",
		"version":"v0.4.1",
		"started_at_unix_ns":1781451402000000000
	}`)
	got, err := DecodeMonitorHost(raw)
	if err != nil {
		t.Fatalf("decode : %v", err)
	}
	if got.HostUUID != "a777bdcf" {
		t.Errorf("HostUUID : got %q, want a777bdcf", got.HostUUID)
	}
	if got.Hostname != "dc1-r1-h1" {
		t.Errorf("Hostname : got %q, want dc1-r1-h1", got.Hostname)
	}
	if got.StartedAt == "" {
		t.Errorf("StartedAt : empty ; expected RFC-3339")
	}
}

func TestDecodeMonitorHost_HandlesMissingTimestamp(t *testing.T) {
	raw := []byte(`{"host_uuid":"a","hostname":"h"}`)
	got, err := DecodeMonitorHost(raw)
	if err != nil {
		t.Fatalf("decode : %v", err)
	}
	if got.StartedAt != "" {
		t.Errorf("StartedAt : got %q, want empty (no timestamp on the wire)", got.StartedAt)
	}
}
