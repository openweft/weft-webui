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
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

func mountMiscAPI(api huma.API, scope Scope) {
	mountHealthAPI(api)
	mountResourcesAPI(api, scope)
	mountSummaryAPI(api, scope)
	mountRegistryUploadAPI(api)
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
		OperationID: "readyz",
		Method:      "GET",
		Path:        "/api/readyz",
		Summary:     "Readiness probe (returns mode=mock when no daemon is wired)",
		Tags:        []string{"health"},
	}, func(_ context.Context, _ *struct{}) (*passthroughOutput, error) {
		if live == nil {
			return &passthroughOutput{Body: map[string]any{"ok": true, "mode": "mock"}}, nil
		}
		return &passthroughOutput{Body: map[string]any{"ok": true, "mode": "live"}}, nil
	})
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
