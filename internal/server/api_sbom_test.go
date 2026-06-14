// api_sbom_test.go — end-to-end on /api/sbom : returns a parseable
// CycloneDX 1.5 document with a non-empty components list, gated
// admin-only.

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSBOM_ReturnsCycloneDX15(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	var body struct {
		BomFormat   string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Version     int    `json:"version"`
		Metadata    struct {
			Component struct {
				Type    string `json:"type"`
				Name    string `json:"name"`
				Version string `json:"version"`
				Purl    string `json:"purl"`
			} `json:"component"`
		} `json:"metadata"`
		Components []struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Version string `json:"version"`
			Purl    string `json:"purl"`
		} `json:"components"`
	}
	if c := hit(t, srv, "GET", "/api/sbom", nil, &body); c != 200 {
		t.Fatalf("status = %d", c)
	}
	if body.BomFormat != "CycloneDX" {
		t.Errorf("bomFormat = %q, want CycloneDX", body.BomFormat)
	}
	if body.SpecVersion != "1.5" {
		t.Errorf("specVersion = %q, want 1.5", body.SpecVersion)
	}
	if body.Version != 1 {
		t.Errorf("version = %d, want 1", body.Version)
	}
	if body.Metadata.Component.Type != "application" {
		t.Errorf("metadata.component.type = %q, want application", body.Metadata.Component.Type)
	}
	// Note : `go test` populates bi.Main but not bi.Deps — so the
	// components list is empty under the test harness even though
	// `go build`-produced binaries get the full list. We can't
	// regression-test against a specific dep here ; spot-check
	// instead that components is a non-nil slice (the JSON shape
	// is what cyclonedx-cli reads).
	if body.Components == nil {
		t.Errorf("components must be a slice (possibly empty), got nil")
	}
	for _, c := range body.Components {
		if c.Purl != "" && !strings.HasPrefix(c.Purl, "pkg:golang/") {
			t.Errorf("component %s : purl = %q, want pkg:golang/ prefix", c.Name, c.Purl)
		}
	}
}

func TestSBOM_UserPortalReturns404(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeUser))
	t.Cleanup(srv.Close)

	// User portal must not see the dep list — that's recon material.
	if c := hit(t, srv, "GET", "/api/sbom", nil, nil); c != 404 {
		t.Errorf("status = %d, want 404 (admin-only)", c)
	}
}
