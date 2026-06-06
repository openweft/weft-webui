// portals.go — three-portal split.
//
// Hard isolation : a user who hits the public listener cannot reach a
// tenant-admin / cluster-admin endpoint even by crafting a URL, because
// the corresponding huma operation is never registered on that mux.
//
//   - PortalUser   : end-user surface. Own-scope reads / writes only.
//                    No quota, audit, host, plugin, federation,
//                    registry, scheduling, dns-zone, security-rule
//                    endpoints registered AT ALL.
//   - PortalTenant : everything PortalUser sees, plus tenant-wide
//                    views (tenant catalogues, cross-project tenant
//                    listings, tenant-scoped audit log) and the
//                    tenant-admin mutation surface.
//   - PortalInfra  : superset — cluster-wide ops, plugins, federation,
//                    inventory, plus /metrics. WireGuard-mesh-only.
//
// The Portal value drives a Scope bitmask passed to the per-area
// mountXxxAPI helpers. The existing helpers already gate on the same
// bits (ScopeAdmin / ScopeUser) — the new bit ScopeTenant lets the
// tenant-portal surface tenant-admin endpoints without pulling in the
// cluster-admin surface.
//
// A legacy mode is preserved : when only --addr is set (no new
// --tenant-addr / --infra-addr flags), the binary keeps booting as a
// single full-surface listener via New() / NewAdmin(), so a single-
// host dev install doesn't have to learn the new flags.

package server

import (
	"io/fs"
	"net/http"
)

// Portal selects the operation set registered on a listener. One
// listener = one Portal = one huma router with a different set of
// registered endpoints.
type Portal int

const (
	// PortalUser is the public end-user surface : own-scope reads +
	// writes. No tenant-admin, no cluster-admin endpoints registered.
	PortalUser Portal = iota
	// PortalTenant is the tenant-VLAN surface : everything PortalUser
	// sees plus tenant-wide views and tenant-admin mutations.
	PortalTenant
	// PortalInfra is the WireGuard-mesh-only superset : every cluster-
	// wide operation, /metrics, plugins, federation, inventory.
	PortalInfra
	// PortalLegacy preserves the pre-split behaviour : a single
	// listener with the full surface (everything Infra has). Used by
	// New() / NewAdmin() so existing single-host installs keep
	// working when only --addr (and optionally --admin-addr alias for
	// tenant-addr) is set.
	PortalLegacy
)

// String returns the canonical lower-case label used in startup logs.
func (p Portal) String() string {
	switch p {
	case PortalUser:
		return "user"
	case PortalTenant:
		return "tenant"
	case PortalInfra:
		return "infra"
	case PortalLegacy:
		return "legacy"
	}
	return "unknown"
}

// AssetSubdir returns the path inside the embedded web/dist tree that
// holds the SPA bundle this portal serves. The Vite build emits three
// sibling directories (user/, tenant/, infra/) ; the legacy portal
// reuses the infra bundle (full surface).
func (p Portal) AssetSubdir() string {
	switch p {
	case PortalUser:
		return "user"
	case PortalTenant:
		return "tenant"
	case PortalInfra, PortalLegacy:
		return "infra"
	}
	return "infra"
}

// Scope returns the bitmask the huma mountXxxAPI helpers test
// against to decide which operations to register. The bits are
// additive : PortalInfra carries every bit.
func (p Portal) Scope() Scope {
	switch p {
	case PortalUser:
		return ScopeUser
	case PortalTenant:
		return ScopeUser | ScopeTenant
	case PortalInfra, PortalLegacy:
		return ScopeUser | ScopeTenant | ScopeAdmin
	}
	return ScopeUser
}

// requiresClusterAdmin reports whether the portal exposes the
// cluster-admin mutation surface (plugins, federation, inventory,
// audit-log read, /metrics).
func (p Portal) requiresClusterAdmin() bool {
	return p == PortalInfra || p == PortalLegacy
}

// requiresTenantOrAbove reports whether the portal exposes the
// tenant-admin mutation surface (member management, quotas,
// tenant-scoped audit-log).
func (p Portal) requiresTenantOrAbove() bool {
	return p == PortalTenant || p == PortalInfra || p == PortalLegacy
}

// newPortalRouter is the canonical entry point for the three-portal
// model. It returns a fully-wired http.Handler for the given Portal :
// huma surface restricted to the portal's Scope, the SPA bundle from
// the right embedded subtree (with a fall-through to the shared
// assets pool), the SAME shared middleware chain (auth, rate-limit,
// metrics, …).
func newPortalRouter(d Deps, p Portal) http.Handler {
	persona := p.String()
	exposeMetrics := p.requiresClusterAdmin()

	// SPA assets : layered FS. The portal sub-FS (e.g. dist/user/)
	// owns the index.html ; the shared root (dist/) owns the
	// /assets/*-<hash>.js + *.css pool. When the portal subdir
	// doesn't exist (legacy single-output build), assetsForPortal
	// falls back to the root for both.
	portalFS, sharedFS, err := assetsForPortal(d.Static, p)
	if err != nil {
		d.Logger.Warn("portal assets missing — falling back to flat dist layout",
			"portal", p.String(), "err", err)
	}
	pd := d
	pd.Static = portalFS
	pd.SharedAssets = sharedFS
	return buildHandler(pd, p.Scope(), persona, exposeMetrics)
}

// assetsForPortal returns a two-FS view of the embedded tree :
//
//   - portalFS  : rooted at the portal's bundle (e.g. dist/user/).
//                 Owns the per-portal index.html.
//   - sharedFS  : the parent root (dist/). Owns the shared
//                 /assets/* pool that every portal's index.html
//                 references via an absolute path.
//
// When dist/<portal>/index.html doesn't exist (legacy flat build),
// both returned values are the same root + the error is non-nil so
// the caller logs the fall-back.
func assetsForPortal(root fs.FS, p Portal) (portalFS, sharedFS fs.FS, err error) {
	if root == nil {
		return nil, nil, fs.ErrNotExist
	}
	sub := p.AssetSubdir()
	if _, statErr := fs.Stat(root, sub+"/index.html"); statErr != nil {
		// Legacy flat layout : every path goes through the root FS.
		return root, root, statErr
	}
	portalFS, err = fs.Sub(root, sub)
	if err != nil {
		return root, root, err
	}
	return portalFS, root, nil
}
