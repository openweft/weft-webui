// api_plugins_static_test.go — covers the static fallback the
// /api/plugins/catalogue endpoint serves when the live weft-agent
// isn't wired (dev mode, preview, agent unreachable).
//
// The list MUST stay in lock-step with `weft/catalogue/*/plugin.hcl`.
// We don't read those HCL files here (this repo doesn't import
// weft to keep the webui standalone) ; instead we lock the slug
// set so any HCL change forces an intentional update of the
// fallback below.

package server

import (
	"sort"
	"testing"
)

func TestStaticPluginCatalogue_NotEmpty(t *testing.T) {
	got := staticPluginCatalogue()
	if len(got) == 0 {
		t.Fatal("staticPluginCatalogue() returned empty ; the superadmin Plugins panel will look broken")
	}
}

func TestStaticPluginCatalogue_MatchesHCLPluginSet(t *testing.T) {
	// Expected set mirrors `weft/catalogue/*/plugin.hcl` as of 2026-06.
	// When you add/remove an HCL plugin under `weft/catalogue/`, update
	// both this set AND staticPluginCatalogue() ; this test will fail
	// loudly until they're in sync.
	want := []string{
		"caddy-edge",
		"forgejo-ha",
		"forgejo-runners-ha",
		"github-runners-ha",
		"gitlab-runners-ha",
		"grafana-ha",
		"irods-ha",
		"jupyterhub-ha",
		"loki-ha",
		"postgres-ha",
		"prometheus-ha",
		"redis-ha",
		"vault-ha",
		"versitygw-ha",
	}
	got := staticPluginCatalogue()
	gotNames := make([]string, 0, len(got))
	for _, e := range got {
		gotNames = append(gotNames, e.Name)
	}
	sort.Strings(gotNames)
	if len(gotNames) != len(want) {
		t.Fatalf("plugin count drift : got %d (%v), want %d (%v)", len(gotNames), gotNames, len(want), want)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Errorf("plugin[%d] : got %q, want %q", i, gotNames[i], want[i])
		}
	}
}

func TestStaticPluginCatalogue_EntriesAreWellFormed(t *testing.T) {
	for _, e := range staticPluginCatalogue() {
		if e.Name == "" {
			t.Errorf("catalogue entry has empty Name : %+v", e)
		}
		if e.Kind == "" {
			t.Errorf("catalogue entry %q has empty Kind", e.Name)
		}
		if e.Description == "" {
			t.Errorf("catalogue entry %q has empty Description", e.Name)
		}
		for j, in := range e.Inputs {
			if in.Name == "" {
				t.Errorf("%s.inputs[%d] has empty Name", e.Name, j)
			}
			if in.Type == "" {
				t.Errorf("%s.inputs[%d] (%s) has empty Type", e.Name, j, in.Name)
			}
			switch in.Type {
			case "string", "number", "bool", "secret":
				// ok — matches the SPA enum
			default:
				t.Errorf("%s.inputs[%d] (%s) has invalid Type %q (must be one of string|number|bool|secret)", e.Name, j, in.Name, in.Type)
			}
		}
	}
}

func TestListPluginCatalogue_FallsBackWhenLiveNil(t *testing.T) {
	// Save + restore the package-level `live` so we don't poison
	// neighbouring tests.
	prev := live
	t.Cleanup(func() { live = prev })
	live = nil

	got := listPluginCatalogue(t.Context())
	if len(got) == 0 {
		t.Fatal("listPluginCatalogue(nil-live) returned empty ; expected static fallback")
	}
}
