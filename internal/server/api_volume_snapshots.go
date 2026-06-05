package server

// api_volume_snapshots.go — HTTP/huma surface for volume snapshots.
//
// Five routes, all live-only (the snapshot story is daemon-owned ; an
// in-memory mock would either lie about persistence or surface a
// disabled UI). When the agent isn't wired, requireLiveCtx returns
// 503 and the SPA renders "no live weft daemon" instead of fake data.
//
// Backend dispatch lives one layer down : the agent's adapter routes
// to the reflink store for file-backend parents, to weft-block's
// driver for block-backend parents. The webui doesn't model the
// backend choice in the request shape — operators don't need to spell
// it out at create time, the parent volume's row already encodes it.
//
//	GET    /api/volumes/{uuid}/snapshots         list-volume-snapshots
//	POST   /api/volumes/{uuid}/snapshots         create-volume-snapshot
//	POST   /api/snapshots/{uuid}/restore         restore-volume-snapshot
//	POST   /api/snapshots/{uuid}/revert          revert-volume-snapshot (block-only ; 412 on file parents)
//	DELETE /api/snapshots/{uuid}                 delete-volume-snapshot

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/wclient"
)

func mountVolumeSnapshotsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-volume-snapshots",
		Method:      "GET",
		Path:        "/api/volumes/{uuid}/snapshots",
		Summary:     "List snapshots of a volume",
		Description: "Returns every snapshot taken on the given parent volume, oldest-first by creation time. Empty list when the volume has none. Live-only ; 503 when no weft daemon is wired.",
		Tags:        []string{"volumes", "snapshots"},
	}, func(ctx context.Context, in *listVolumeSnapshotsInput) (*listVolumeSnapshotsOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		rows, _, cerr := live.ListVolumeSnapshots(ctx, in.UUID, in.Project, wclient.ListOpts{Limit: 1000})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		out := &listVolumeSnapshotsOutput{}
		for _, r := range rows {
			out.Body = append(out.Body, volumeSnapshotRowFromMap(r))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-volume-snapshot",
		Method:        "POST",
		Path:          "/api/volumes/{uuid}/snapshots",
		Summary:       "Snapshot a volume",
		Description:   "Freezes the volume's current state under the given name. File-backed parents do a reflink CoW clone ; block-backed parents create a controller-side snapshot through weft-block. The snapshot name must be unique within the parent volume.",
		Tags:          []string{"volumes", "snapshots"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createVolumeSnapshotInput) (*volumeSnapshotOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Body.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		snap, cerr := live.CreateVolumeSnapshot(ctx, in.UUID, in.Body.Name, in.Project)
		Audit(ctx, auditLogger, "volume.snapshot.create", "volume_snapshot", in.Body.Name, "", cerr, map[string]string{
			"volume_uuid": in.UUID,
			"project":     in.Project,
		})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "volume.snapshot.create")
		out := &volumeSnapshotOutput{}
		if snap != nil {
			out.Body = volumeSnapshotRowFromTyped(*snap)
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "restore-volume-snapshot",
		Method:        "POST",
		Path:          "/api/snapshots/{uuid}/restore",
		Summary:       "Restore a snapshot into a new volume",
		Description:   "Clones the snapshot's contents into a fresh volume. The new volume lands in the same project as the snapshot — there's no cross-project restore.",
		Tags:          []string{"snapshots"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *restoreVolumeSnapshotInput) (*struct{ Body RestoreVolumeSnapshotResp }, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Body.NewVolumeName == "" {
			return nil, huma.Error400BadRequest("new_volume_name is required")
		}
		err := live.RestoreVolumeSnapshot(ctx, in.UUID, in.Body.NewVolumeName, in.Body.Project)
		Audit(ctx, auditLogger, "volume.snapshot.restore", "volume_snapshot", in.UUID, "", err, map[string]string{
			"new_volume_name": in.Body.NewVolumeName,
			"project":         in.Body.Project,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.snapshot.restore")
		return &struct{ Body RestoreVolumeSnapshotResp }{Body: RestoreVolumeSnapshotResp{
			SnapshotUUID:  in.UUID,
			NewVolumeName: in.Body.NewVolumeName,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "revert-volume-snapshot",
		Method:        "POST",
		Path:          "/api/snapshots/{uuid}/revert",
		Summary:       "Revert the parent volume to this snapshot (block-backend only)",
		Description:   "Rolls the parent volume's contents back to the snapshot's state. Only supported on block-backend volumes (file parents reject with 502 — the agent surfaces a clear 'block-only' error message). The volume should be detached first ; the agent enforces this at the driver layer.",
		Tags:          []string{"snapshots"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		err := live.RevertVolumeSnapshot(ctx, in.UUID)
		Audit(ctx, auditLogger, "volume.snapshot.revert", "volume_snapshot", in.UUID, "", err, nil)
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.snapshot.revert")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-volume-snapshot",
		Method:        "DELETE",
		Path:          "/api/snapshots/{uuid}",
		Summary:       "Delete a snapshot (does not touch the parent volume)",
		Tags:          []string{"snapshots"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		err := live.DeleteVolumeSnapshot(ctx, in.UUID)
		Audit(ctx, auditLogger, "volume.snapshot.delete", "volume_snapshot", in.UUID, "", err, nil)
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.snapshot.delete")
		return nil, nil
	})
}

// ---- input / output types -----------------------------------------

type listVolumeSnapshotsInput struct {
	UUID    string `path:"uuid" doc:"Parent volume UUID" minLength:"1" maxLength:"64"`
	Project string `query:"project" doc:"Override the session project"`
}

type createVolumeSnapshotInput struct {
	UUID    string `path:"uuid" doc:"Parent volume UUID" minLength:"1" maxLength:"64"`
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Name string `json:"name" doc:"Snapshot name (unique within the parent volume)" minLength:"1" maxLength:"128"`
	}
}

type restoreVolumeSnapshotInput struct {
	UUID string `path:"uuid" doc:"Source snapshot UUID" minLength:"1" maxLength:"64"`
	Body struct {
		NewVolumeName string `json:"new_volume_name" doc:"Name for the restored volume" minLength:"1" maxLength:"128"`
		Project       string `json:"project,omitempty" doc:"Override the session project"`
	}
}

// VolumeSnapshotRow is the dashboard projection of one snapshot row.
// Mirrors the wclient.VolumeSnapshotInfo but with date-stringified
// timestamps so the SPA renders without per-cell formatting.
type VolumeSnapshotRow struct {
	UUID       string `json:"uuid"`
	VolumeUUID string `json:"volume_uuid"`
	Name       string `json:"name"`
	SizeGiB    int64  `json:"size_gib"`
	Project    string `json:"project"`
	Created    string `json:"created"`
}

// RestoreVolumeSnapshotResp echoes the request — the SPA already knows
// the project, and the new volume's UUID is discoverable through the
// listing right after the restore.
type RestoreVolumeSnapshotResp struct {
	SnapshotUUID  string `json:"snapshot_uuid"`
	NewVolumeName string `json:"new_volume_name"`
}

type listVolumeSnapshotsOutput struct{ Body []VolumeSnapshotRow }
type volumeSnapshotOutput struct{ Body VolumeSnapshotRow }

func volumeSnapshotRowFromMap(m map[string]any) VolumeSnapshotRow {
	return VolumeSnapshotRow{
		UUID:       asString(m["uuid"]),
		VolumeUUID: asString(m["volume_uuid"]),
		Name:       asString(m["name"]),
		SizeGiB:    asInt64(m["size_gib"]),
		Project:    asString(m["project"]),
		Created:    asString(m["created"]),
	}
}

func volumeSnapshotRowFromTyped(s wclient.VolumeSnapshotInfo) VolumeSnapshotRow {
	return VolumeSnapshotRow{
		UUID:       s.UUID,
		VolumeUUID: s.VolumeUUID,
		Name:       s.Name,
		SizeGiB:    s.SizeGiB,
		Project:    s.Project,
		Created:    s.CreatedAt,
	}
}

// asString / asInt64 narrow a map[string]any value to its expected
// scalar type. Defensive against the wclient projecting differently in
// the future ; the dashboard never wants to render a `<nil>`.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
