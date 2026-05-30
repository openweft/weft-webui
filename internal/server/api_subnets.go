// api_subnets.go — per-network subnet sub-resource endpoints.
//
//   GET    /api/networks/{key}/subnets
//   POST   /api/networks/{key}/subnets        (admin) — upsert by name/uuid
//   DELETE /api/networks/{key}/subnets/{uuid} (admin)

package server

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

func mountSubnetsAPI(api huma.API, scope Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "list-subnets",
		Method:      "GET",
		Path:        "/api/networks/{key}/subnets",
		Summary:     "List subnets attached to one network",
		Tags:        []string{"networks", "subnets"},
	}, func(_ context.Context, in *subnetsListInput) (*subnetsListOutput, error) {
		return &subnetsListOutput{Body: listSubnets(in.Key)}, nil
	})

	if scope != ScopeAdmin {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID:   "set-subnet",
		Method:        "POST",
		Path:          "/api/networks/{key}/subnets",
		Summary:       "Create or update a subnet inside a network (admin)",
		Description:   "Upsert by uuid (when supplied) or by name. UpdatedAt / UpdatedBy stamped server-side.",
		Tags:          []string{"networks", "subnets"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *subnetSetInput) (*subnetSetOutput, error) {
		s := in.Body
		s.Name = strings.TrimSpace(s.Name)
		s.CIDR = strings.TrimSpace(s.CIDR)
		s.Gateway = strings.TrimSpace(s.Gateway)
		if s.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		if s.CIDR == "" {
			return nil, huma.Error400BadRequest("cidr is required")
		}
		s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if u := auth.UserFromContext(ctx); u != nil {
			s.UpdatedBy = u.Email
			if s.UpdatedBy == "" {
				s.UpdatedBy = u.Subject
			}
		}
		saved, _ := upsertSubnet(in.Key, s)
		return &subnetSetOutput{Body: saved}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-subnet",
		Method:        "DELETE",
		Path:          "/api/networks/{key}/subnets/{uuid}",
		Summary:       "Delete a subnet (admin) — idempotent",
		Tags:          []string{"networks", "subnets"},
		DefaultStatus: 200,
	}, func(_ context.Context, in *subnetDeleteInput) (*subnetDeleteOutput, error) {
		deleteSubnet(in.Key, in.UUID)
		out := &subnetDeleteOutput{}
		out.Body.Deleted = in.UUID
		return out, nil
	})
}

type subnetsListInput struct {
	Key string `path:"key" doc:"Network identifier" minLength:"1" maxLength:"128"`
}

type subnetsListOutput struct {
	Body []Subnet
}

type subnetSetInput struct {
	Key  string `path:"key" doc:"Network identifier" minLength:"1" maxLength:"128"`
	Body Subnet
}

type subnetSetOutput struct {
	Body Subnet
}

type subnetDeleteInput struct {
	Key  string `path:"key" doc:"Network identifier" minLength:"1" maxLength:"128"`
	UUID string `path:"uuid" doc:"Subnet uuid" minLength:"1" maxLength:"64"`
}

type subnetDeleteOutput struct {
	Body struct {
		Deleted string `json:"deleted"`
	}
}
