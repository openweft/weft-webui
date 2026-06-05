// api.go — central huma setup. One huma.API instance per http.ServeMux ;
// each feature area (flavors, scripts, microvms, networks, …) registers
// its operations into the same API via a mountX(api) helper.
//
// Why huma : typed input/output structs replace the historical map
// [string]any envelopes ; validation tags become OpenAPI constraints
// AND 422 responses before the handler runs ; the spec is published
// at /api/openapi.json + interactive docs at /api/docs. Svelte
// generates its client types from the spec, eliminating the
// drift class that hand-rolled map[string]any kept hidden.
//
// Routes that stay on stdlib (not registered here) :
//
//   - /api/healthz, /api/readyz — trivial, no contract to express
//   - /api/auth/{login,callback,logout} — OIDC 302 redirects
//   - /api/session/scope — exposed by the auth package, not part of
//     the public API contract
//   - /api/events — SSE stream (huma's streaming story is heavier
//     than what we need ; the hand-rolled one is 195 lines and
//     works)
//   - /metrics — Prometheus handler, opaque to us
//   - SPA static (/) — embedded SvelteKit bundle
//
// Everything else flows through huma.

package server

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// MountAPIForCodegen is the exported alias the dump-openapi tool
// uses to introspect the spec without instantiating the rest of the
// server (Deps, OIDC, gRPC client, …). Production code stays on the
// package-private mountAPI via buildHandler.
func MountAPIForCodegen(mux *http.ServeMux, scope Scope) huma.API {
	return mountAPI(mux, scope)
}

// mountAPI wires the typed REST surface onto mux. The persona +
// scope drive operation visibility :
//
//   - scope == ScopeAdmin : full surface, including cluster-admin
//     operations the user listener must never even acknowledge.
//   - scope == ScopeUser  : the public surface ; admin-only ops
//     are simply not registered, so unauthenticated probes see a
//     plain 404 (no "you're not allowed" signal).
//
// Returns the huma.API instance for completeness (tests can
// introspect the spec via api.OpenAPI()).
func mountAPI(mux *http.ServeMux, scope Scope) huma.API {
	cfg := huma.DefaultConfig("Weft WebUI API", "v1")
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = "/api/docs"
	api := humago.New(mux, cfg)

	mountFlavorsAPI(api)
	mountScriptsAPI(api, scope)
	mountSSHKeysCatalogueAPI(api)
	mountMicroVMMetadataAPI(api)
	mountMicroVMLifecycleAPI(api)
	mountNetworkingAPI(api, scope)
	mountTenantsAPI(api, scope)
	mountStorageAPI(api)
	mountVolumeMetadataAPI(api, scope)
	mountVolumeSnapshotsAPI(api)
	mountVolumeBackupsAPI(api)
	mountSubnetsAPI(api, scope)
	mountVMAuthzAPI(api, scope)
	mountEditableMetadataAPI(api, scope)
	mountRegistriesAPI(api, scope)
	mountPluginsAPI(api, scope)
	mountFederationAPI(api, scope)
	mountInventoryAPI(api, scope)
	mountMiscAPI(api, scope)

	return api
}
