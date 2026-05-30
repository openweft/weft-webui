// api_vm_authz.go — per-VM authorized-groups endpoints.
//
//   GET    /api/microvms/{name}/authorized-groups
//   POST   /api/microvms/{name}/authorized-groups        (admin) — add
//   DELETE /api/microvms/{name}/authorized-groups/{tenant}/{group} (admin)
//   GET    /api/microvms/{name}/effective-keys           — union of
//                                                          explicit + group-derived
//
// The explicit per-VM key flow (api_microvm_metadata.go) is preserved
// so existing assignments survive ; the union is what weft-microvm-agent
// receives in the resolved KeySet on the next NATS push.

package server

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

func mountVMAuthzAPI(api huma.API, scope Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "list-vm-authorized-groups",
		Method:      "GET",
		Path:        "/api/microvms/{name}/authorized-groups",
		Summary:     "List groups authorized to access one microVM",
		Tags:        []string{"microvms", "authz"},
	}, func(_ context.Context, in *vmAuthzListInput) (*vmAuthzListOutput, error) {
		return &vmAuthzListOutput{Body: listVMAuthorizedGroups(in.Name)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-vm-effective-keys",
		Method:      "GET",
		Path:        "/api/microvms/{name}/effective-keys",
		Summary:     "Derived SSH key set this microVM will see",
		Description: "Union of explicit per-VM assignments AND keys whose owner is a member of any authorized group. Same shape as the per-VM SSH-keys list.",
		Tags:        []string{"microvms", "authz"},
	}, func(ctx context.Context, in *vmAuthzListInput) (*effectiveKeysOutput, error) {
		names := effectiveVMKeyNames(in.Name)
		out := make([]EffectiveKey, 0, len(names))
		for _, n := range names {
			k, ok := sshKeysCatalogue.Get(ctx, n)
			if !ok {
				continue
			}
			source := "direct"
			// Quick heuristic for the SOURCE column : if the key has
			// an owner AND that owner is in any authorized group, mark
			// it as "group" ; otherwise "direct". Keys can match both ;
			// the resolver-side dedup just picks the first.
			vmAuthzMu.Lock()
			groups := append([]AuthorizedGroup(nil), vmAuthorized[in.Name]...)
			vmAuthzMu.Unlock()
			for _, g := range groups {
				for _, email := range resolveGroupMembers(g.Tenant, g.Group) {
					if email == k.Owner {
						source = "group:" + g.Tenant + "/" + g.Group
					}
				}
				if source != "direct" {
					break
				}
			}
			out = append(out, EffectiveKey{
				Name:        k.Name,
				Fingerprint: k.Fingerprint,
				Owner:       k.Owner,
				Source:      source,
			})
		}
		return &effectiveKeysOutput{Body: out}, nil
	})

	if scope != ScopeAdmin {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID:   "add-vm-authorized-group",
		Method:        "POST",
		Path:          "/api/microvms/{name}/authorized-groups",
		Summary:       "Authorize a (tenant, group) pair on this microVM (admin)",
		Tags:          []string{"microvms", "authz"},
		DefaultStatus: 200,
	}, func(_ context.Context, in *vmAuthzAddInput) (*vmAuthzAddOutput, error) {
		tenant := strings.TrimSpace(in.Body.Tenant)
		group := strings.TrimSpace(in.Body.Group)
		if tenant == "" || group == "" {
			return nil, huma.Error400BadRequest("tenant and group are required")
		}
		entry := AuthorizedGroup{
			Tenant: tenant, Group: group,
			AddedAt: time.Now().UTC().Format(time.RFC3339),
		}
		addVMAuthorizedGroup(in.Name, entry)
		return &vmAuthzAddOutput{Body: entry}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "remove-vm-authorized-group",
		Method:        "DELETE",
		Path:          "/api/microvms/{name}/authorized-groups/{tenant}/{group}",
		Summary:       "Remove a (tenant, group) authorization from this microVM (admin) — idempotent",
		Tags:          []string{"microvms", "authz"},
		DefaultStatus: 200,
	}, func(_ context.Context, in *vmAuthzRemoveInput) (*vmAuthzRemoveOutput, error) {
		removeVMAuthorizedGroup(in.Name, in.Tenant, in.Group)
		out := &vmAuthzRemoveOutput{}
		out.Body.Deleted = in.Tenant + "/" + in.Group
		return out, nil
	})

	// _ = auth dependency for editorial parity with the rest of the file.
	_ = auth.UserFromContext
}

type vmAuthzListInput struct {
	Name string `path:"name" doc:"microVM name" minLength:"1" maxLength:"128"`
}

type vmAuthzListOutput struct {
	Body []AuthorizedGroup
}

type vmAuthzAddInput struct {
	Name string `path:"name" doc:"microVM name" minLength:"1" maxLength:"128"`
	Body struct {
		Tenant string `json:"tenant" doc:"Tenant the group belongs to" minLength:"1" maxLength:"128"`
		Group  string `json:"group"  doc:"Group name within the tenant" minLength:"1" maxLength:"128"`
	}
}

type vmAuthzAddOutput struct {
	Body AuthorizedGroup
}

type vmAuthzRemoveInput struct {
	Name   string `path:"name"   doc:"microVM name" minLength:"1" maxLength:"128"`
	Tenant string `path:"tenant" doc:"Tenant"        minLength:"1" maxLength:"128"`
	Group  string `path:"group"  doc:"Group name"    minLength:"1" maxLength:"128"`
}

type vmAuthzRemoveOutput struct {
	Body struct {
		Deleted string `json:"deleted"`
	}
}

// EffectiveKey is the wire shape returned by /effective-keys. Lighter
// than VMSSHKey (no public_key body, no AddedAt) since the consumer
// is the dashboard's drawer ; the agent's NATS payload is unchanged.
type EffectiveKey struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Owner       string `json:"owner"`
	Source      string `json:"source" doc:"'direct' or 'group:<tenant>/<group>'"`
}

type effectiveKeysOutput struct {
	Body []EffectiveKey
}
