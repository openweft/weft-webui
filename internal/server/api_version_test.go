// api_version_test.go — pins /api/version : returns the value
// Deps.Version stamped, falls back to "dev" otherwise, exposed on
// every portal so operators verifying a rolling deploy don't have
// to switch UIs.

package server

import (
	"net/http/httptest"
	"testing"
)

func TestVersion_ReturnsStamped(t *testing.T) {
	// buildHandler overwrites serverVersion from Deps.Version on
	// construction (falls back to "dev" when Deps.Version is empty).
	// Set the global AFTER newE2EHandler returns so it sticks for
	// the request that follows.
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	prev := serverVersion
	t.Cleanup(func() { serverVersion = prev })
	serverVersion = "v0.9.1"

	var body struct {
		Version string `json:"version"`
	}
	if c := hit(t, srv, "GET", "/api/version", nil, &body); c != 200 {
		t.Fatalf("status = %d", c)
	}
	if body.Version != "v0.9.1" {
		t.Errorf("version = %q, want v0.9.1", body.Version)
	}
}

func TestVersion_FallsBackToDev(t *testing.T) {
	// newE2EHandler doesn't set Deps.Version, so buildHandler's
	// fallback kicks in.
	prev := serverVersion
	t.Cleanup(func() { serverVersion = prev })

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		Version string `json:"version"`
	}
	if c := hit(t, srv, "GET", "/api/version", nil, &body); c != 200 {
		t.Fatalf("status = %d", c)
	}
	if body.Version != "dev" {
		t.Errorf("version = %q, want dev (fallback)", body.Version)
	}
}

func TestVersion_ExposedOnEveryPortal(t *testing.T) {
	for _, sc := range []Scope{ScopeUser, ScopeUser | ScopeTenant, ScopeAdmin} {
		t.Run("scope="+scopeName(sc), func(t *testing.T) {
			srv := httptest.NewServer(newE2EHandler(t, sc))
			t.Cleanup(srv.Close)
			var body struct {
				Version string `json:"version"`
			}
			if c := hit(t, srv, "GET", "/api/version", nil, &body); c != 200 {
				t.Errorf("status = %d, want 200 on every portal", c)
			}
			if body.Version == "" {
				t.Errorf("version blank on this portal")
			}
		})
	}
}

func TestVersion_OpenAPISpecCarriesBuildVersion(t *testing.T) {
	// The spec at /api/openapi advertises info.version. Downstream
	// codegen / contract-test tooling pins to specific releases by
	// reading that field — it MUST track the binary build version,
	// not the "v1" placeholder huma defaults to.
	prev := serverVersion
	t.Cleanup(func() { serverVersion = prev })

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)
	// Note : mountAPI ran when newE2EHandler built the handler and
	// captured serverVersion at that point. Mutating it now wouldn't
	// affect the already-generated spec. Read the actual stamped
	// value (newE2EHandler doesn't set Deps.Version → "dev"
	// fallback).
	var spec map[string]any
	if c := hit(t, srv, "GET", "/api/openapi.json", nil, &spec); c != 200 {
		t.Fatalf("status = %d", c)
	}
	info, _ := spec["info"].(map[string]any)
	if info == nil {
		t.Fatalf("spec has no info block ; got %+v", spec)
	}
	v, _ := info["version"].(string)
	if v != "dev" {
		t.Errorf("info.version = %q, want \"dev\" (newE2EHandler default)", v)
	}
}

func scopeName(s Scope) string {
	switch {
	case s.Has(ScopeAdmin):
		return "infra"
	case s.Has(ScopeTenant):
		return "tenant"
	default:
		return "user"
	}
}
