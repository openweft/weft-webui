package config

import "testing"

// Tests for the three-portal config helpers : ResolveAdminAlias +
// LegacySingleListener. Each helper is pure-Go and warrants a unit
// test under the Plan B coverage policy.

func TestResolveAdminAlias(t *testing.T) {
	t.Run("alias only — adopted by tenant addr", func(t *testing.T) {
		c := &Config{AdminAddr: ":8088"}
		if !c.ResolveAdminAlias() {
			t.Error("want deprecated=true when admin-addr is the only one set")
		}
		if c.TenantAddr != ":8088" {
			t.Errorf("tenant addr = %q, want :8088", c.TenantAddr)
		}
	})
	t.Run("both set — tenant wins, but still deprecated", func(t *testing.T) {
		c := &Config{AdminAddr: ":8088", TenantAddr: ":9099"}
		if !c.ResolveAdminAlias() {
			t.Error("want deprecated=true so the caller still warns")
		}
		if c.TenantAddr != ":9099" {
			t.Errorf("tenant addr clobbered : %q", c.TenantAddr)
		}
	})
	t.Run("neither set — no deprecation", func(t *testing.T) {
		c := &Config{}
		if c.ResolveAdminAlias() {
			t.Error("want deprecated=false when neither is set")
		}
		if c.TenantAddr != "" {
			t.Errorf("tenant addr leaked : %q", c.TenantAddr)
		}
	})
}

func TestLegacySingleListener(t *testing.T) {
	cases := map[string]struct {
		c    Config
		want bool
	}{
		"only --addr":                  {Config{UserAddr: ":8080"}, true},
		"--addr + --tenant-addr":       {Config{UserAddr: ":8080", TenantAddr: ":8088"}, false},
		"--addr + --infra-addr":        {Config{UserAddr: ":8080", InfraAddr: ":8089"}, false},
		"--addr + --tenant + --infra":  {Config{UserAddr: ":8080", TenantAddr: ":8088", InfraAddr: ":8089"}, false},
	}
	for name, tc := range cases {
		got := tc.c.LegacySingleListener()
		if got != tc.want {
			t.Errorf("%s: LegacySingleListener() = %v, want %v", name, got, tc.want)
		}
	}
}
