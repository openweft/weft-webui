// api_plugins_tenant_test.go — pin filterPluginInstancesByTenant.
// The seeded tenant store has acme→team-alpha+team-beta and
// globex→research, so we use those as test fixtures.

package server

import "testing"

func TestFilterPluginInstancesByTenant_AcmeSeesOwnProjects(t *testing.T) {
	all := []PluginInstance{
		{Name: "valkey", Project: "team-alpha", InstanceUUID: "u1"},
		{Name: "redis", Project: "team-beta", InstanceUUID: "u2"},
		{Name: "kafka", Project: "research", InstanceUUID: "u3"},
	}
	got := filterPluginInstancesByTenant(all, "acme")
	if len(got) != 2 {
		t.Fatalf("want 2 acme instances, got %d : %+v", len(got), got)
	}
	for _, inst := range got {
		if inst.Project == "research" {
			t.Errorf("globex project %q leaked into acme view", inst.Project)
		}
	}
}

func TestFilterPluginInstancesByTenant_GlobexSeesOwn(t *testing.T) {
	all := []PluginInstance{
		{Name: "valkey", Project: "team-alpha"},
		{Name: "kafka", Project: "research"},
	}
	got := filterPluginInstancesByTenant(all, "globex")
	if len(got) != 1 || got[0].Project != "research" {
		t.Errorf("want only research, got %+v", got)
	}
}

func TestFilterPluginInstancesByTenant_EmptyTenantReturnsEmpty(t *testing.T) {
	// Fail-closed : a session with no tenant claim must NOT see the
	// cluster-wide list ; that's the infra-portal's job. Returning
	// `all` here would be the leak.
	all := []PluginInstance{
		{Name: "valkey", Project: "team-alpha"},
	}
	got := filterPluginInstancesByTenant(all, "")
	if len(got) != 0 {
		t.Errorf("want empty (fail-closed), got %+v", got)
	}
}

func TestFilterPluginInstancesByTenant_UnknownTenantReturnsEmpty(t *testing.T) {
	// A tenant that doesn't exist in tenantsDB → no projects to allow
	// → no instances.
	all := []PluginInstance{
		{Name: "valkey", Project: "team-alpha"},
		{Name: "kafka", Project: "research"},
	}
	got := filterPluginInstancesByTenant(all, "does-not-exist")
	if len(got) != 0 {
		t.Errorf("unknown tenant should see empty, got %+v", got)
	}
}

func TestFilterPluginInstancesByTenant_ProjectOutsideTenant(t *testing.T) {
	// A plugin instance in a project that doesn't belong to any
	// declared tenant — handler should not surface it on a tenant
	// portal (whose tenant doesn't own it).
	all := []PluginInstance{
		{Name: "valkey", Project: "orphan-project"},
	}
	got := filterPluginInstancesByTenant(all, "acme")
	if len(got) != 0 {
		t.Errorf("orphan project should not be visible to acme, got %+v", got)
	}
}
