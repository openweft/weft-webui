// api_inventory.go — typed CRUD endpoints for the AZ / Rack / Host
// hierarchy. The data still lives in resourceByID["azs"|"racks"|"hosts"]
// (same shape the tree + map already poll) ; this file only adds
// the mutation surface and stamps row counts so the dashboard sees
// derived totals automatically.
//
// Mounted only when scope == ScopeAdmin — non-admin listeners never
// see these routes (404 rather than 403, no signal). Every write
// emits an audit event.

package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

func mountInventoryAPI(api huma.API, scope Scope) {
	if scope != ScopeAdmin {
		return
	}

	mountAZAPI(api)
	mountRackAPI(api)
	mountHostAPI(api)
}

// -------------------- AZs ---------------------------------------

type APIAZ struct {
	UUID   string `json:"uuid,omitempty" doc:"Server-generated stable id ; clients omit on create" readOnly:"true"`
	Code   string `json:"code" doc:"Short uppercase code, e.g. DC-A" example:"DC-A" minLength:"1" maxLength:"32"`
	Name   string `json:"name" doc:"Human-friendly name" example:"Datacenter Alpha" maxLength:"128"`
	Region string `json:"region" doc:"Region tag (eu-west-1, us-east-1, ...)" example:"eu-west-1" maxLength:"64"`
	Status string `json:"status" doc:"Operational state" example:"active" enum:"active,draining,down,provisioning"`
}

type createAZInput struct {
	Body APIAZ
}
type updateAZInput struct {
	UUID string `path:"uuid" doc:"AZ uuid" minLength:"4" maxLength:"64"`
	Body APIAZ
}
type uuidPathInput struct {
	UUID string `path:"uuid" doc:"Row uuid" minLength:"4" maxLength:"64"`
}
type azOutput struct {
	Body APIAZ
}
type deleteOutput struct {
	Body struct {
		Deleted string `json:"deleted" doc:"UUID of the removed row"`
		Cascade struct {
			Racks int `json:"racks,omitempty"`
			Hosts int `json:"hosts,omitempty"`
		} `json:"cascade,omitempty"`
	}
}

func mountAZAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-az",
		Method:      "POST",
		Path:        "/api/azs",
		Summary:     "Register an availability zone (cluster-admin)",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *createAZInput) (*azOutput, error) {
		row := normaliseAZ(in.Body)
		if row.Code == "" {
			return nil, huma.Error400BadRequest("code is required")
		}
		if azCodeExists(row.Code) {
			Audit(ctx, auditLogger, "az.create", "az", row.Code, "conflict", nil, nil)
			return nil, huma.Error409Conflict("az code already registered: " + row.Code)
		}
		row.UUID = newUUID("az")
		if row.Status == "" {
			row.Status = "active"
		}
		appendAZ(map[string]any{
			"uuid":   row.UUID,
			"code":   row.Code,
			"name":   row.Name,
			"region": row.Region,
			"racks":  0,
			"hosts":  0,
			"status": row.Status,
		})
		Audit(ctx, auditLogger, "az.create", "az", row.UUID, "", nil, map[string]string{"code": row.Code})
		return &azOutput{Body: row}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-az",
		Method:      "PUT",
		Path:        "/api/azs/{uuid}",
		Summary:     "Update an availability zone (cluster-admin)",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *updateAZInput) (*azOutput, error) {
		patch := normaliseAZ(in.Body)
		ok := updateAZRow(in.UUID, func(row map[string]any) {
			if patch.Name != "" {
				row["name"] = patch.Name
			}
			if patch.Region != "" {
				row["region"] = patch.Region
			}
			if patch.Status != "" {
				row["status"] = patch.Status
			}
			// code is the join key (racks + hosts reference it by string).
			// Changing it would require a coordinated rewrite of those rows ;
			// keep it immutable in the mock layer.
		})
		if !ok {
			return nil, huma.Error404NotFound("az not found")
		}
		row, _, _ := findAZByUUID(in.UUID)
		out := azFromRow(row)
		Audit(ctx, auditLogger, "az.update", "az", in.UUID, "", nil, map[string]string{"code": out.Code})
		return &azOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-az",
		Method:      "DELETE",
		Path:        "/api/azs/{uuid}",
		Summary:     "Delete an availability zone + cascade racks/hosts (cluster-admin)",
		Description: "Cascade-deletes every rack and host whose `az` column matches the AZ code. Use with care.",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *uuidPathInput) (*deleteOutput, error) {
		azDel, rk, hs := deleteAZRow(in.UUID)
		if azDel == 0 {
			return nil, huma.Error404NotFound("az not found")
		}
		out := &deleteOutput{}
		out.Body.Deleted = in.UUID
		out.Body.Cascade.Racks = rk
		out.Body.Cascade.Hosts = hs
		Audit(ctx, auditLogger, "az.delete", "az", in.UUID, "", nil, nil)
		return out, nil
	})
}

func normaliseAZ(in APIAZ) APIAZ {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.Region = strings.TrimSpace(in.Region)
	in.Status = strings.TrimSpace(in.Status)
	return in
}

func azFromRow(row map[string]any) APIAZ {
	if row == nil {
		return APIAZ{}
	}
	return APIAZ{
		UUID:   str(row["uuid"]),
		Code:   str(row["code"]),
		Name:   str(row["name"]),
		Region: str(row["region"]),
		Status: str(row["status"]),
	}
}

// -------------------- Racks -------------------------------------

type APIRack struct {
	UUID     string `json:"uuid,omitempty" doc:"Server-generated" readOnly:"true"`
	Code     string `json:"code" doc:"Rack code within its AZ" example:"R1" minLength:"1" maxLength:"32"`
	AZ       string `json:"az" doc:"Parent AZ code" example:"DC-A" minLength:"1" maxLength:"32"`
	Position string `json:"position" doc:"Free-form physical position (row1-col1, ...)" maxLength:"64"`
	// HeightU is the rack's total height in rack units. Standard
	// values are 42 (open frame) ; 24, 12 or 9 for half-height or
	// office racks. Zero / unset is treated as 42 by the dashboard.
	HeightU int    `json:"height_u" doc:"Rack total height in U (default 42)" example:"42" minimum:"1" maximum:"60"`
	Status  string `json:"status" doc:"Operational state" example:"active" enum:"active,draining,down,provisioning"`
}
type createRackInput struct{ Body APIRack }
type updateRackInput struct {
	UUID string `path:"uuid" doc:"Rack uuid" minLength:"4" maxLength:"64"`
	Body APIRack
}
type rackOutput struct {
	Body APIRack
}

func mountRackAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-rack",
		Method:      "POST",
		Path:        "/api/racks",
		Summary:     "Register a rack (cluster-admin)",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *createRackInput) (*rackOutput, error) {
		row := normaliseRack(in.Body)
		if row.Code == "" || row.AZ == "" {
			return nil, huma.Error400BadRequest("code and az are required")
		}
		if !azCodeExists(row.AZ) {
			return nil, huma.Error400BadRequest("parent az not registered: " + row.AZ)
		}
		if rackCodeExistsInAZ(row.AZ, row.Code) {
			Audit(ctx, auditLogger, "rack.create", "rack", row.Code, "conflict", nil, map[string]string{"az": row.AZ})
			return nil, huma.Error409Conflict("rack code already registered in az: " + row.AZ + "/" + row.Code)
		}
		row.UUID = newUUID("rack")
		if row.Status == "" {
			row.Status = "active"
		}
		if row.HeightU <= 0 {
			row.HeightU = 42
		}
		appendRack(map[string]any{
			"uuid":     row.UUID,
			"code":     row.Code,
			"az":       row.AZ,
			"position": row.Position,
			"height_u": row.HeightU,
			"hosts":    0,
			"status":   row.Status,
		})
		Audit(ctx, auditLogger, "rack.create", "rack", row.UUID, "", nil, map[string]string{
			"az":   row.AZ,
			"code": row.Code,
		})
		return &rackOutput{Body: row}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-rack",
		Method:      "PUT",
		Path:        "/api/racks/{uuid}",
		Summary:     "Update a rack (cluster-admin)",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *updateRackInput) (*rackOutput, error) {
		patch := normaliseRack(in.Body)
		ok := updateRackRow(in.UUID, func(row map[string]any) {
			if patch.Position != "" {
				row["position"] = patch.Position
			}
			if patch.HeightU > 0 {
				row["height_u"] = patch.HeightU
			}
			if patch.Status != "" {
				row["status"] = patch.Status
			}
		})
		if !ok {
			return nil, huma.Error404NotFound("rack not found")
		}
		row, _ := findRackByUUID(in.UUID)
		out := rackFromRow(row)
		Audit(ctx, auditLogger, "rack.update", "rack", in.UUID, "", nil, nil)
		return &rackOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-rack",
		Method:      "DELETE",
		Path:        "/api/racks/{uuid}",
		Summary:     "Delete a rack + cascade its hosts (cluster-admin)",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *uuidPathInput) (*deleteOutput, error) {
		rkDel, hs := deleteRackRow(in.UUID)
		if rkDel == 0 {
			return nil, huma.Error404NotFound("rack not found")
		}
		out := &deleteOutput{}
		out.Body.Deleted = in.UUID
		out.Body.Cascade.Hosts = hs
		Audit(ctx, auditLogger, "rack.delete", "rack", in.UUID, "", nil, nil)
		return out, nil
	})
}

func normaliseRack(in APIRack) APIRack {
	in.Code = strings.TrimSpace(in.Code)
	in.AZ = strings.TrimSpace(in.AZ)
	in.Position = strings.TrimSpace(in.Position)
	in.Status = strings.TrimSpace(in.Status)
	return in
}

func rackFromRow(row map[string]any) APIRack {
	if row == nil {
		return APIRack{}
	}
	return APIRack{
		UUID:     str(row["uuid"]),
		Code:     str(row["code"]),
		AZ:       str(row["az"]),
		Position: str(row["position"]),
		HeightU:  toInt(row["height_u"]),
		Status:   str(row["status"]),
	}
}

// -------------------- Hosts -------------------------------------

type APIHost struct {
	UUID       string `json:"uuid,omitempty" doc:"Server-generated" readOnly:"true"`
	Name       string `json:"name" doc:"Hostname, cluster-unique" example:"dc-a-r1-h2" minLength:"1" maxLength:"128"`
	AZ         string `json:"az" doc:"Parent AZ code" example:"DC-A" minLength:"1" maxLength:"32"`
	Rack       string `json:"rack" doc:"Parent rack code within the AZ" example:"R1" minLength:"1" maxLength:"32"`
	Arch       string `json:"arch" doc:"CPU architecture" example:"arm64" enum:"amd64,arm64,riscv64,loong64"`
	Hypervisor string `json:"hypervisor" doc:"Hypervisor backend" example:"qemu-kvm" enum:"qemu-kvm,apple-vz"`
	GPU        string `json:"gpu" doc:"GPU complement, Flavor.gpu notation, empty for none" example:"2×A100-40G" maxLength:"64"`
	// PositionU is the top-of-unit slot the chassis occupies in the
	// parent rack, 1-based, counted from the TOP (U1 is the top slot
	// — same convention as count.racku.la and most data-center
	// management tools). Zero / unset means "let the dashboard
	// auto-pack at the first free slot when rendering".
	PositionU int `json:"position_u" doc:"Top-of-unit slot in the rack (1-based, 1 = top)" example:"5" minimum:"0" maximum:"60"`
	// HeightU is the chassis height in rack units (1 for a 1U pizza
	// box, 2 for 2U, 4 for a quad-socket beast, ...). Defaults to 1
	// when unset. The rack viz respects this for vertical stretching.
	HeightU int    `json:"height_u" doc:"Chassis height in U (default 1)" example:"2" minimum:"0" maximum:"20"`
	Status  string `json:"status" doc:"Operational state" example:"active" enum:"active,draining,down,provisioning"`
}
type createHostInput struct{ Body APIHost }
type updateHostInput struct {
	UUID string `path:"uuid" doc:"Host uuid" minLength:"4" maxLength:"64"`
	Body APIHost
}
type hostOutput struct {
	Body APIHost
}

func mountHostAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-host",
		Method:      "POST",
		Path:        "/api/hosts",
		Summary:     "Register a host (cluster-admin)",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *createHostInput) (*hostOutput, error) {
		row := normaliseHost(in.Body)
		if row.Name == "" || row.AZ == "" || row.Rack == "" {
			return nil, huma.Error400BadRequest("name, az, and rack are required")
		}
		if !azCodeExists(row.AZ) {
			return nil, huma.Error400BadRequest("parent az not registered: " + row.AZ)
		}
		if !rackCodeExistsInAZ(row.AZ, row.Rack) {
			return nil, huma.Error400BadRequest("parent rack not registered in az: " + row.AZ + "/" + row.Rack)
		}
		if hostNameExists(row.Name) {
			Audit(ctx, auditLogger, "host.create", "host", row.Name, "conflict", nil, nil)
			return nil, huma.Error409Conflict("host name already registered: " + row.Name)
		}
		row.UUID = newUUID("host")
		if row.Status == "" {
			row.Status = "active"
		}
		if row.HeightU <= 0 {
			row.HeightU = 1
		}
		// Validate the U range fits inside the parent rack. We treat
		// PositionU=0 as "unspecified — auto-pack at render time", so
		// only enforce when both fields are set.
		if row.PositionU > 0 {
			if err := validateHostUFitsInRack(row.AZ, row.Rack, row.PositionU, row.HeightU, ""); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
		}
		appendHost(map[string]any{
			"uuid":       row.UUID,
			"name":       row.Name,
			"az":         row.AZ,
			"rack":       row.Rack,
			"arch":       row.Arch,
			"hypervisor": row.Hypervisor,
			"gpu":        row.GPU,
			"position_u": row.PositionU,
			"height_u":   row.HeightU,
			"status":     row.Status,
			"last_seen":  time.Now().UTC().Format("2006-01-02"),
		})
		Audit(ctx, auditLogger, "host.create", "host", row.UUID, "", nil, map[string]string{
			"az":   row.AZ,
			"rack": row.Rack,
			"name": row.Name,
		})
		return &hostOutput{Body: row}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-host",
		Method:      "PUT",
		Path:        "/api/hosts/{uuid}",
		Summary:     "Update a host (cluster-admin)",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *updateHostInput) (*hostOutput, error) {
		patch := normaliseHost(in.Body)
		// Validate U range before mutation — fail-fast keeps the row
		// state consistent. Lookup the existing host so we can fall
		// back to its current az/rack when the patch doesn't include
		// them (PUT bodies in the dashboard usually only carry the
		// fields the operator edited).
		existing, _ := findHostByUUID(in.UUID)
		az := patch.AZ
		if az == "" {
			az = str(existing["az"])
		}
		rack := patch.Rack
		if rack == "" {
			rack = str(existing["rack"])
		}
		posU := patch.PositionU
		if posU == 0 {
			posU = toInt(existing["position_u"])
		}
		htU := patch.HeightU
		if htU == 0 {
			htU = toInt(existing["height_u"])
		}
		if htU == 0 {
			htU = 1
		}
		if posU > 0 {
			if err := validateHostUFitsInRack(az, rack, posU, htU, in.UUID); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
		}
		ok := updateHostRow(in.UUID, func(row map[string]any) {
			if patch.Arch != "" {
				row["arch"] = patch.Arch
			}
			if patch.Hypervisor != "" {
				row["hypervisor"] = patch.Hypervisor
			}
			// gpu may legitimately be set empty (host had a card pulled).
			row["gpu"] = patch.GPU
			if patch.PositionU > 0 {
				row["position_u"] = patch.PositionU
			}
			if patch.HeightU > 0 {
				row["height_u"] = patch.HeightU
			}
			if patch.Status != "" {
				row["status"] = patch.Status
			}
		})
		if !ok {
			return nil, huma.Error404NotFound("host not found")
		}
		row, _ := findHostByUUID(in.UUID)
		out := hostFromRow(row)
		Audit(ctx, auditLogger, "host.update", "host", in.UUID, "", nil, nil)
		return &hostOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-host",
		Method:      "DELETE",
		Path:        "/api/hosts/{uuid}",
		Summary:     "Delete a host (cluster-admin)",
		Description: "Hosts carry microVMs. The dashboard SHOULD block deletion when the count > 0 ; the API doesn't enforce that yet — it's idempotent.",
		Tags:        []string{"inventory"},
	}, func(ctx context.Context, in *uuidPathInput) (*deleteOutput, error) {
		if !deleteHostRow(in.UUID) {
			return nil, huma.Error404NotFound("host not found")
		}
		out := &deleteOutput{}
		out.Body.Deleted = in.UUID
		Audit(ctx, auditLogger, "host.delete", "host", in.UUID, "", nil, nil)
		return out, nil
	})
}

func normaliseHost(in APIHost) APIHost {
	in.Name = strings.TrimSpace(in.Name)
	in.AZ = strings.TrimSpace(in.AZ)
	in.Rack = strings.TrimSpace(in.Rack)
	in.Arch = strings.TrimSpace(in.Arch)
	in.Hypervisor = strings.TrimSpace(in.Hypervisor)
	in.GPU = strings.TrimSpace(in.GPU)
	in.Status = strings.TrimSpace(in.Status)
	return in
}

func hostFromRow(row map[string]any) APIHost {
	if row == nil {
		return APIHost{}
	}
	return APIHost{
		UUID:       str(row["uuid"]),
		Name:       str(row["name"]),
		AZ:         str(row["az"]),
		Rack:       str(row["rack"]),
		Arch:       str(row["arch"]),
		Hypervisor: str(row["hypervisor"]),
		GPU:        str(row["gpu"]),
		PositionU:  toInt(row["position_u"]),
		HeightU:    toInt(row["height_u"]),
		Status:     str(row["status"]),
	}
}

// newUUID returns a short random hex slug for a new inventory row.
// Inventory uuids don't need to be globally unique forever ; we
// just want a stable handle the dashboard can address. 8 random
// bytes (= 16 hex chars) prefixed by the row kind keeps debug
// output legible.
func newUUID(kind string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return kind + "-" + hex.EncodeToString(b[:])
}
