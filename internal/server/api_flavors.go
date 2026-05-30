// api_flavors.go — typed flavor catalogue endpoints. Source of truth
// is flavorsCatalogue (live-first, mem fallback on Unimplemented ;
// see flavors.go). The hand-rolled handleListFlavors has been retired
// in favour of these typed registrations.

package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// APIFlavor is the typed wire shape exposed by /api/flavors/*.
// Validation tags surface as OpenAPI constraints AND get enforced
// before the handler runs (no hand-rolled bounds-checking).
type APIFlavor struct {
	Name        string `json:"name" doc:"Flavor name (e.g. small / gpu-large)" example:"small" minLength:"1" maxLength:"64"`
	VCPU        int    `json:"vcpu" doc:"vCPU count" example:"2" minimum:"1" maximum:"256"`
	RAM         string `json:"ram" doc:"RAM with unit suffix (Gi/Mi) or raw MB" example:"4Gi" minLength:"1"`
	EphemeralGB int    `json:"ephemeral_gb" doc:"Ephemeral disk in GiB (0 = none)" example:"8" minimum:"0"`
	GPU         string `json:"gpu" doc:"GPU descriptor (empty = none)" example:"1×A100-40G"`
}

func toAPIFlavor(f Flavor) APIFlavor {
	return APIFlavor{
		Name: f.Name, VCPU: f.VCPU, RAM: f.RAM,
		EphemeralGB: f.EphemeralGB, GPU: f.GPU,
	}
}

// mountFlavorsAPI registers the flavor endpoints onto the shared
// huma.API. Writes are 501 today — the catalogue is read-only in the
// webui until weft-agent's etcd-backed wrapper lands ; operator
// edits go through the `weft flavor` CLI.
func mountFlavorsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-flavors",
		Method:      http.MethodGet,
		Path:        "/api/flavors",
		Summary:     "List the cluster's compute flavors",
		Description: "Cluster-wide compute envelope catalogue. Read-open on both listeners since the CreateVMModal picker needs it.",
		Tags:        []string{"flavors"},
	}, func(ctx context.Context, _ *struct{}) (*listFlavorsOutput, error) {
		fl, err := flavorsCatalogue.List(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list flavors", err)
		}
		out := &listFlavorsOutput{}
		out.Body.Flavors = make([]APIFlavor, 0, len(fl))
		for _, f := range fl {
			out.Body.Flavors = append(out.Body.Flavors, toAPIFlavor(f))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-flavor",
		Method:      http.MethodGet,
		Path:        "/api/flavors/{name}",
		Summary:     "Get one flavor by name",
		Tags:        []string{"flavors"},
	}, func(ctx context.Context, in *flavorNameInput) (*getFlavorOutput, error) {
		f, ok := flavorsCatalogue.Get(ctx, in.Name)
		if !ok {
			return nil, huma.Error404NotFound("no such flavor: " + in.Name)
		}
		return &getFlavorOutput{Body: toAPIFlavor(f)}, nil
	})
}

type listFlavorsOutput struct {
	Body struct {
		Flavors []APIFlavor `json:"flavors" doc:"Cluster-wide compute envelope catalogue"`
	}
}

type flavorNameInput struct {
	Name string `path:"name" doc:"Flavor name" example:"small" minLength:"1" maxLength:"64"`
}

type getFlavorOutput struct {
	Body APIFlavor
}
