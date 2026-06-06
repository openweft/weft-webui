// api_scripts.go — typed provisioning-script catalogue endpoints.
// Source of truth is scriptsCatalogue (live-first → mem fallback ;
// see scripts.go). Read endpoints exposed on both listeners ;
// write endpoints are admin-only and only registered when scope ==
// ScopeAdmin (so the user listener returns 404 rather than 403, no
// "you're not allowed" signal).

package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

// APIScript is the typed wire shape exposed by /api/scripts/*. Body
// is the literal sh source ; UpdatedAt + UpdatedBy are server-stamped
// (the client's value is overwritten in Set, so a wire client can't
// lie about provenance).
type APIScript struct {
	Name        string `json:"name" doc:"Script name (must be unique)" example:"deploy-nginx" minLength:"1" maxLength:"128"`
	Description string `json:"description" doc:"Short description shown in listings" example:"Installs nginx + copies the site" maxLength:"512"`
	Body        string `json:"body" doc:"POSIX sh source" example:"#!/bin/sh\\nset -eu\\napk add nginx"`
	UpdatedAt   string `json:"updated_at" doc:"RFC3339, server-stamped" example:"2026-05-29T10:00:00Z" readOnly:"true"`
	UpdatedBy   string `json:"updated_by" doc:"OIDC sub / email of the last editor" example:"alice@x" readOnly:"true"`
}

func toAPIScript(s Script) APIScript {
	return APIScript{
		Name: s.Name, Description: s.Description, Body: s.Body,
		UpdatedAt: s.UpdatedAt, UpdatedBy: s.UpdatedBy,
	}
}

func fromAPIScript(s APIScript) Script {
	return Script{
		Name: s.Name, Description: s.Description, Body: s.Body,
		UpdatedAt: s.UpdatedAt, UpdatedBy: s.UpdatedBy,
	}
}

func mountScriptsAPI(api huma.API, scope Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "list-scripts",
		Method:      http.MethodGet,
		Path:        "/api/scripts",
		Summary:     "List provisioning scripts (metadata only — body omitted)",
		Description: "The body is the heavy payload ; the CreateVMModal picker uses this listing then lazy-loads bodies via GET /api/scripts/{name}.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, _ *struct{}) (*listScriptsOutput, error) {
		ss, err := scriptsCatalogue.List(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list scripts", err)
		}
		out := &listScriptsOutput{}
		out.Body = make([]APIScript, 0, len(ss))
		for _, s := range ss {
			// Strip body from the listing : the payload can be hundreds
			// of lines, the modal only needs metadata. GET /api/scripts/
			// {name} returns the body.
			s.Body = ""
			out.Body = append(out.Body, toAPIScript(s))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-script",
		Method:      http.MethodGet,
		Path:        "/api/scripts/{name}",
		Summary:     "Get one script (body included)",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, in *scriptNameInput) (*getScriptOutput, error) {
		s, ok := scriptsCatalogue.Get(ctx, in.Name)
		if !ok {
			return nil, huma.Error404NotFound("no such script: " + in.Name)
		}
		return &getScriptOutput{Body: toAPIScript(s)}, nil
	})

	if !scope.Has(ScopeAdmin) {
		return
	}

	// --- Admin-only ---------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "set-script",
		Method:      http.MethodPost,
		Path:        "/api/scripts",
		Summary:     "Create or update a script (admin)",
		Description: "UpdatedAt + UpdatedBy are server-stamped from the auth context ; the wire can't lie about provenance.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, in *setScriptInput) (*setScriptOutput, error) {
		body := fromAPIScript(in.Body)
		body.Name = strings.TrimSpace(body.Name)
		if body.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		body.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if u := auth.UserFromContext(ctx); u != nil {
			body.UpdatedBy = u.Email
			if body.UpdatedBy == "" {
				body.UpdatedBy = u.Subject
			}
		}
		if err := scriptsCatalogue.Set(ctx, body); err != nil {
			return nil, huma.Error500InternalServerError("set script", err)
		}
		return &setScriptOutput{Body: toAPIScript(body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-script",
		Method:      http.MethodDelete,
		Path:        "/api/scripts/{name}",
		Summary:     "Delete a script (admin) — idempotent",
		Description: "Missing scripts return 200 with the requested name in 'deleted', so a retried client doesn't see a confusing 404.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, in *scriptNameInput) (*deleteScriptOutput, error) {
		if err := scriptsCatalogue.Delete(ctx, in.Name); err != nil {
			return nil, huma.Error500InternalServerError("delete script", err)
		}
		out := &deleteScriptOutput{}
		out.Body.Deleted = in.Name
		return out, nil
	})
}

type listScriptsOutput struct {
	Body []APIScript
}

type scriptNameInput struct {
	Name string `path:"name" doc:"Script name" example:"deploy-nginx" minLength:"1" maxLength:"128"`
}

type getScriptOutput struct {
	Body APIScript
}

type setScriptInput struct {
	Body APIScript
}

type setScriptOutput struct {
	Body APIScript
}

type deleteScriptOutput struct {
	Body struct {
		Deleted string `json:"deleted"`
	}
}
