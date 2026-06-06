package server

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// Plan B coverage policy : pure-Go helpers in portals.go each get a
// unit test. Reflects the three-portal split contract :
//
//   - PortalUser   visibility bitmask = ScopeUser
//   - PortalTenant visibility bitmask = ScopeUser | ScopeTenant
//   - PortalInfra  visibility bitmask = ScopeUser | ScopeTenant | ScopeAdmin
//   - PortalLegacy visibility bitmask = same as PortalInfra (full
//                  surface), distinct label so logs are unambiguous.

func TestPortal_String(t *testing.T) {
	cases := map[Portal]string{
		PortalUser:   "user",
		PortalTenant: "tenant",
		PortalInfra:  "infra",
		PortalLegacy: "legacy",
		Portal(99):   "unknown",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("Portal(%d).String() = %q, want %q", int(p), got, want)
		}
	}
}

func TestPortal_AssetSubdir(t *testing.T) {
	cases := map[Portal]string{
		PortalUser:   "user",
		PortalTenant: "tenant",
		PortalInfra:  "infra",
		PortalLegacy: "infra", // legacy reuses the full bundle
		Portal(99):   "infra",
	}
	for p, want := range cases {
		if got := p.AssetSubdir(); got != want {
			t.Errorf("Portal(%d).AssetSubdir() = %q, want %q", int(p), got, want)
		}
	}
}

func TestPortal_Scope(t *testing.T) {
	type want struct{ user, tenant, admin bool }
	cases := map[Portal]want{
		PortalUser:   {user: true, tenant: false, admin: false},
		PortalTenant: {user: true, tenant: true, admin: false},
		PortalInfra:  {user: true, tenant: true, admin: true},
		PortalLegacy: {user: true, tenant: true, admin: true},
	}
	for p, w := range cases {
		s := p.Scope()
		if got := s.Has(ScopeUser); got != w.user {
			t.Errorf("%s.Scope().Has(User) = %v, want %v", p, got, w.user)
		}
		if got := s.Has(ScopeTenant); got != w.tenant {
			t.Errorf("%s.Scope().Has(Tenant) = %v, want %v", p, got, w.tenant)
		}
		if got := s.Has(ScopeAdmin); got != w.admin {
			t.Errorf("%s.Scope().Has(Admin) = %v, want %v", p, got, w.admin)
		}
	}
}

func TestPortal_Requires(t *testing.T) {
	type want struct{ tenantOrAbove, admin bool }
	cases := map[Portal]want{
		PortalUser:   {tenantOrAbove: false, admin: false},
		PortalTenant: {tenantOrAbove: true, admin: false},
		PortalInfra:  {tenantOrAbove: true, admin: true},
		PortalLegacy: {tenantOrAbove: true, admin: true},
	}
	for p, w := range cases {
		if got := p.requiresTenantOrAbove(); got != w.tenantOrAbove {
			t.Errorf("%s.requiresTenantOrAbove() = %v, want %v", p, got, w.tenantOrAbove)
		}
		if got := p.requiresClusterAdmin(); got != w.admin {
			t.Errorf("%s.requiresClusterAdmin() = %v, want %v", p, got, w.admin)
		}
	}
}

func TestAssetsForPortal_PicksSubtree(t *testing.T) {
	// fstest.MapFS simulates the multi-portal Vite build output : a
	// per-portal index.html in dist/<portal>/ + a shared dist/assets/
	// pool with the entry chunks every index.html references.
	root := fstest.MapFS{
		"user/index.html":        {Data: []byte("user")},
		"tenant/index.html":      {Data: []byte("tenant")},
		"infra/index.html":       {Data: []byte("infra")},
		"assets/user-abc.js":     {Data: []byte("user-js")},
		"assets/tenant-abc.js":   {Data: []byte("tenant-js")},
		"assets/infra-abc.js":    {Data: []byte("infra-js")},
		"assets/common-abc.css":  {Data: []byte(".a{}")},
	}

	for _, tc := range []struct {
		portal      Portal
		wantContent string
	}{
		{PortalUser, "user"},
		{PortalTenant, "tenant"},
		{PortalInfra, "infra"},
		{PortalLegacy, "infra"}, // reuses infra bundle
	} {
		portalFS, sharedFS, err := assetsForPortal(root, tc.portal)
		if err != nil {
			t.Fatalf("%s: assetsForPortal err = %v", tc.portal, err)
		}
		b, err := fs.ReadFile(portalFS, "index.html")
		if err != nil {
			t.Fatalf("%s: read index.html = %v", tc.portal, err)
		}
		if string(b) != tc.wantContent {
			t.Errorf("%s: index.html = %q, want %q", tc.portal, b, tc.wantContent)
		}
		// Shared FS must give access to /assets/<entry>-<hash>.js
		// — that's the absolute URL the per-portal index.html
		// references.
		if _, err := fs.ReadFile(sharedFS, "assets/common-abc.css"); err != nil {
			t.Errorf("%s: shared assets unreachable : %v", tc.portal, err)
		}
	}
}

func TestAssetsForPortal_LegacyFlatLayout(t *testing.T) {
	// When the dist/ doesn't contain per-portal subdirs (legacy Vite
	// build), assetsForPortal returns the root unchanged for both
	// FSes + a sentinel error so the caller logs the fall-back.
	root := fstest.MapFS{
		"index.html":      {Data: []byte("legacy")},
		"assets/x-abc.js": {Data: []byte("legacy-js")},
	}
	portalFS, sharedFS, err := assetsForPortal(root, PortalUser)
	if err == nil {
		t.Fatal("want error signalling fall-back, got nil")
	}
	b, err := fs.ReadFile(portalFS, "index.html")
	if err != nil {
		t.Fatalf("read index.html = %v", err)
	}
	if string(b) != "legacy" {
		t.Errorf("want legacy content, got %q", b)
	}
	if _, err := fs.ReadFile(sharedFS, "assets/x-abc.js"); err != nil {
		t.Errorf("shared FS should fall back to root : %v", err)
	}
}

func TestAssetsForPortal_NilRoot(t *testing.T) {
	if _, _, err := assetsForPortal(nil, PortalUser); err == nil {
		t.Fatal("want error for nil root, got nil")
	}
}
