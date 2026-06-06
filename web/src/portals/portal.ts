// Portal identifies which of the three weft-webui listeners is
// serving this SPA bundle. The build-time entry point passes the
// constant down to App.svelte so the sidebar + active-route switch
// can hide pages that aren't backed by registered endpoints on this
// listener.
//
// Hard-isolation contract :
//
//   - 'user'   :8080 — own-scope only. No tenant-admin, no
//                      cluster-admin pages.
//   - 'tenant' :8088 — tenant VLAN. Same as user + tenant-wide
//                      catalogues + tenant-admin actions.
//   - 'infra'  :8089 — superadmin. WireGuard mesh only. Full surface.
//   - 'legacy' single-listener compat : everything.
//
// The Go side enforces the same split server-side : an admin endpoint
// is genuinely not registered on the user mux, so the SPA's hide-the-
// page move on the user portal is the cosmetic half of a real
// defence-in-depth scheme.
export type Portal = 'user' | 'tenant' | 'infra' | 'legacy';

// portalCanSeeTenantAdmin reports whether this portal exposes tenant-
// admin affordances (quotas, audit log, member management).
export function portalCanSeeTenantAdmin(p: Portal): boolean {
  return p === 'tenant' || p === 'infra' || p === 'legacy';
}

// portalCanSeeClusterAdmin reports whether this portal exposes
// cluster-admin affordances (inventory, plugins, federation,
// /metrics, audit log with cluster scope).
export function portalCanSeeClusterAdmin(p: Portal): boolean {
  return p === 'infra' || p === 'legacy';
}

// portalShowsResource is the SPA's mirror of the server's
// resolveScope().Has(scope) filter. Used by the sidebar to drop
// categories the listener doesn't serve so a stale build doesn't
// surface a broken link.
//
// The set of resource ids is small and stable ; we list the strictly
// cluster-admin-only ones inline rather than chasing the Go-side
// registry — the /api/resources catalogue is the source of truth at
// runtime, this map is only a fallback for offline SPA scaffolding.
const clusterOnlyResources = new Set([
  'azs',
  'racks',
  'hosts',
  'inventory-tree',
  'inventory-map',
  'topology',
  'plugins',
  'audit-log',
  'groups',
  'users',
  'federation',
  'flavors',
  'scripts',
  'security-rules',
  'scheduling-rules',
  'dns',
  'dns-zones',
  'dns-records',
  'routers',
]);

const tenantOnlyResources = new Set<string>([
  // Tenant-level affordances that should NOT show on the user portal.
  // Currently empty — user portal still sees Tenants page (read-only).
]);

export function portalShowsResource(p: Portal, id: string): boolean {
  if (p === 'infra' || p === 'legacy') return true;
  if (p === 'tenant') return !clusterOnlyResources.has(id);
  // user portal : drop cluster + tenant-only resources.
  return !clusterOnlyResources.has(id) && !tenantOnlyResources.has(id);
}
