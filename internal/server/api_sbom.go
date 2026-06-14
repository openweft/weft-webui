// api_sbom.go — GET /api/sbom emits a minimal CycloneDX 1.5
// software-bill-of-materials from the binary's runtime/debug.BuildInfo.
//
// Why CycloneDX :
//   - SPDX 2.3 has 30+ required fields per package (download URLs,
//     copyright text, license expressions) we can't honestly fill
//     from debug.BuildInfo alone.
//   - CycloneDX 1.5 has a clean minimal subset : bomFormat,
//     specVersion, version, components[].
//   - Both formats round-trip through `cyclonedx-cli` /
//     `cyclonedx-py` / Grype / Trivy.
//
// Why admin-only : the dep list is the canonical "what to attack"
// recon for an interested party — exact module versions, sum
// hashes. Surface only on the infra portal.

package server

import (
	"context"
	"net/http"
	"runtime/debug"

	"github.com/danielgtaylor/huma/v2"
)

func mountSBOMAPI(api huma.API, scope Scope) {
	if !scope.Has(ScopeAdmin) {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-sbom",
		Method:      "GET",
		Path:        "/api/sbom",
		Summary:     "Software bill of materials (CycloneDX 1.5) for the running binary",
		Description: "Synthesised from runtime/debug.BuildInfo — same source the dependency-scanning side of `go list -m all` consumes. Feed to cyclonedx-cli / Grype / Trivy to surface CVEs against the linked modules. Admin-only ; the exact dependency versions are recon material for an attacker, never exposed on tenant + user portals.",
		Tags:        []string{"audit", "supply-chain"},
	}, func(_ context.Context, _ *struct{}) (*sbomOutput, error) {
		out := &sbomOutput{Body: buildCycloneDX()}
		return out, nil
	})
}

// buildCycloneDX walks debug.BuildInfo + emits the minimal CycloneDX
// 1.5 subset Trivy / Grype scanners require :
//
//	bomFormat: "CycloneDX"
//	specVersion: "1.5"
//	version: 1            (the SBOM document version, not the app version)
//	metadata.component    main module
//	components[]          each dependency
func buildCycloneDX() CycloneDX {
	bom := CycloneDX{
		BomFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Version:     1,
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return bom
	}
	bom.Metadata.Component = CDXComponent{
		Type:    "application",
		Name:    bi.Main.Path,
		Version: serverVersion,
		Purl:    "pkg:golang/" + bi.Main.Path + "@" + serverVersion,
	}
	bom.Components = make([]CDXComponent, 0, len(bi.Deps))
	for _, d := range bi.Deps {
		if d == nil {
			continue
		}
		// Follow the chain of replaces so the version reported is the
		// one actually linked into the binary, not the original
		// require line.
		eff := d
		for eff.Replace != nil {
			eff = eff.Replace
		}
		bom.Components = append(bom.Components, CDXComponent{
			Type:    "library",
			Name:    eff.Path,
			Version: eff.Version,
			Purl:    "pkg:golang/" + eff.Path + "@" + eff.Version,
		})
	}
	return bom
}

// CycloneDX is the response shape — the minimal CycloneDX 1.5
// subset. Field names mirror the spec exactly so a consumer who
// expects vendor-neutral CycloneDX JSON gets a parseable doc.
type CycloneDX struct {
	BomFormat   string         `json:"bomFormat"`
	SpecVersion string         `json:"specVersion"`
	Version     int            `json:"version"`
	Metadata    CDXMetadata    `json:"metadata"`
	Components  []CDXComponent `json:"components"`
}

type CDXMetadata struct {
	Component CDXComponent `json:"component"`
}

type CDXComponent struct {
	Type    string `json:"type"`              // "application" | "library"
	Name    string `json:"name"`              // module path
	Version string `json:"version"`           // module version ("(devel)" for main on a non-VCS build)
	Purl    string `json:"purl,omitempty"`    // package URL — what cyclonedx-cli matches against
}

type sbomOutput struct {
	Body CycloneDX
}

// Compile-time hint that the package's http stdlib import isn't
// dead — kept for future endpoints that might need it.
var _ = http.MethodGet
