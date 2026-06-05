// api_volumes.go — per-volume metadata + properties endpoints.
//
// Volumes themselves (create / list / delete / resize / attach / detach
// / rename) are still served via the generic resources path + the
// weft-agent RPCs in api_storage.go. This file adds the editable
// dashboard layer on top : free-form description, mount + filesystem
// hints, and a property bag.
//
//   GET  /api/volumes/{key}/metadata
//   PUT  /api/volumes/{key}/metadata        (admin)
//   GET  /api/volumes/{key}/properties
//   POST /api/volumes/{key}/properties      (admin) — upsert one
//   DELETE /api/volumes/{key}/properties/{prop_key}   (admin)
//
// `key` is the volume name today ; the live wiring will switch to the
// volume UUID once weft-agent's RPCs land. The mock store accepts
// either since the resources.go seed uses names.

package server

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/wclient"
)

func mountVolumeMetadataAPI(api huma.API, scope Scope) {
	if scope == ScopeAdmin {
		huma.Register(api, huma.Operation{
			OperationID:   "rename-volume",
			Method:        "PUT",
			Path:          "/api/volumes/{key}",
			Summary:       "Rename a volume (admin)",
			Description:   "Updates the human-readable name. Attached VMs keep referencing the volume by uuid ; this is the dashboard label.",
			Tags:          []string{"volumes"},
			DefaultStatus: 200,
		}, func(_ context.Context, in *renameVolumeInput) (*renameVolumeOutput, error) {
			newName := strings.TrimSpace(in.Body.NewName)
			if newName == "" {
				return nil, huma.Error400BadRequest("new_name is required")
			}
			if newName == in.Key {
				return &renameVolumeOutput{Body: renameVolumeResp{Name: newName}}, nil
			}
			if !renameVolumeRow(in.Key, newName) {
				return nil, huma.Error404NotFound("volume not found")
			}
			// Carry the metadata + property bag along with the rename.
			volumeMetadataMu.Lock()
			if m, ok := volumeMetadataByID[in.Key]; ok {
				volumeMetadataByID[newName] = m
				delete(volumeMetadataByID, in.Key)
			}
			volumeMetadataMu.Unlock()
			volumePropsMu.Lock()
			if ps, ok := volumeProps[in.Key]; ok {
				volumeProps[newName] = ps
				delete(volumeProps, in.Key)
			}
			volumePropsMu.Unlock()
			return &renameVolumeOutput{Body: renameVolumeResp{Name: newName}}, nil
		})
	}

	huma.Register(api, huma.Operation{
		OperationID: "get-volume-metadata",
		Method:      "GET",
		Path:        "/api/volumes/{key}/metadata",
		Summary:     "Get the editable metadata layer for one volume",
		Tags:        []string{"volumes"},
	}, func(_ context.Context, in *volumeKeyInput) (*getVolumeMetadataOutput, error) {
		return &getVolumeMetadataOutput{Body: getVolumeMetadata(in.Key)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-volume-properties",
		Method:      "GET",
		Path:        "/api/volumes/{key}/properties",
		Summary:     "List the property bag attached to a volume",
		Tags:        []string{"volumes"},
	}, func(_ context.Context, in *volumeKeyInput) (*listVolumePropertiesOutput, error) {
		return &listVolumePropertiesOutput{Body: listVolumeProperties(in.Key)}, nil
	})

	if scope != ScopeAdmin {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID:   "set-volume-metadata",
		Method:        "PUT",
		Path:          "/api/volumes/{key}/metadata",
		Summary:       "Replace the editable metadata for one volume (admin)",
		Description:   "Free-form description + suggested mount-point + filesystem hint. UpdatedAt / UpdatedBy are stamped server-side.",
		Tags:          []string{"volumes"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *setVolumeMetadataInput) (*setVolumeMetadataOutput, error) {
		m := in.Body
		m.Description = strings.TrimSpace(m.Description)
		m.MountPoint = strings.TrimSpace(m.MountPoint)
		m.Filesystem = strings.TrimSpace(m.Filesystem)
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if u := auth.UserFromContext(ctx); u != nil {
			m.UpdatedBy = u.Email
			if m.UpdatedBy == "" {
				m.UpdatedBy = u.Subject
			}
		}
		setVolumeMetadata(in.Key, m)
		return &setVolumeMetadataOutput{Body: m}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "set-volume-property",
		Method:        "POST",
		Path:          "/api/volumes/{key}/properties",
		Summary:       "Upsert a property on a volume (admin)",
		Description:   "Inserts or updates by Key. Live-first via weft-agent's SetVolumeProperty (proto v0.9.0) ; mock store mirrored on success and is the source of truth on Unimplemented. The `key` path segment doubles as the volume UUID when live wiring is on (the mock stores by name ; live keys by UUID — the dashboard already moved to UUID-shaped keys for new volumes).",
		Tags:          []string{"volumes"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *setVolumePropertyInput) (*setVolumePropertyOutput, error) {
		p := in.Body
		p.Key = strings.TrimSpace(p.Key)
		if p.Key == "" {
			return nil, huma.Error400BadRequest("key is required")
		}
		p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if live != nil {
			err := live.SetVolumeProperty(ctx, in.Key, p.Key, p.Value)
			if err == nil {
				setVolumeProperty(in.Key, p)
				Audit(ctx, auditLogger, "volume.property.set", "volume", in.Key, "", nil,
					map[string]string{"key": p.Key})
				return &setVolumePropertyOutput{Body: p}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		setVolumeProperty(in.Key, p)
		Audit(ctx, auditLogger, "volume.property.set", "volume", in.Key, "", nil,
			map[string]string{"key": p.Key})
		return &setVolumePropertyOutput{Body: p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-volume-property",
		Method:        "DELETE",
		Path:          "/api/volumes/{key}/properties/{prop_key}",
		Summary:       "Delete one property on a volume (admin) — idempotent",
		Description:   "Live-first via weft-agent's DeleteVolumeProperty (proto v0.9.0) ; mock store mirrored on success and is the source of truth on Unimplemented.",
		Tags:          []string{"volumes"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *deleteVolumePropertyInput) (*deleteVolumePropertyOutput, error) {
		if live != nil {
			err := live.DeleteVolumeProperty(ctx, in.Key, in.PropKey)
			if err == nil {
				deleteVolumeProperty(in.Key, in.PropKey)
				Audit(ctx, auditLogger, "volume.property.delete", "volume", in.Key, "", nil,
					map[string]string{"key": in.PropKey})
				out := &deleteVolumePropertyOutput{}
				out.Body.Deleted = in.PropKey
				return out, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		deleteVolumeProperty(in.Key, in.PropKey)
		Audit(ctx, auditLogger, "volume.property.delete", "volume", in.Key, "", nil,
			map[string]string{"key": in.PropKey})
		out := &deleteVolumePropertyOutput{}
		out.Body.Deleted = in.PropKey
		return out, nil
	})
}

// ---- inputs / outputs -------------------------------------------

type volumeKeyInput struct {
	Key string `path:"key" doc:"Volume identifier (name today ; uuid once live wiring lands)" minLength:"1" maxLength:"128"`
}

type getVolumeMetadataOutput struct {
	Body VolumeMetadata
}

type setVolumeMetadataInput struct {
	Key  string `path:"key" doc:"Volume identifier" minLength:"1" maxLength:"128"`
	Body VolumeMetadata
}

type setVolumeMetadataOutput struct {
	Body VolumeMetadata
}

type listVolumePropertiesOutput struct {
	Body []VolumeProperty
}

type setVolumePropertyInput struct {
	Key  string `path:"key" doc:"Volume identifier" minLength:"1" maxLength:"128"`
	Body VolumeProperty
}

type setVolumePropertyOutput struct {
	Body VolumeProperty
}

type deleteVolumePropertyInput struct {
	Key     string `path:"key"      doc:"Volume identifier" minLength:"1" maxLength:"128"`
	PropKey string `path:"prop_key" doc:"Property key to delete" minLength:"1" maxLength:"128"`
}

type deleteVolumePropertyOutput struct {
	Body struct {
		Deleted string `json:"deleted"`
	}
}

type renameVolumeInput struct {
	Key  string `path:"key" doc:"Current volume identifier (name today ; uuid once live wiring lands)" minLength:"1" maxLength:"128"`
	Body struct {
		NewName string `json:"new_name" doc:"New human-readable name ; must be unique within the project" minLength:"1" maxLength:"128"`
	}
}

type renameVolumeResp struct {
	Name string `json:"name"`
}

type renameVolumeOutput struct {
	Body renameVolumeResp
}
