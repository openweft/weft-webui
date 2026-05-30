// volumes.go — per-volume mutable metadata + property bag.
//
// The volume "row" in resources.go is the static, daemon-owned view
// (name / size / format / attachment / project / created). This file
// adds the operator-editable layer :
//
//   * VolumeMetadata — free-form `description`, suggested guest-side
//     `mount_point`, and `filesystem` hint (the FS the host will
//     mkfs the disk to when the guest claims it). Same shape across
//     every volume.
//   * VolumeProperty — k/v annotations the orchestration layer reads
//     to make placement / lifecycle decisions (e.g. workload=database,
//     backup-policy=nightly). Mirrors the per-VM property pattern.
//
// Both stores are in-memory mocks ; live wiring would replace the
// seed maps with weft-agent RPCs (Unimplemented falls through to
// the mock so the dashboard stays useful through staged rollouts).
package server

import "sync"

// VolumeMetadata is the editable layer over the daemon-owned volume row.
type VolumeMetadata struct {
	Description string `json:"description" doc:"Operator-supplied prose ; surfaced in the dashboard and the volume drawer"`
	MountPoint  string `json:"mount_point" doc:"Guest-side mount path the agent honours when attaching (e.g. /mnt/data)"`
	Filesystem  string `json:"filesystem"  doc:"mkfs target when the guest claims a fresh volume" enum:",ext4,xfs,btrfs,ext3,zfs"`
	UpdatedAt   string `json:"updated_at"  doc:"RFC-3339 ; server-stamped" readOnly:"true"`
	UpdatedBy   string `json:"updated_by"  doc:"OIDC email of the last editor"      readOnly:"true"`
}

// VolumeProperty mirrors VMProperty : free-form k/v, server-stamped
// timestamp. No GuestReadable flag — volume properties are read by
// the orchestration layer (placement, backup engine, …), not by the
// guest filesystem itself.
type VolumeProperty struct {
	Key       string `json:"key"        doc:"Free-form annotation key" minLength:"1" maxLength:"128"`
	Value     string `json:"value"      doc:"Annotation value (opaque to this layer)"`
	UpdatedAt string `json:"updated_at" doc:"RFC-3339 ; server-stamped" readOnly:"true"`
}

var (
	volumeMetadataMu   sync.Mutex
	volumeMetadataByID = seedVolumeMetadata()

	volumePropsMu sync.Mutex
	volumeProps   = seedVolumeProperties()
)

func seedVolumeMetadata() map[string]VolumeMetadata {
	now := "2026-05-20T14:00:00Z"
	return map[string]VolumeMetadata{
		"pg-data": {
			Description: "PostgreSQL data directory ; backed up nightly to bucket pg-backups.",
			MountPoint:  "/var/lib/postgresql/data",
			Filesystem:  "ext4",
			UpdatedAt:   now, UpdatedBy: "alice@weft.local",
		},
		"cubefs-d0": {
			Description: "CubeFS data node 0 (replication factor 3).",
			MountPoint:  "/srv/cubefs/d0",
			Filesystem:  "xfs",
			UpdatedAt:   now, UpdatedBy: "bob@weft.local",
		},
	}
}

func seedVolumeProperties() map[string][]VolumeProperty {
	now := "2026-05-20T14:00:00Z"
	return map[string][]VolumeProperty{
		"pg-data": {
			{Key: "workload", Value: "database", UpdatedAt: now},
			{Key: "backup-policy", Value: "nightly", UpdatedAt: now},
			{Key: "iops-class", Value: "high", UpdatedAt: now},
		},
		"cubefs-d0": {
			{Key: "workload", Value: "object-store", UpdatedAt: now},
			{Key: "replication", Value: "rf3", UpdatedAt: now},
		},
	}
}

// ---- metadata accessors -----------------------------------------

func getVolumeMetadata(key string) VolumeMetadata {
	volumeMetadataMu.Lock()
	defer volumeMetadataMu.Unlock()
	return volumeMetadataByID[key]
}

func setVolumeMetadata(key string, m VolumeMetadata) {
	volumeMetadataMu.Lock()
	defer volumeMetadataMu.Unlock()
	volumeMetadataByID[key] = m
}

// ---- property accessors -----------------------------------------

func listVolumeProperties(key string) []VolumeProperty {
	volumePropsMu.Lock()
	defer volumePropsMu.Unlock()
	out := make([]VolumeProperty, len(volumeProps[key]))
	copy(out, volumeProps[key])
	return out
}

// setVolumeProperty inserts or updates by key. UpdatedAt must already
// be stamped by the caller (so the handler can use auth context for
// the email-as-updater pattern other resources follow).
func setVolumeProperty(volKey string, p VolumeProperty) {
	volumePropsMu.Lock()
	defer volumePropsMu.Unlock()
	existing := volumeProps[volKey]
	for i, e := range existing {
		if e.Key == p.Key {
			existing[i] = p
			volumeProps[volKey] = existing
			return
		}
	}
	volumeProps[volKey] = append(existing, p)
}

// renameVolumeRow updates the static volume row in resources.go's
// seed table — the mock side of `RenameVolume`. Returns false if no
// row matches the old name. Mock-only ; live wiring calls weft-agent
// and the next resources refresh shows the new name.
func renameVolumeRow(oldName, newName string) bool {
	res, ok := resourceByID["volumes"]
	if !ok {
		return false
	}
	for i, row := range res.Rows {
		if row["name"] == oldName {
			res.Rows[i]["name"] = newName
			return true
		}
	}
	return false
}

func deleteVolumeProperty(volKey, propKey string) bool {
	volumePropsMu.Lock()
	defer volumePropsMu.Unlock()
	existing := volumeProps[volKey]
	for i, e := range existing {
		if e.Key == propKey {
			volumeProps[volKey] = append(existing[:i], existing[i+1:]...)
			return true
		}
	}
	return false
}
