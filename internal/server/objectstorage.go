package server

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
)

// Object Storage — the S3 face of CubeFS. Buckets hold objects keyed with
// "/" delimiters, browsed by prefix like the AWS / MinIO consoles. Seeded
// with mock data ; wiring to CubeFS's S3 endpoint means swapping the store
// for an S3 client — the HTTP shapes here stay the same.

type s3object struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	Modified    string `json:"modified"`
	ContentType string `json:"contentType"`
	Content     string `json:"-"` // text preview ; empty for binary
}

type bucket struct {
	Name    string
	Created string
	Objects []s3object
}

var (
	bucketsMu sync.Mutex
	buckets   = seedBuckets()
	// policies — per-bucket access policy, keyed by bucket name. Same
	// mutex as `buckets` since they're touched together (delete cascade,
	// PUT after a CreateBucket reload, etc.). Nil entry = no policy ;
	// the SPA reads that as "everyone in the project can read/write".
	policies = seedPolicies()
)

// BucketPolicy mirrors the trimmed-down S3 policy shape this dashboard
// presents : a flat list of statements, one principal + one action +
// one resource each. No conditions, no wildcards in principals beyond
// "*". The Version field follows the AWS convention so the JSON looks
// familiar to anyone copying a snippet in/out of a real S3 console.
type BucketPolicy struct {
	Version    string            `json:"version"`
	Statements []PolicyStatement `json:"statements"`
}

// PolicyStatement is the SPA-presented row : Allow/Deny one principal
// to do one action against one resource pattern. The action vocabulary
// is restricted to the four verbs that have first-class affordances in
// the FileBrowser : GetObject, PutObject, DeleteObject, ListBucket.
type PolicyStatement struct {
	Effect    string `json:"effect"`    // "Allow" | "Deny"
	Principal string `json:"principal"` // OIDC sub OR "*"
	Action    string `json:"action"`    // "s3:GetObject" | "s3:PutObject" | "s3:DeleteObject" | "s3:ListBucket"
	Resource  string `json:"resource"`  // "*" | "prefix/*" | exact key
}

// seedPolicies — start with one demonstrative policy on team-data so
// the SPA editor isn't empty on first open. Mirrors the kind of rule
// an admin writes after onboarding a team : "read for everyone in the
// tenant, write for the bucket's owners".
func seedPolicies() map[string]*BucketPolicy {
	return map[string]*BucketPolicy{
		"team-data": {
			Version: "2012-10-17",
			Statements: []PolicyStatement{
				{Effect: "Allow", Principal: "*", Action: "s3:GetObject", Resource: "*"},
				{Effect: "Allow", Principal: "*", Action: "s3:ListBucket", Resource: "*"},
				{Effect: "Allow", Principal: "alice@acme.example", Action: "s3:PutObject", Resource: "datasets/*"},
				{Effect: "Allow", Principal: "alice@acme.example", Action: "s3:DeleteObject", Resource: "datasets/*"},
			},
		},
	}
}

// validPolicyEffects / Actions are the closed sets the SPA editor
// emits ; the handler refuses anything else so a bad client can't
// silently land a policy that bypasses the UI's affordances.
var (
	validPolicyEffects = map[string]bool{"Allow": true, "Deny": true}
	validPolicyActions = map[string]bool{
		"s3:GetObject": true, "s3:PutObject": true,
		"s3:DeleteObject": true, "s3:ListBucket": true,
	}
)

// policyDecision is the result of evaluating a bucket's policy against
// one request. "allow"/"deny" carry an optional human reason for the
// 403 body so the operator sees which rule fired.
type policyDecision struct {
	allow  bool
	reason string
}

// evaluatePolicy answers "may this principal perform action on this
// key in this bucket?". The semantics are deliberately permissive for
// the demo : no policy = allow ; otherwise an explicit Deny match
// wins, an explicit Allow match grants, and a no-match falls back to
// allow. AWS-style default-deny would be more correct, but lock-outs
// from a single Allow-only statement would be confusing here — wire
// strict mode behind a flag when the deployment needs it.
//
// key may be "" for bucket-level actions (s3:ListBucket).
//
// Cluster/tenant admins bypass policies — same shortcut Shares uses.
func evaluatePolicy(ctx context.Context, bucket, action, key string) policyDecision {
	u := auth.UserFromContext(ctx)
	if u != nil && (isClusterAdmin(u) || tenantsDB.isAnyTenantAdmin(u.Email)) {
		return policyDecision{allow: true}
	}
	bucketsMu.Lock()
	p := policies[bucket]
	bucketsMu.Unlock()
	if p == nil || len(p.Statements) == 0 {
		return policyDecision{allow: true}
	}
	principal := ""
	if u != nil {
		principal = u.Email
		if principal == "" {
			principal = u.Subject
		}
	}
	var allowed *PolicyStatement
	for i := range p.Statements {
		s := &p.Statements[i]
		if s.Action != action {
			continue
		}
		if !principalMatch(s.Principal, principal) {
			continue
		}
		if !resourceMatch(s.Resource, key) {
			continue
		}
		if s.Effect == "Deny" {
			return policyDecision{
				allow:  false,
				reason: "denied by policy : " + s.Principal + " " + s.Action + " " + s.Resource,
			}
		}
		if s.Effect == "Allow" && allowed == nil {
			allowed = s
		}
	}
	if allowed != nil {
		return policyDecision{allow: true}
	}
	return policyDecision{allow: true} // permissive default ; see comment
}

// principalMatch — "*" matches anyone (even unauthenticated callers),
// otherwise exact (case-insensitive) match on the caller's email.
func principalMatch(rule, caller string) bool {
	if rule == "*" {
		return true
	}
	return strings.EqualFold(rule, caller)
}

// resourceMatch supports three patterns : "*" (everything),
// "prefix/*" (prefix glob), and exact-key. Bucket-level actions
// (key == "") match "*" only.
func resourceMatch(rule, key string) bool {
	if rule == "*" {
		return true
	}
	if strings.HasSuffix(rule, "/*") {
		return strings.HasPrefix(key, strings.TrimSuffix(rule, "*"))
	}
	return rule == key
}

// requirePolicy is the request-side wrapper : evaluate, on deny write
// a 403 + reason and return false so the caller exits the handler.
func requirePolicy(w http.ResponseWriter, r *http.Request, bucket, action, key string) bool {
	d := evaluatePolicy(r.Context(), bucket, action, key)
	if d.allow {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": d.reason})
	return false
}

func seedBuckets() []*bucket {
	return []*bucket{
		{Name: "team-data", Created: "2026-03-02", Objects: []s3object{
			obj("README.md", "# team-data\n\nShared datasets and models for team-alpha.\nServed by CubeFS over its S3 endpoint.\n"),
			obj("datasets/2026/jan/report.csv", "date,region,requests,errors\n2026-01-01,dc-a,18422,3\n2026-01-02,dc-b,21044,0\n2026-01-03,dc-c,17310,5\n"),
			obj("datasets/2026/jan/notes.md", "# January\n\n- Backfilled the dc-c gap on the 3rd.\n- Errors correlate with the rack-2 drain.\n"),
			obj("models/model-v1.bin", ""),
		}},
		{Name: "notebooks", Created: "2026-04-11", Objects: []s3object{
			obj("users/yann/analysis.ipynb", "{\n  \"cells\": [],\n  \"metadata\": { \"kernel\": \"python3\" },\n  \"nbformat\": 4\n}\n"),
			obj("users/alice/scratch.py", "import weft\n\nc = weft.connect()\nfor vm in c.microvms():\n    print(vm.name, vm.status)\n"),
			obj("shared/data.parquet", ""),
		}},
		{Name: "backups", Created: "2026-05-01", Objects: []s3object{
			obj("db/2026-05-01.dump", ""),
			obj("db/2026-05-02.dump", ""),
		}},
	}
}

func obj(key, content string) s3object {
	return s3object{
		Key:         key,
		Size:        sizeOf(key, content),
		Modified:    "2026-05-20",
		ContentType: guessType(key),
		Content:     content,
	}
}

// sizeOf uses the text length when known, else a plausible mock size.
func sizeOf(key, content string) int64 {
	if content != "" {
		return int64(len(content))
	}
	switch {
	case strings.HasSuffix(key, ".bin"):
		return 412 << 20
	case strings.HasSuffix(key, ".parquet"):
		return 88 << 20
	case strings.HasSuffix(key, ".dump"):
		return 1<<30 + 320<<20
	default:
		return 4096
	}
}

func guessType(key string) string {
	switch strings.ToLower(path.Ext(key)) {
	case ".md":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".txt", ".log":
		return "text/plain"
	case ".py":
		return "text/x-python"
	case ".json", ".ipynb":
		return "application/json"
	case ".bin", ".parquet", ".dump", ".img", ".raw":
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

func previewable(ct string) bool {
	return strings.HasPrefix(ct, "text/") || ct == "application/json"
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func findBucket(name string) *bucket {
	for _, b := range buckets {
		if b.Name == name {
			return b
		}
	}
	return nil
}

func bucketsCount() int {
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	return len(buckets)
}

// bucketSummaries powers the sidebar count + the bucket list (via the generic
// /api/resources/buckets route).
func bucketSummaries() []map[string]any {
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	out := make([]map[string]any, 0, len(buckets))
	for _, b := range buckets {
		var total int64
		for _, o := range b.Objects {
			total += o.Size
		}
		out = append(out, row(
			"name", b.Name, "objects", len(b.Objects),
			"size", humanSize(total), "created", b.Created,
		))
	}
	return out
}

// ---- handlers ----

func handleCreateBucket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		badUpload(w, "invalid body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if !bucketName.MatchString(name) {
		badUpload(w, "bucket name must be 3–63 chars, lowercase letters/digits/hyphens")
		return
	}
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	if findBucket(name) != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "bucket already exists"})
		return
	}
	buckets = append(buckets, &bucket{Name: name, Created: time.Now().UTC().Format("2006-01-02")})
	writeJSON(w, http.StatusCreated, map[string]any{"name": name})
}

func handleDeleteBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	for i, b := range buckets {
		if b.Name == name {
			buckets = append(buckets[:i], buckets[i+1:]...)
			// Cascade : drop any attached policy so a re-created bucket
			// with the same name starts clean (no surprise inherited rules).
			delete(policies, name)
			writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
}

// handleGetBucketPolicy — empty {Statements:[]} when no policy is set,
// 404 when the bucket itself doesn't exist. Same Version constant the
// PUT path emits so a get-then-put round-trips identically.
func handleGetBucketPolicy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	if findBucket(name) == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}
	if p, ok := policies[name]; ok && p != nil {
		writeJSON(w, http.StatusOK, p)
		return
	}
	writeJSON(w, http.StatusOK, BucketPolicy{Version: "2012-10-17", Statements: []PolicyStatement{}})
}

// handleSetBucketPolicy replaces the bucket's policy atomically. An
// empty statement list is treated as "remove the policy" so the SPA
// can clear back to the default-allow state without a separate verb.
func handleSetBucketPolicy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body BucketPolicy
	if err := decodeJSON(r, &body); err != nil {
		badUpload(w, "invalid body")
		return
	}
	// Validate against the closed-set vocabulary before mutating. One
	// bad statement rejects the whole submission ; partial saves would
	// leave the editor and server out of sync.
	for i, s := range body.Statements {
		if !validPolicyEffects[s.Effect] {
			badUpload(w, "statement "+itoaSafe(i)+": invalid effect "+s.Effect)
			return
		}
		if !validPolicyActions[s.Action] {
			badUpload(w, "statement "+itoaSafe(i)+": invalid action "+s.Action)
			return
		}
		if strings.TrimSpace(s.Principal) == "" || strings.TrimSpace(s.Resource) == "" {
			badUpload(w, "statement "+itoaSafe(i)+": principal and resource are required")
			return
		}
	}
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	if findBucket(name) == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}
	if len(body.Statements) == 0 {
		delete(policies, name)
		writeJSON(w, http.StatusOK, BucketPolicy{Version: "2012-10-17", Statements: []PolicyStatement{}})
		return
	}
	if body.Version == "" {
		body.Version = "2012-10-17"
	}
	policies[name] = &body
	writeJSON(w, http.StatusOK, body)
}

// itoaSafe — local strconv-free int→string for tiny indices in error
// messages. Matches the same style network.go uses for itoa().
func itoaSafe(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// handleListObjects returns the folders (common prefixes) and objects directly
// under ?prefix= inside a bucket. Policy gate : s3:ListBucket on the
// bucket itself (no key — the resource match is bucket-level).
func handleListObjects(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !requirePolicy(w, r, name, "s3:ListBucket", "") {
		return
	}
	prefix := r.URL.Query().Get("prefix")
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	b := findBucket(name)
	if b == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}
	folders, objects := listEntries(b.Objects, prefix)
	writeJSON(w, http.StatusOK, map[string]any{
		"bucket": b.Name, "prefix": prefix, "folders": folders, "objects": objects,
	})
}

// handleGetObject returns one object's metadata + a preview for text
// content. Policy gate : s3:GetObject on the requested key.
func handleGetObject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key := r.URL.Query().Get("key")
	if !requirePolicy(w, r, name, "s3:GetObject", key) {
		return
	}
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	b := findBucket(name)
	if b == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}
	if d, ok := objectDetail(b.Objects, key); ok {
		writeJSON(w, http.StatusOK, d)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such object"})
}

// handleUploadObject stores uploaded files under ?prefix in the bucket.
// Policy gate : s3:PutObject on the destination prefix (the upload
// places files under <prefix>/<filename>, so the prefix is what the
// rule has to match).
func handleUploadObject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		badUpload(w, "invalid multipart form")
		return
	}
	prefix := strings.TrimSpace(r.FormValue("prefix"))
	if !requirePolicy(w, r, name, "s3:PutObject", prefix) {
		return
	}
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	b := findBucket(name)
	if b == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) == 0 {
		badUpload(w, "no files")
		return
	}
	uploaded := readUploads(r.MultipartForm.File["file"], prefix)
	b.Objects = append(b.Objects, uploaded...)
	writeJSON(w, http.StatusCreated, map[string]any{"bucket": b.Name, "added": len(uploaded)})
}

// ---- shared browsing helpers (used by buckets + shares) ----

type objEntry struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	SizeHuman   string `json:"sizeHuman"`
	Modified    string `json:"modified"`
	ContentType string `json:"contentType"`
}

// listEntries splits the objects under prefix into folders (common prefixes
// one level down) and the files directly at that level.
func listEntries(objs []s3object, prefix string) ([]string, []objEntry) {
	seen := map[string]bool{}
	folders := []string{}
	entries := []objEntry{}
	for _, o := range objs {
		if !strings.HasPrefix(o.Key, prefix) {
			continue
		}
		rest := o.Key[len(prefix):]
		if rest == "" {
			continue
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			folder := rest[:i+1]
			if !seen[folder] {
				seen[folder] = true
				folders = append(folders, folder)
			}
			continue
		}
		entries = append(entries, objEntry{
			Name: rest, Key: o.Key, Size: o.Size, SizeHuman: humanSize(o.Size),
			Modified: o.Modified, ContentType: o.ContentType,
		})
	}
	sort.Strings(folders)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return folders, entries
}

// objectDetail returns one object's metadata + a text preview, if present.
func objectDetail(objs []s3object, key string) (map[string]any, bool) {
	for _, o := range objs {
		if o.Key == key {
			return map[string]any{
				"key": o.Key, "size": o.Size, "sizeHuman": humanSize(o.Size),
				"modified": o.Modified, "contentType": o.ContentType,
				"previewable": previewable(o.ContentType), "content": o.Content,
			}, true
		}
	}
	return nil, false
}

// readUploads turns multipart files into objects under prefix, reading small
// text files for inline preview.
func readUploads(files []*multipart.FileHeader, prefix string) []s3object {
	out := make([]s3object, 0, len(files))
	for _, fh := range files {
		key := prefix + fh.Filename
		ct := guessType(key)
		content := ""
		if previewable(ct) && fh.Size <= 256<<10 {
			if f, err := fh.Open(); err == nil {
				if data, err := io.ReadAll(io.LimitReader(f, 256<<10)); err == nil {
					content = string(data)
				}
				_ = f.Close()
			}
		}
		out = append(out, s3object{
			Key: key, Size: fh.Size, Modified: time.Now().UTC().Format("2006-01-02"),
			ContentType: ct, Content: content,
		})
	}
	return out
}
