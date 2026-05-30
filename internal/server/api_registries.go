// api_registries.go — typed registry endpoints :
//
//   * /api/registries/remotes              — list remote registries
//   * /api/registries/remotes/{name}       — get / put / delete one
//
// The artifact-listing path stays on the generic
// /api/resources/registries (paginated through handleResourceRows)
// and uploads go through /api/registry/upload in api_misc.go.

package server

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

func mountRegistriesAPI(api huma.API, scope Scope) {
	// Remotes — only the admin listener publishes the write surface
	// (proxy / replica setup is a cluster-wide concern, not a
	// project-tenant one). Reads are surfaced everywhere so any user
	// looking at the registries page can see what's federated.
	mountRegistriesRemotesAPI(api, scope)
}

func mountRegistriesRemotesAPI(api huma.API, scope Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "list-registry-remotes",
		Method:      "GET",
		Path:        "/api/registries/remotes",
		Summary:     "List remote OCI registries (proxy / replica configuration)",
		Tags:        []string{"registries"},
	}, func(_ context.Context, _ *struct{}) (*listRegistryRemotesOutput, error) {
		out := &listRegistryRemotesOutput{}
		out.Body = registryRemotesList()
		return out, nil
	})

	// Search a remote registry for matching images. Replaces the
	// list-everything affordance for proxies (Docker Hub / GHCR are
	// too large to enumerate). Live wiring would proxy to the
	// remote's /v2/_catalog + per-repo /v2/<name>/tags/list ; the
	// mock returns canned matches against the query.
	huma.Register(api, huma.Operation{
		OperationID: "search-registry-remote",
		Method:      "GET",
		Path:        "/api/registries/remotes/{name}/search",
		Summary:     "Search a remote OCI registry for matching images",
		Description: "Substring match on repository name. Live wiring routes to the remote's catalog API ; the mock returns canned results against a fixed corpus per remote.",
		Tags:        []string{"registries"},
	}, func(_ context.Context, in *searchRegistryRemoteInput) (*searchRegistryRemoteOutput, error) {
		if _, ok := registryRemoteFind(in.Name); !ok {
			return nil, huma.Error404NotFound("remote not found: " + in.Name)
		}
		results := searchRemoteCatalog(in.Name, in.Q)
		return &searchRegistryRemoteOutput{Body: results}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-registry-remote",
		Method:      "GET",
		Path:        "/api/registries/remotes/{name}",
		Summary:     "Get one remote registry by name",
		Tags:        []string{"registries"},
	}, func(_ context.Context, in *registryRemoteNameInput) (*getRegistryRemoteOutput, error) {
		r, ok := registryRemoteFind(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("no such remote: " + in.Name)
		}
		return &getRegistryRemoteOutput{Body: r}, nil
	})

	if scope != ScopeAdmin {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID: "set-registry-remote",
		Method:      "POST",
		Path:        "/api/registries/remotes",
		Summary:     "Create or update a remote registry (cluster-admin)",
		Description: "Insert-or-update by Name. The LastSync field is owned by the sync engine — caller-supplied values are ignored.",
		Tags:        []string{"registries"},
	}, func(ctx context.Context, in *setRegistryRemoteInput) (*setRegistryRemoteOutput, error) {
		body := in.Body
		body.Name = strings.TrimSpace(body.Name)
		body.URL = strings.TrimSpace(body.URL)
		if body.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		if body.URL == "" {
			return nil, huma.Error400BadRequest("url is required")
		}
		if body.Kind != "proxy" && body.Kind != "replica" {
			return nil, huma.Error400BadRequest("kind must be 'proxy' or 'replica'")
		}
		body.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if u := auth.UserFromContext(ctx); u != nil {
			body.UpdatedBy = u.Email
			if body.UpdatedBy == "" {
				body.UpdatedBy = u.Subject
			}
		}
		registryRemoteUpsert(body)
		// Re-fetch so we return the canonical row (including the
		// LastSync the store preserved across updates).
		saved, _ := registryRemoteFind(body.Name)
		return &setRegistryRemoteOutput{Body: saved}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-registry-remote",
		Method:      "DELETE",
		Path:        "/api/registries/remotes/{name}",
		Summary:     "Delete a remote registry (cluster-admin) — idempotent",
		Tags:        []string{"registries"},
	}, func(_ context.Context, in *registryRemoteNameInput) (*deleteRegistryRemoteOutput, error) {
		registryRemoteDelete(in.Name)
		out := &deleteRegistryRemoteOutput{}
		out.Body.Deleted = in.Name
		return out, nil
	})
}

// ---- inputs / outputs --------------------------------------------

type registryRemoteNameInput struct {
	Name string `path:"name" doc:"Remote-registry slug" minLength:"1" maxLength:"64"`
}

type listRegistryRemotesOutput struct {
	Body []RegistryRemote
}

type getRegistryRemoteOutput struct {
	Body RegistryRemote
}

type setRegistryRemoteInput struct {
	Body RegistryRemote
}

type setRegistryRemoteOutput struct {
	Body RegistryRemote
}

type deleteRegistryRemoteOutput struct {
	Body struct {
		Deleted string `json:"deleted"`
	}
}

type searchRegistryRemoteInput struct {
	Name string `path:"name"  doc:"Remote-registry slug" minLength:"1" maxLength:"64"`
	Q    string `query:"q"    doc:"Repository substring to search for. Empty returns a curated 'featured' subset."`
}

// RemoteSearchHit is one row in the search response. Same loose
// shape as the artifact catalogue so the dashboard renders it with
// the existing columns.
type RemoteSearchHit struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Type       string `json:"type"   doc:"container / raw / chart / model …"`
	Arches     string `json:"arches" doc:"Comma-separated arch list, e.g. 'amd64, arm64'"`
	Size       string `json:"size"   doc:"Human-readable size, e.g. '52 MiB'"`
	Pushed     string `json:"pushed" doc:"Relative timestamp, e.g. '3d ago'"`
}

type searchRegistryRemoteOutput struct {
	Body []RemoteSearchHit
}
