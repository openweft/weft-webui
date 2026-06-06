// api_editable.go — generic editable-metadata + rename endpoints for
// resources whose drawer needs just { description + rename } :
//
//   * routers           — /api/routers/{key}{,/metadata}
//   * floating-ips      — /api/floating-ips/{key}{,/metadata}
//   * scheduling-rules  — /api/scheduling-rules/{key}{,/metadata}
//
// Volumes + networks have their own typed surface (extra fields) ;
// this file is for the long tail.

package server

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

func mountEditableMetadataAPI(api huma.API, scope Scope) {
	mountOneEditable(api, scope, "routers", routerMetadata)
	mountOneEditable(api, scope, "floating-ips", floatingIPMetadata)
	mountOneEditable(api, scope, "scheduling-rules", schedulingRuleMetadata)
	mountSchedulingRuleMicroVMsAPI(api)
}

func mountSchedulingRuleMicroVMsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-scheduling-rule-microvms",
		Method:      "GET",
		Path:        "/api/scheduling-rules/{key}/microvms",
		Summary:     "List the microVMs deployed under one scheduling rule",
		Description: "Filters the microvms catalogue by the row's `scheduling_rule` field (set when a rule expands to N replicas).",
		Tags:        []string{"scheduling-rules"},
	}, func(_ context.Context, in *schedulingRuleMicroVMsInput) (*schedulingRuleMicroVMsOutput, error) {
		out := make([]map[string]any, 0)
		res, ok := resourceByID["microvms"]
		if !ok {
			return &schedulingRuleMicroVMsOutput{Body: out}, nil
		}
		for _, row := range res.Rows {
			if str(row["scheduling_rule"]) == in.Key {
				out = append(out, row)
			}
		}
		return &schedulingRuleMicroVMsOutput{Body: out}, nil
	})
}

type schedulingRuleMicroVMsInput struct {
	Key string `path:"key" doc:"Scheduling-rule name" minLength:"1" maxLength:"128"`
}

type schedulingRuleMicroVMsOutput struct {
	Body []map[string]any
}

func mountOneEditable(api huma.API, scope Scope, resID string, store *metadataStore) {
	// GET /api/<resID>/{key}/metadata
	huma.Register(api, huma.Operation{
		OperationID: "get-" + resID + "-metadata",
		Method:      "GET",
		Path:        "/api/" + resID + "/{key}/metadata",
		Summary:     "Get editable metadata for one " + singular(resID),
		Tags:        []string{resID},
	}, func(_ context.Context, in *editableKeyInput) (*editableMetadataOutput, error) {
		return &editableMetadataOutput{Body: store.get(in.Key)}, nil
	})

	if !scope.Has(ScopeAdmin) {
		return
	}

	// PUT /api/<resID>/{key}/metadata
	huma.Register(api, huma.Operation{
		OperationID:   "set-" + resID + "-metadata",
		Method:        "PUT",
		Path:          "/api/" + resID + "/{key}/metadata",
		Summary:       "Replace editable metadata for one " + singular(resID) + " (admin)",
		Tags:          []string{resID},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *setEditableMetadataInput) (*editableMetadataOutput, error) {
		m := in.Body
		m.Description = strings.TrimSpace(m.Description)
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if u := auth.UserFromContext(ctx); u != nil {
			m.UpdatedBy = u.Email
			if m.UpdatedBy == "" {
				m.UpdatedBy = u.Subject
			}
		}
		store.set(in.Key, m)
		return &editableMetadataOutput{Body: m}, nil
	})

	// PUT /api/<resID>/{key} — rename
	huma.Register(api, huma.Operation{
		OperationID:   "rename-" + resID + "-row",
		Method:        "PUT",
		Path:          "/api/" + resID + "/{key}",
		Summary:       "Rename one " + singular(resID) + " (admin)",
		Tags:          []string{resID},
		DefaultStatus: 200,
	}, func(_ context.Context, in *renameRowInput) (*renameRowOutput, error) {
		newName := strings.TrimSpace(in.Body.NewName)
		if newName == "" {
			return nil, huma.Error400BadRequest("new_name is required")
		}
		if newName == in.Key {
			return &renameRowOutput{Body: renameRowResp{Name: newName}}, nil
		}
		if !renameResourceRow(resID, in.Key, newName) {
			return nil, huma.Error404NotFound(singular(resID) + " not found")
		}
		store.rename(in.Key, newName)
		return &renameRowOutput{Body: renameRowResp{Name: newName}}, nil
	})
}

// singular strips a trailing "s" for the doc text (best-effort ;
// "scheduling-rules" → "scheduling-rule", "floating-ips" → "floating-ip").
func singular(s string) string {
	if strings.HasSuffix(s, "s") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}

type editableKeyInput struct {
	Key string `path:"key" doc:"Row identifier (name or uuid)" minLength:"1" maxLength:"128"`
}

type editableMetadataOutput struct {
	Body EditableMetadata
}

type setEditableMetadataInput struct {
	Key  string `path:"key" doc:"Row identifier" minLength:"1" maxLength:"128"`
	Body EditableMetadata
}

type renameRowInput struct {
	Key  string `path:"key" doc:"Current row identifier" minLength:"1" maxLength:"128"`
	Body struct {
		NewName string `json:"new_name" doc:"New human-readable name" minLength:"1" maxLength:"128"`
	}
}

type renameRowResp struct {
	Name string `json:"name"`
}

type renameRowOutput struct {
	Body renameRowResp
}
