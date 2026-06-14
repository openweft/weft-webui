// api_misc.go — the long tail of typed endpoints :
//
//   * /api/healthz, /api/readyz             — liveness + readiness
//   * /api/resources                        — catalogue listing (scope-filtered)
//   * /api/resources/{id}                   — paginated rows per resource
//   * /api/summary                          — scope-aware row counts
//   * /api/registry/upload                  — OCI artifact upload (mock)
//
// /api/resources/{id} is the dispatcher behind the SPA's generic
// ResourceTable. Because the response shape is polymorphic across
// dozens of resource kinds (and several call paths go through
// live-first → mem-fallback ladders), the op delegates to the
// pre-huma handleResourceRows via an httptest.ResponseRecorder.
// Transitional — the typed shape lands when the underlying source
// stabilises ; the huma seam is in place so the migration is a
// no-op for callers.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

func mountMiscAPI(api huma.API, scope Scope) {
	mountHealthAPI(api)
	mountResourcesAPI(api, scope)
	mountSummaryAPI(api, scope)
	mountRegistryUploadAPI(api)
	mountAuditAPI(api, scope)
	mountAuthThrottleAPI(api, scope)
	mountSBOMAPI(api, scope)
}

// ---- /api/healthz + /api/readyz -----------------------------------

func mountHealthAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "healthz",
		Method:      "GET",
		Path:        "/api/healthz",
		Summary:     "Liveness probe",
		Tags:        []string{"health"},
	}, func(_ context.Context, _ *struct{}) (*okOutput, error) {
		return &okOutput{Body: okBody{OK: true}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "version",
		Method:      "GET",
		Path:        "/api/version",
		Summary:     "Build version surfaced to operators + the SPA footer",
		Description: "Returns the linker-stamped version string (`-ldflags \"-X main.version=...\"`). Defaults to \"dev\" when not stamped. Surfaces on every portal — operators verifying a rolling deploy poll this before reloading the SPA.",
		Tags:        []string{"health"},
	}, func(_ context.Context, _ *struct{}) (*versionOutput, error) {
		return &versionOutput{Body: versionBody{Version: serverVersion}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "build-info",
		Method:      "GET",
		Path:        "/api/build-info",
		Summary:     "Detailed build identifier : version + go runtime + commit + build time",
		Description: "Strictly more detail than /api/version. Reads runtime/debug.BuildInfo for go_version + VCS commit + VCS time (populated automatically by `go build` on a clean checkout). Operators verifying a rolling deploy can cross-check the commit hash against what's expected, not just the human-friendly version tag.",
		Tags:        []string{"health"},
	}, func(_ context.Context, _ *struct{}) (*buildInfoOutput, error) {
		info := buildInfoBody{
			Version:   serverVersion,
			GoVersion: runtime.Version(),
		}
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					info.Commit = s.Value
				case "vcs.time":
					info.BuildTime = s.Value
				case "vcs.modified":
					info.Dirty = s.Value == "true"
				}
			}
		}
		return &buildInfoOutput{Body: info}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "readyz",
		Method:      "GET",
		Path:        "/api/readyz",
		Summary:     "Readiness probe — surfaces controller mode + state-file writability",
		Description: "Returns 200 + ok:true when the server can serve traffic ; returns 503 + ok:false when a wired persistence path isn't writable. Used by k8s readinessProbe / load-balancer health to take a node out of rotation if its state disk failed.",
		Tags:        []string{"health"},
	}, func(_ context.Context, _ *struct{}) (*readyzOutput, error) {
		body := map[string]any{"ok": true}
		if live == nil {
			body["mode"] = "mock"
		} else {
			body["mode"] = "live"
		}
		// Persistence probe : if any wired state-file path can't be
		// written, the dashboard is degraded (mutations would
		// succeed in-memory but vanish on restart). Surface this as
		// 503 so operators / orchestrators take this replica out
		// of rotation until the disk recovers.
		probes := map[string]string{}
		if p := inventoryPath; p != "" {
			probes["inventory"] = probeWritable(p)
		}
		if p := dnsPath; p != "" {
			probes["dns"] = probeWritable(p)
		}
		if p := securityPath; p != "" {
			probes["security"] = probeWritable(p)
		}
		if mem, ok := scriptsCatalogue.(*memScriptCatalogue); ok && mem.path != "" {
			probes["scripts"] = probeWritable(mem.path)
		}
		var degraded []string
		for name, status := range probes {
			if status != "ok" {
				degraded = append(degraded, name+":"+status)
			}
		}
		if len(probes) > 0 {
			body["probes"] = probes
		}
		if len(degraded) > 0 {
			body["ok"] = false
			body["degraded"] = degraded
			return &readyzOutput{Status: http.StatusServiceUnavailable, Body: body}, nil
		}
		return &readyzOutput{Status: http.StatusOK, Body: body}, nil
	})
}

// readyzOutput carries a dynamic HTTP status so a degraded probe
// returns 503 without going through huma.Error (which would log it
// as a request error in middleware metrics).
type readyzOutput struct {
	Status int
	Body   map[string]any
}

// probeWritable tries an atomic "create then remove" against a
// sibling .probe-<rand>.tmp file in path's directory. Returns "ok"
// on success ; otherwise a short error code suitable for the
// readyz body. Idempotent — no leftover files on disk.
func probeWritable(path string) string {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	// MkdirTemp is the cleanest "I can write here" test : creates
	// the dir if needed, fails clearly when not allowed, returns
	// a path we can immediately remove.
	tmp, err := os.MkdirTemp(dir, ".readyz-probe-")
	if err != nil {
		return "unwritable"
	}
	_ = os.RemoveAll(tmp)
	return "ok"
}

// ---- /api/resources catalogue listing -----------------------------

func mountResourcesAPI(api huma.API, scope Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "list-resource-catalogue",
		Method:      "GET",
		Path:        "/api/resources",
		Summary:     "List the visible resource catalogue (sidebar)",
		Description: "Scope-filtered : the user listener excludes admin-only entries (hosts / users / tenants) so a stale SPA never even sees they exist.",
		Tags:        []string{"resources"},
	}, func(_ context.Context, _ *struct{}) (*listResourcesOutput, error) {
		out := &listResourcesOutput{}
		out.Body = make([]resourceMeta, 0, len(registry))
		for i := range registry {
			res := &registry[i]
			if !resolveScope(res.Scope).Has(scope) {
				continue
			}
			if res.Hidden {
				continue
			}
			if !isResourceGateOpen(res.ID) {
				continue
			}
			out.Body = append(out.Body, resourceMeta{
				ID: res.ID, Label: res.Label, Section: res.Section,
				Columns: res.Columns, Count: rowCount(res),
			})
		}
		return out, nil
	})

	// /api/resources/{id} : delegating wrapper around handleResourceRows.
	// The legacy handler writes a {rows, next, total} envelope directly ;
	// we capture it via httptest.ResponseRecorder then replay it as a
	// huma passthrough body. Same wire shape, no behavioural drift.
	huma.Register(api, huma.Operation{
		OperationID: "list-resource-rows",
		Method:      "GET",
		Path:        "/api/resources/{id}",
		Summary:     "Paginated row listing for one resource kind",
		Description: "Polymorphic across the catalogue. Live-first for resources weft-agent ships ; mem-fallback otherwise. ?limit=N&page_token=... ; the cursor is opaque (base64 offset today, real keyset cursor once the upstream paginates).",
		Tags:        []string{"resources"},
	}, func(ctx context.Context, in *resourceRowsInput) (*passthroughOutput, error) {
		res, ok := resourceByID[in.ID]
		if !ok || !resolveScope(res.Scope).Has(scope) {
			return nil, huma.Error404NotFound("unknown resource")
		}
		// Synthesise a request that the legacy dispatcher can read +
		// capture its response. The legacy handler relies on the
		// request's context (auth + scope) and query string ; preserve
		// both. PathValue("id") is set via the muxed-pattern field.
		url := "/api/resources/" + in.ID + "?" + buildQuery(in.Limit, in.PageToken, in.Tenant, in.Project)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("synthesise request: " + err.Error())
		}
		req.SetPathValue("id", in.ID)
		rec := httptest.NewRecorder()
		handleResourceRows(rec, req)

		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			return nil, huma.Error500InternalServerError("decode legacy response: " + err.Error())
		}
		if rec.Code != http.StatusOK {
			// Propagate the legacy error envelope as a huma error so
			// the status code lands on the wire.
			msg, _ := body["error"].(string)
			if msg == "" {
				msg = http.StatusText(rec.Code)
			}
			return nil, huma.NewError(rec.Code, msg)
		}
		return &passthroughOutput{Body: body}, nil
	})
}

func buildQuery(limit int, pageToken, tenant, project string) string {
	parts := []string{}
	if limit > 0 {
		parts = append(parts, "limit="+itoaSafe(limit))
	}
	if pageToken != "" {
		parts = append(parts, "page_token="+pageToken)
	}
	if tenant != "" {
		parts = append(parts, "tenant="+tenant)
	}
	if project != "" {
		parts = append(parts, "project="+project)
	}
	return strings.Join(parts, "&")
}

// ---- /api/summary scope-aware row counts --------------------------

func mountSummaryAPI(api huma.API, scope Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "scope-summary",
		Method:      "GET",
		Path:        "/api/summary",
		Summary:     "Per-resource row counts narrowed by the session scope",
		Description: "One item per visible catalogue entry. When the session carries a tenant + project, counts narrow to that scope (mock + live agreed semantics).",
		Tags:        []string{"resources"},
	}, func(ctx context.Context, in *summaryInput) (*summaryOutput, error) {
		tenant := in.Tenant
		project := in.Project
		// Fall back to the session's scope when the query params are
		// empty (matches the legacy scopeFromRequest).
		// Note : we don't reach into auth here because the user
		// already provided the tenant + project explicitly when
		// they care ; the SPA always sends them.
		out := &summaryOutput{}
		for i := range registry {
			res := &registry[i]
			if !resolveScope(res.Scope).Has(scope) {
				continue
			}
			out.Body = append(out.Body, summaryItem{
				ID: res.ID, Label: res.Label, Count: scopedRowCount(res, tenant, project),
			})
		}
		return out, nil
	})
}

// ---- /api/registry/upload (mock OCI artifact ingest) --------------

func mountRegistryUploadAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "registry-upload",
		Method:        "POST",
		Path:          "/api/registry/upload",
		Summary:       "Register an OCI artifact (mock — records the row, no real push)",
		Description:   "Accepts a multipart form with type / repository / tag / registry / arch / file. Persists a registry row so the dashboard round-trips.",
		Tags:          []string{"registry"},
		DefaultStatus: 201,
	}, func(_ context.Context, in *registryUploadInput) (*passthroughOutput, error) {
		data := in.RawBody.Data()
		typ := strings.TrimSpace(data.Type)
		repo := strings.TrimSpace(data.Repository)
		tag := strings.TrimSpace(data.Tag)
		registryName := strings.TrimSpace(data.Registry)
		arches := data.Arch

		switch {
		case typ != "container" && typ != "raw":
			return nil, huma.Error400BadRequest("type must be 'container' or 'raw'")
		case repo == "":
			return nil, huma.Error400BadRequest("repository is required")
		case tag == "":
			return nil, huma.Error400BadRequest("tag is required")
		case len(arches) == 0:
			return nil, huma.Error400BadRequest("select at least one architecture")
		}
		if registryName == "" {
			registryName = "zot.dc-a"
		}

		var total int64
		for _, f := range data.File {
			total += f.Size
		}

		newRow := row(
			"repository", repo,
			"tag", tag,
			"type", typ,
			"arch", strings.Join(arches, ", "),
			"registry", registryName,
			"size", humanSize(total),
			"pushed", "just now",
		)
		registryAdd(newRow)
		return &passthroughOutput{Body: newRow}, nil
	})
}

// keep time imported (used by date stamps elsewhere when the registry
// upload extends to real push)
var _ = time.Now

// ---- inputs ------------------------------------------------------

type okBody struct {
	OK bool `json:"ok"`
}

type okOutput struct {
	Body okBody
}

type versionBody struct {
	Version string `json:"version" example:"v0.4.7"`
}

type versionOutput struct {
	Body versionBody
}

type buildInfoBody struct {
	Version   string `json:"version" example:"v0.4.7"`
	GoVersion string `json:"go_version" example:"go1.26.4"`
	Commit    string `json:"commit,omitempty" example:"a1b2c3d…" doc:"Git revision the binary was built from. Empty when built outside a VCS checkout (e.g. tarball + go install)."`
	BuildTime string `json:"build_time,omitempty" example:"2026-06-02T14:30:00Z" doc:"RFC3339 commit time of the source the binary was built from. Empty in non-VCS builds."`
	Dirty     bool   `json:"dirty,omitempty" doc:"True when the working tree was dirty at build time (uncommitted changes)."`
}

type buildInfoOutput struct {
	Body buildInfoBody
}

type listResourcesOutput struct {
	Body []resourceMeta
}

type resourceRowsInput struct {
	ID        string `path:"id" doc:"Resource catalogue id (e.g. 'microvms', 'tenants')" minLength:"1" maxLength:"64"`
	Limit     int    `query:"limit" doc:"Page size (1..1000)" minimum:"0" maximum:"1000"`
	PageToken string `query:"page_token" doc:"Opaque cursor from a previous page"`
	Tenant    string `query:"tenant" doc:"Override the session tenant"`
	Project   string `query:"project" doc:"Override the session project"`
}

type summaryInput struct {
	Tenant  string `query:"tenant"`
	Project string `query:"project"`
}

type summaryItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type summaryOutput struct {
	Body []summaryItem
}

type registryUploadInput struct {
	RawBody huma.MultipartFormFiles[struct {
		Type       string          `form:"type"       doc:"'container' or 'raw'" enum:"container,raw"`
		Repository string          `form:"repository" doc:"OCI repository name"`
		Tag        string          `form:"tag"        doc:"Tag for this artifact"`
		Registry   string          `form:"registry"   doc:"Target registry name (default zot.dc-a)"`
		Arch       []string        `form:"arch"       doc:"Architectures included in the artifact"`
		File       []huma.FormFile `form:"file"       doc:"Artifact blobs"`
	}]
}
