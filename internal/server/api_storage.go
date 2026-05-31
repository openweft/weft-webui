// api_storage.go — typed endpoints for the storage surface :
// volumes, shares, object buckets + their inline browser.
//
// Three sub-areas with subtly different gating :
//   * Volumes : live-only (no mock-mode mutation path).
//   * Shares  : tenant-admin gated, live-first → mem fallback.
//   * Buckets : mem-only today (mock S3 with embedded policy
//               evaluator) ; the policy + listing logic moves
//               whole to api_storage.go so the legacy file shrinks
//               to seeds + helpers.
//
// Multipart uploads go through huma.MultipartFormFiles so the
// OpenAPI declares 'multipart/form-data' instead of leaking the
// implementation. Same readUploads-from-MultipartForm path the
// legacy handlers used (just a different cursor type).

package server

import (
	"context"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/wclient"
)

// itoaSize is a tiny strconv wrapper for the audit extra map (which
// is string-keyed string-valued by design ; numeric extras stringify
// here).
func itoaSize(n int64) string { return strconv.FormatInt(n, 10) }

func mountStorageAPI(api huma.API) {
	mountVolumesAPI(api)
	mountSharesAPI(api)
	mountSharesStorageAPI(api)
	mountBucketsAPI(api)
}

// ---- Volumes -----------------------------------------------------

func mountVolumesAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-volume",
		Method:        "POST",
		Path:          "/api/volumes",
		Summary:       "Create a volume (live-only)",
		Tags:          []string{"volumes"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createVolumeInput) (*createVolumeOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		project, err := resolveVMProjectCtx(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		if in.Body.Name == "" || in.Body.SizeGiB <= 0 {
			return nil, huma.Error400BadRequest("name and a positive size_gib are required")
		}
		cerr := live.CreateVolume(ctx, project, in.Body.Name, in.Body.SizeGiB, in.Body.Format)
		Audit(ctx, auditLogger, "volume.create", "volume", in.Body.Name, "", cerr, map[string]string{
			"project":  project,
			"size_gib": itoaSize(in.Body.SizeGiB),
			"format":   in.Body.Format,
		})
		if cerr != nil {
			return nil, huma.Error502BadGateway("live: " + cerr.Error())
		}
		userActionCtx(ctx, "volume.create")
		return &createVolumeOutput{Body: CreateVolumeResp{
			Name: in.Body.Name, Project: project, SizeGiB: in.Body.SizeGiB,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-volume",
		Method:        "DELETE",
		Path:          "/api/volumes/{uuid}",
		Summary:       "Delete a volume (live-only)",
		Tags:          []string{"volumes"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.UUID == "" {
			return nil, huma.Error400BadRequest("uuid is required")
		}
		err := live.DeleteVolume(ctx, in.UUID)
		Audit(ctx, auditLogger, "volume.delete", "volume", in.UUID, "", err, nil)
		if err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.delete")
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "attach-volume",
		Method:      "POST",
		Path:        "/api/volumes/{uuid}/attach",
		Summary:     "Attach a volume to a VM (live-only)",
		Description: "weft-agent keys on VM UUID, not name ; the caller looks up the VM UUID from VMStatus or ListVMs before calling.",
		Tags:        []string{"volumes"},
	}, func(ctx context.Context, in *attachVolumeInput) (*attachVolumeOutput, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if in.Body.VMUUID == "" {
			return nil, huma.Error400BadRequest("vm_uuid is required")
		}
		if err := live.AttachVolume(ctx, in.UUID, in.Body.VMUUID); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.attach")
		return &attachVolumeOutput{Body: AttachVolumeResp{Volume: in.UUID, VM: in.Body.VMUUID}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "detach-volume",
		Method:        "POST",
		Path:          "/api/volumes/{uuid}/detach",
		Summary:       "Detach a volume from its current VM (live-only)",
		Tags:          []string{"volumes"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *uuidInput) (*struct{}, error) {
		if err := requireLiveCtx(); err != nil {
			return nil, err
		}
		if err := live.DetachVolume(ctx, in.UUID); err != nil {
			return nil, huma.Error502BadGateway("live: " + err.Error())
		}
		userActionCtx(ctx, "volume.detach")
		return nil, nil
	})
}

// ---- Shares (lifecycle) ------------------------------------------

func mountSharesAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-share",
		Method:        "POST",
		Path:          "/api/shares",
		Summary:       "Create a share (tenant admin)",
		Description:   "POSIX (RWX) face of CubeFS. Live-first via weft-agent ; falls back to the mock store on Unimplemented so the dashboard stays useful before the daemon catches up with CreateShare.",
		Tags:          []string{"shares"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createShareInput) (*createShareOutput, error) {
		project := in.Body.Project
		if project == "" {
			project = resolveProjectOrPlatform(ctx, "")
		}
		if project == "" {
			return nil, huma.Error400BadRequest("project is required (set scope via the topbar or pass project=...)")
		}
		tenant, ok := tenantsDB.projectTenant(project)
		if !ok {
			return nil, huma.Error400BadRequest("unknown project: " + project)
		}
		u := auth.UserFromContext(ctx)
		if !tenantsDB.isTenantAdmin(u, tenant) {
			return nil, huma.Error403Forbidden("tenant admin required")
		}
		if live != nil {
			uuid, err := live.CreateShare(ctx, project, in.Body.Name, in.Body.SizeGB, in.Body.ReadOnly, in.Body.Backend)
			if err == nil {
				userActionCtx(ctx, "share.create")
				return &createShareOutput{Body: CreateShareResp{
					Name: in.Body.Name, Project: project, UUID: uuid,
					SizeGB: in.Body.SizeGB, ReadOnly: in.Body.ReadOnly,
					Status: "provisioning",
				}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		sh := &Share{
			Name: in.Body.Name, Project: project, Backend: in.Body.Backend,
			SizeGB: in.Body.SizeGB, ReadOnly: in.Body.ReadOnly,
		}
		if err := sharesDB.create(sh); err != nil {
			return nil, hideHTTPErr(err)
		}
		userActionCtx(ctx, "share.create")
		return &createShareOutput{Body: CreateShareResp{
			Name: sh.Name, Project: sh.Project,
			SizeGB: sh.SizeGB, ReadOnly: sh.ReadOnly,
			Status: "provisioning",
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "resize-share",
		Method:        "PUT",
		Path:          "/api/shares/{name}",
		Summary:       "Resize a share / toggle read-only (tenant admin)",
		Description:   "Grows capacity ; shrinking is not supported (returns 400). The CubeFS volume owns physical capacity — this updates the metadata that drives mount-time enforcement. ReadOnly toggles re-fan to mounting VMs on the next reconcile.",
		Tags:          []string{"shares"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *resizeShareInput) (*resizeShareOutput, error) {
		project, ok := sharesDB.shareProject(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("share not found")
		}
		tenant, ok := tenantsDB.projectTenant(project)
		if !ok {
			return nil, huma.Error404NotFound("project not found")
		}
		u := auth.UserFromContext(ctx)
		if !tenantsDB.isTenantAdmin(u, tenant) {
			return nil, huma.Error403Forbidden("tenant admin required")
		}
		if err := sharesDB.resize(in.Name, in.Body.SizeGB, in.Body.ReadOnly); err != nil {
			return nil, hideHTTPErr(err)
		}
		userActionCtx(ctx, "share.resize")
		return &resizeShareOutput{Body: ResizeShareResp{
			Name: in.Name, SizeGB: in.Body.SizeGB, ReadOnly: in.Body.ReadOnly,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-share",
		Method:        "DELETE",
		Path:          "/api/shares/{name}",
		Summary:       "Delete a share (tenant admin)",
		Tags:          []string{"shares"},
		DefaultStatus: 204,
	}, func(ctx context.Context, in *shareNameInput) (*struct{}, error) {
		project, ok := sharesDB.shareProject(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("share not found")
		}
		tenant, ok := tenantsDB.projectTenant(project)
		if !ok {
			return nil, huma.Error404NotFound("project not found")
		}
		u := auth.UserFromContext(ctx)
		if !tenantsDB.isTenantAdmin(u, tenant) {
			return nil, huma.Error403Forbidden("tenant admin required")
		}
		if err := sharesDB.delete(in.Name); err != nil {
			return nil, hideHTTPErr(err)
		}
		userActionCtx(ctx, "share.delete")
		return nil, nil
	})
}

// ---- Share storage (browse + upload) -----------------------------

func mountSharesStorageAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-share-objects",
		Method:      "GET",
		Path:        "/api/shares/{name}/objects",
		Summary:     "List the share's objects under ?prefix",
		Tags:        []string{"shares"},
	}, func(_ context.Context, in *listShareObjectsInput) (*objectListingOutput, error) {
		sharesMu.Lock()
		defer sharesMu.Unlock()
		objs, ok := shareFiles[in.Name]
		if !ok {
			return nil, huma.Error404NotFound("no such share")
		}
		folders, entries := listEntries(objs, in.Prefix)
		return &objectListingOutput{Body: ObjectListing{
			Share: in.Name, Prefix: in.Prefix, Folders: folders, Objects: entries,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-share-object",
		Method:      "GET",
		Path:        "/api/shares/{name}/object",
		Summary:     "Get one share object's metadata + preview",
		Tags:        []string{"shares"},
	}, func(_ context.Context, in *getShareObjectInput) (*objectDetailOutput, error) {
		sharesMu.Lock()
		defer sharesMu.Unlock()
		objs, ok := shareFiles[in.Name]
		if !ok {
			return nil, huma.Error404NotFound("no such share")
		}
		if d, ok := objectDetail(objs, in.Key); ok {
			return &objectDetailOutput{Body: *d}, nil
		}
		return nil, huma.Error404NotFound("no such object")
	})

	huma.Register(api, huma.Operation{
		OperationID:   "upload-share-objects",
		Method:        "POST",
		Path:          "/api/shares/{name}/objects",
		Summary:       "Upload one or more objects into a share",
		Tags:          []string{"shares"},
		DefaultStatus: 201,
	}, func(_ context.Context, in *uploadShareObjectsInput) (*passthroughOutput, error) {
		data := in.RawBody.Data()
		prefix := strings.TrimSpace(data.Prefix)
		sharesMu.Lock()
		defer sharesMu.Unlock()
		objs, ok := shareFiles[in.Name]
		if !ok {
			return nil, huma.Error404NotFound("no such share")
		}
		if len(data.File) == 0 {
			return nil, huma.Error400BadRequest("no files")
		}
		uploaded := readUploadsHuma(data.File, prefix)
		shareFiles[in.Name] = append(objs, uploaded...)
		return &passthroughOutput{Body: map[string]any{"share": in.Name, "added": len(uploaded)}}, nil
	})
}

// ---- Buckets (object storage) ------------------------------------

func mountBucketsAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-bucket",
		Method:        "POST",
		Path:          "/api/buckets",
		Summary:       "Create an object-storage bucket",
		Tags:          []string{"buckets"},
		DefaultStatus: 201,
	}, func(_ context.Context, in *createBucketInput) (*bucketNameOutput, error) {
		name := strings.TrimSpace(in.Body.Name)
		if !bucketName.MatchString(name) {
			return nil, huma.Error400BadRequest("bucket name must be 3–63 chars, lowercase letters/digits/hyphens")
		}
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		if findBucket(name) != nil {
			return nil, huma.Error409Conflict("bucket already exists")
		}
		buckets = append(buckets, &bucket{Name: name, Created: time.Now().UTC().Format("2006-01-02")})
		return &bucketNameOutput{Body: BucketNameResp{Name: name}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-bucket",
		Method:      "DELETE",
		Path:        "/api/buckets/{name}",
		Summary:     "Delete a bucket (cascades the attached policy)",
		Tags:        []string{"buckets"},
	}, func(_ context.Context, in *bucketNameInput) (*deletedNameOutput, error) {
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		for i, b := range buckets {
			if b.Name == in.Name {
				buckets = append(buckets[:i], buckets[i+1:]...)
				delete(policies, in.Name)
				return &deletedNameOutput{Body: DeletedNameResp{Deleted: in.Name}}, nil
			}
		}
		return nil, huma.Error404NotFound("no such bucket")
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-bucket-policy",
		Method:      "GET",
		Path:        "/api/buckets/{name}/policy",
		Summary:     "Get a bucket's IAM policy (empty {Statements:[]} when none)",
		Tags:        []string{"buckets"},
	}, func(_ context.Context, in *bucketNameInput) (*bucketPolicyOutput, error) {
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		if findBucket(in.Name) == nil {
			return nil, huma.Error404NotFound("no such bucket")
		}
		if p, ok := policies[in.Name]; ok && p != nil {
			return &bucketPolicyOutput{Body: *p}, nil
		}
		return &bucketPolicyOutput{Body: BucketPolicy{Version: "2012-10-17", Statements: []PolicyStatement{}}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-bucket-policy",
		Method:      "PUT",
		Path:        "/api/buckets/{name}/policy",
		Summary:     "Atomically set a bucket's policy",
		Description: "An empty statement list clears the policy back to the default-allow state. One bad statement rejects the whole submission so the editor and server don't go out of sync.",
		Tags:        []string{"buckets"},
	}, func(_ context.Context, in *setBucketPolicyInput) (*bucketPolicyOutput, error) {
		body := in.Body
		for i, s := range body.Statements {
			if !validPolicyEffects[s.Effect] {
				return nil, huma.Error400BadRequest("statement " + itoaSafe(i) + ": invalid effect " + s.Effect)
			}
			if !validPolicyActions[s.Action] {
				return nil, huma.Error400BadRequest("statement " + itoaSafe(i) + ": invalid action " + s.Action)
			}
			if strings.TrimSpace(s.Principal) == "" || strings.TrimSpace(s.Resource) == "" {
				return nil, huma.Error400BadRequest("statement " + itoaSafe(i) + ": principal and resource are required")
			}
		}
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		if findBucket(in.Name) == nil {
			return nil, huma.Error404NotFound("no such bucket")
		}
		if len(body.Statements) == 0 {
			delete(policies, in.Name)
			return &bucketPolicyOutput{Body: BucketPolicy{Version: "2012-10-17", Statements: []PolicyStatement{}}}, nil
		}
		if body.Version == "" {
			body.Version = "2012-10-17"
		}
		policies[in.Name] = &body
		return &bucketPolicyOutput{Body: body}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-bucket-objects",
		Method:      "GET",
		Path:        "/api/buckets/{name}/objects",
		Summary:     "List bucket objects (s3:ListBucket gate)",
		Tags:        []string{"buckets"},
	}, func(ctx context.Context, in *listShareObjectsInput) (*objectListingOutput, error) {
		if err := requirePolicyCtx(ctx, in.Name, "s3:ListBucket", ""); err != nil {
			return nil, err
		}
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		b := findBucket(in.Name)
		if b == nil {
			return nil, huma.Error404NotFound("no such bucket")
		}
		folders, objects := listEntries(b.Objects, in.Prefix)
		return &objectListingOutput{Body: ObjectListing{
			Bucket: b.Name, Prefix: in.Prefix, Folders: folders, Objects: objects,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-bucket-object",
		Method:      "GET",
		Path:        "/api/buckets/{name}/object",
		Summary:     "Get one bucket object (s3:GetObject gate)",
		Tags:        []string{"buckets"},
	}, func(ctx context.Context, in *getShareObjectInput) (*objectDetailOutput, error) {
		if err := requirePolicyCtx(ctx, in.Name, "s3:GetObject", in.Key); err != nil {
			return nil, err
		}
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		b := findBucket(in.Name)
		if b == nil {
			return nil, huma.Error404NotFound("no such bucket")
		}
		if d, ok := objectDetail(b.Objects, in.Key); ok {
			return &objectDetailOutput{Body: *d}, nil
		}
		return nil, huma.Error404NotFound("no such object")
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-bucket-object",
		Method:      "DELETE",
		Path:        "/api/buckets/{name}/object",
		Summary:     "Delete one bucket object (s3:DeleteObject gate)",
		Tags:        []string{"buckets"},
	}, func(ctx context.Context, in *getShareObjectInput) (*passthroughOutput, error) {
		if in.Key == "" {
			return nil, huma.Error400BadRequest("missing ?key")
		}
		if err := requirePolicyCtx(ctx, in.Name, "s3:DeleteObject", in.Key); err != nil {
			return nil, err
		}
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		b := findBucket(in.Name)
		if b == nil {
			return nil, huma.Error404NotFound("no such bucket")
		}
		for i, o := range b.Objects {
			if o.Key == in.Key {
				b.Objects = append(b.Objects[:i], b.Objects[i+1:]...)
				return &passthroughOutput{Body: map[string]any{"deleted": in.Key}}, nil
			}
		}
		return nil, huma.Error404NotFound("no such object")
	})

	huma.Register(api, huma.Operation{
		OperationID: "upload-bucket-objects",
		Method:      "POST",
		Path:        "/api/buckets/{name}/objects",
		Summary:     "Upload one or more bucket objects (s3:PutObject gate on the destination prefix)",
		Tags:        []string{"buckets"},
	}, func(ctx context.Context, in *uploadShareObjectsInput) (*passthroughOutput, error) {
		data := in.RawBody.Data()
		prefix := strings.TrimSpace(data.Prefix)
		if err := requirePolicyCtx(ctx, in.Name, "s3:PutObject", prefix); err != nil {
			return nil, err
		}
		bucketsMu.Lock()
		defer bucketsMu.Unlock()
		b := findBucket(in.Name)
		if b == nil {
			return nil, huma.Error404NotFound("no such bucket")
		}
		if len(data.File) == 0 {
			return nil, huma.Error400BadRequest("no files")
		}
		uploaded := readUploadsHuma(data.File, prefix)
		b.Objects = append(b.Objects, uploaded...)
		return &passthroughOutput{Body: map[string]any{"bucket": b.Name, "added": len(uploaded)}}, nil
	})
}

// requirePolicyCtx is the huma analogue of requirePolicy : evaluate
// the policy, return a 403 huma error on deny, nil on allow.
func requirePolicyCtx(ctx context.Context, bucket, action, key string) error {
	d := evaluatePolicy(ctx, bucket, action, key)
	if d.allow {
		return nil
	}
	return huma.Error403Forbidden(d.reason)
}

// readUploadsHuma is the huma-side analog of readUploads from
// objectstorage.go : translate huma.FormFile entries into s3object
// rows, with a 256 KiB preview cap for text/* content.
func readUploadsHuma(files []huma.FormFile, prefix string) []s3object {
	out := make([]s3object, 0, len(files))
	for _, f := range files {
		key := prefix + f.Filename
		ct := f.ContentType
		if ct == "" {
			ct = guessType(key)
		}
		content := ""
		if previewable(ct) && f.Size <= 256<<10 {
			if data, err := io.ReadAll(io.LimitReader(f.File, 256<<10)); err == nil {
				content = string(data)
			}
		}
		out = append(out, s3object{
			Key: key, Size: f.Size, Modified: time.Now().UTC().Format("2006-01-02"),
			ContentType: ct, Content: content,
		})
	}
	return out
}

// ---- inputs ------------------------------------------------------

type createVolumeInput struct {
	Project string `query:"project" doc:"Override the session project"`
	Body    struct {
		Name    string `json:"name" minLength:"1" maxLength:"128"`
		Format  string `json:"format,omitempty"`
		SizeGiB int64  `json:"size_gib" minimum:"1"`
	}
}

type attachVolumeInput struct {
	UUID string `path:"uuid" minLength:"1"`
	Body struct {
		VMUUID string `json:"vm_uuid" minLength:"1"`
	}
}

type createShareInput struct {
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"128"`
		Project  string `json:"project,omitempty"`
		Backend  string `json:"backend,omitempty"`
		SizeGB   int64  `json:"size_gb,omitempty" minimum:"0"`
		ReadOnly bool   `json:"read_only,omitempty"`
	}
}

type shareNameInput struct {
	Name string `path:"name" doc:"Share name" minLength:"1" maxLength:"128"`
}

type resizeShareInput struct {
	Name string `path:"name" doc:"Share name" minLength:"1" maxLength:"128"`
	Body struct {
		SizeGB   int64 `json:"size_gb" doc:"New size in GiB (must be >= current ; shrinking is rejected)" minimum:"1"`
		ReadOnly bool  `json:"read_only,omitempty" doc:"Re-fans to mounting VMs on the next reconcile"`
	}
}

// ResizeShareResp is the typed response body for the resize endpoint.
type ResizeShareResp struct {
	Name     string `json:"name"`
	SizeGB   int64  `json:"size_gb"`
	ReadOnly bool   `json:"read_only"`
}

type resizeShareOutput struct {
	Body ResizeShareResp
}

type listShareObjectsInput struct {
	Name   string `path:"name" doc:"Share or bucket name" minLength:"1" maxLength:"128"`
	Prefix string `query:"prefix" doc:"Filter to entries under this prefix"`
}

type getShareObjectInput struct {
	Name string `path:"name" doc:"Share or bucket name" minLength:"1" maxLength:"128"`
	Key  string `query:"key" doc:"Object key (full path within the bucket)"`
}

type uploadShareObjectsInput struct {
	Name    string `path:"name" doc:"Share or bucket name" minLength:"1" maxLength:"128"`
	RawBody huma.MultipartFormFiles[struct {
		File   []huma.FormFile `form:"file" required:"true"`
		Prefix string          `form:"prefix"`
	}]
}

type createBucketInput struct {
	Body struct {
		Name string `json:"name" doc:"Bucket name (3–63 chars, lowercase letters / digits / hyphens)" minLength:"3" maxLength:"63"`
	}
}

type bucketNameInput struct {
	Name string `path:"name" doc:"Bucket name" minLength:"3" maxLength:"63"`
}

type setBucketPolicyInput struct {
	Name string `path:"name" doc:"Bucket name" minLength:"3" maxLength:"63"`
	Body BucketPolicy
}

// ---- typed create-response shapes --------------------------------

// CreateVolumeResp echoes the parameters the volume was minted with.
// The agent's actual record (with UUID, status, …) shows up via
// the volumes listing after creation.
type CreateVolumeResp struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	SizeGiB int64  `json:"size_gib"`
}

// AttachVolumeResp acknowledges a volume-to-VM attach. Both fields
// are UUIDs ; the caller already knows which VM it asked for, but
// echoing keeps the response self-describing for log dumps.
type AttachVolumeResp struct {
	Volume string `json:"volume"`
	VM     string `json:"vm"`
}

// CreateShareResp covers both the live + mem fallback. UUID is empty
// on the fallback path ; Status is always 'provisioning' for create.
type CreateShareResp struct {
	Name     string `json:"name"`
	Project  string `json:"project"`
	UUID     string `json:"uuid,omitempty"`
	SizeGB   int64  `json:"size_gb"`
	ReadOnly bool   `json:"read_only"`
	Status   string `json:"status"`
}

type createVolumeOutput struct{ Body CreateVolumeResp }
type attachVolumeOutput struct{ Body AttachVolumeResp }
type createShareOutput  struct{ Body CreateShareResp }

// BucketNameResp is the create-bucket ack — just the new name.
type BucketNameResp struct {
	Name string `json:"name"`
}
type bucketNameOutput struct{ Body BucketNameResp }

// DeletedNameResp is what delete-bucket returns ; reused anywhere we
// want to echo a "you just deleted X" payload to the SPA.
type DeletedNameResp struct {
	Deleted string `json:"deleted"`
}
type deletedNameOutput struct{ Body DeletedNameResp }

// objectListingOutput / objectDetailOutput / bucketPolicyOutput
// surface the typed shapes from objectstorage.go in the OpenAPI.
// One listing/detail/policy struct serves both buckets and shares
// (the Go side differentiates with the optional Bucket / Share
// fields ; the SPA already keys on the URL).
type objectListingOutput struct{ Body ObjectListing }
type objectDetailOutput  struct{ Body ObjectDetail }
type bucketPolicyOutput  struct{ Body BucketPolicy }
