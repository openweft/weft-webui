package server

// api_volume_backups.go — HTTP/huma surface for off-host volume backups.
//
// Four routes, all live-only. Backups are a property of the operator's
// target store (oci/s3/sftp/fs), not of the webui's in-memory state —
// listing without a live daemon would only serve stale or fabricated
// rows. requireLiveCtx returns 503 when no daemon is wired ; the SPA
// renders "no live weft daemon" instead.
//
// Only block-backend volumes can be backed up — the daemon enforces
// that and surfaces a clear "block-only" error when a file parent
// sneaks in. The webui doesn't pre-check ; defence-in-depth at the
// agent is the single source of truth.
//
//	POST   /api/backups                  create-volume-backup   (from a snapshot)
//	GET    /api/backups                  list-volume-backups    (?target=URL)
//	DELETE /api/backups                  delete-volume-backup   (?url=URL — the addressing key is opaque to the SPA)
//	POST   /api/backups/restore          restore-volume-backup  (creates a new block volume from the backup)
//
// Targets accepted (validated server-side by the agent + weft-block) :
//   - oci://<registry>/<repo>:<tag>       — recommended ; content-addressed
//   - s3://<bucket>@<region>/<prefix>     — versitygw / CubeFS objectnode
//   - sftp://<user>@<host>:<port>/<path>  — sftpgo
//   - fs:///<absolute_path>               — dev / tests
//
// Encryption + incremental-chain bookkeeping live entirely inside
// weft-block (passphrase env-only on the daemon). The webui never
// touches a passphrase ; the operator sets WEFT_BACKUP_PASSPHRASE on
// the daemon and forgets about it from the dashboard's perspective.

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/wclient"
)

func mountVolumeBackupsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-volume-backup",
		Method:        "POST",
		Path:          "/api/backups",
		Summary:       "Ship a snapshot to a backup target",
		Description:   "Streams the snapshot's bytes to the target URL through weft-block. Encryption + incremental chains are honoured by the daemon when configured. Block-backend volumes only — file parents reject server-side with a clear error.",
		Tags:          []string{"backups"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createVolumeBackupInput) (*volumeBackupOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Body.SnapshotUUID == "" || in.Body.Target == "" {
			return nil, huma.Error400BadRequest("snapshot_uuid and target are required")
		}
		info, cerr := live.CreateVolumeBackup(ctx, in.Body.SnapshotUUID, in.Body.Target, in.Body.Project)
		Audit(ctx, auditLogger, "volume.backup.create", "volume_backup", in.Body.SnapshotUUID, "", cerr, map[string]string{
			"target":  in.Body.Target,
			"project": in.Body.Project,
		})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "volume.backup.create")
		out := &volumeBackupOutput{}
		if info != nil {
			out.Body = volumeBackupRowFromTyped(*info)
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-volume-backups",
		Method:      "GET",
		Path:        "/api/backups",
		Summary:     "List backups at a target store",
		Description: "Walks the target's metadata sidecars and returns one row per backup. Filtered server-side by project (via the caller's visible projects). Optional ?volume_uuid= further narrows to one origin volume.",
		Tags:        []string{"backups"},
	}, func(ctx context.Context, in *listVolumeBackupsInput) (*listVolumeBackupsOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Target == "" {
			return nil, huma.Error400BadRequest("target is required")
		}
		rows, _, cerr := live.ListVolumeBackups(ctx, in.Target, in.VolumeUUID, in.Project, wclient.ListOpts{Limit: 1000})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		out := &listVolumeBackupsOutput{}
		for _, r := range rows {
			out.Body = append(out.Body, volumeBackupRowFromMap(r))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-volume-backup",
		Method:        "DELETE",
		Path:          "/api/backups",
		Summary:       "Delete one backup from its target store",
		Description:   "Idempotent — deleting a missing backup is a no-op. The URL is the same opaque addressing key returned by list-volume-backups ; the SPA passes it back verbatim.",
		Tags:          []string{"backups"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *backupURLInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.URL == "" {
			return nil, huma.Error400BadRequest("url is required")
		}
		err := live.DeleteVolumeBackup(ctx, in.URL)
		Audit(ctx, auditLogger, "volume.backup.delete", "volume_backup", in.URL, "", err, nil)
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.backup.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "restore-volume-backup",
		Method:        "POST",
		Path:          "/api/backups/restore",
		Summary:       "Restore a backup into a new block volume",
		Description:   "Creates a fresh block-backend volume in the requested project and populates it from the backup. Size is discovered from the backup's sidecar metadata — the operator doesn't have to specify it. The source backup is untouched.",
		Tags:          []string{"backups"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *restoreVolumeBackupInput) (*struct{ Body RestoreVolumeBackupResp }, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Body.URL == "" || in.Body.NewVolumeName == "" || in.Body.Project == "" {
			return nil, huma.Error400BadRequest("url, new_volume_name and project are required")
		}
		err := live.RestoreVolumeBackup(ctx, in.Body.URL, in.Body.NewVolumeName, in.Body.Project)
		Audit(ctx, auditLogger, "volume.backup.restore", "volume_backup", in.Body.URL, "", err, map[string]string{
			"new_volume_name": in.Body.NewVolumeName,
			"project":         in.Body.Project,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.backup.restore")
		return &struct{ Body RestoreVolumeBackupResp }{Body: RestoreVolumeBackupResp{
			URL:           in.Body.URL,
			NewVolumeName: in.Body.NewVolumeName,
			Project:       in.Body.Project,
		}}, nil
	})
}

// ---- input / output types -----------------------------------------

type createVolumeBackupInput struct {
	Body struct {
		SnapshotUUID string `json:"snapshot_uuid" doc:"Source snapshot UUID (must already exist on a block-backend volume)" minLength:"1" maxLength:"64"`
		Target       string `json:"target"        doc:"Backup target URL (oci:// / s3:// / sftp:// / fs://)" minLength:"6" maxLength:"1024"`
		Project      string `json:"project,omitempty" doc:"Override the session project"`
	}
}

type listVolumeBackupsInput struct {
	Target     string `query:"target"      doc:"Backup target URL" minLength:"6" maxLength:"1024"`
	VolumeUUID string `query:"volume_uuid" doc:"Limit to one origin volume (UUID)"`
	Project    string `query:"project"     doc:"Override the session project"`
}

type backupURLInput struct {
	URL string `query:"url" doc:"Backup URL (as returned by list-volume-backups)" minLength:"6" maxLength:"2048"`
}

type restoreVolumeBackupInput struct {
	Body struct {
		URL           string `json:"url"             doc:"Backup URL to restore" minLength:"6" maxLength:"2048"`
		NewVolumeName string `json:"new_volume_name" doc:"Name for the restored block volume" minLength:"1" maxLength:"128"`
		Project       string `json:"project"         doc:"Project the new volume lands in" minLength:"1" maxLength:"128"`
	}
}

// VolumeBackupRow is the dashboard projection of one backup row.
// SizeBytes is on-target size (post-compression / encryption — the
// SPA renders it as "encrypted/ciphertext size" not plaintext).
// State is one of "in-progress" | "complete" | "error".
type VolumeBackupRow struct {
	URL          string `json:"url"`
	VolumeUUID   string `json:"volume_uuid"`
	SnapshotUUID string `json:"snapshot_uuid"`
	Project      string `json:"project"`
	SizeBytes    int64  `json:"size_bytes"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
	Created      string `json:"created"`
}

// RestoreVolumeBackupResp echoes the request — the new volume's UUID
// is discoverable through the volume listing right after the restore.
type RestoreVolumeBackupResp struct {
	URL           string `json:"url"`
	NewVolumeName string `json:"new_volume_name"`
	Project       string `json:"project"`
}

type listVolumeBackupsOutput struct{ Body []VolumeBackupRow }
type volumeBackupOutput struct{ Body VolumeBackupRow }

func volumeBackupRowFromMap(m map[string]any) VolumeBackupRow {
	return VolumeBackupRow{
		URL:          asString(m["url"]),
		VolumeUUID:   asString(m["volume_uuid"]),
		SnapshotUUID: asString(m["snapshot_uuid"]),
		Project:      asString(m["project"]),
		SizeBytes:    asInt64(m["size_bytes"]),
		State:        asString(m["state"]),
		Error:        asString(m["error"]),
		Created:      asString(m["created"]),
	}
}

func volumeBackupRowFromTyped(b wclient.VolumeBackupInfo) VolumeBackupRow {
	return VolumeBackupRow{
		URL:          b.URL,
		VolumeUUID:   b.VolumeUUID,
		SnapshotUUID: b.SnapshotUUID,
		Project:      b.Project,
		SizeBytes:    b.SizeBytes,
		State:        b.State,
		Error:        b.Error,
		Created:      b.CreatedAt,
	}
}
