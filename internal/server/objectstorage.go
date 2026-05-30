package server

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

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
	Effect    string `json:"effect" enum:"Allow,Deny"`
	Principal string `json:"principal"` // OIDC sub OR "*"
	Action    string `json:"action" enum:"s3:GetObject,s3:PutObject,s3:DeleteObject,s3:ListBucket"`
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
// key in this bucket?". The semantics :
//
//   - no policy on the bucket           → allow (uncontrolled object)
//   - cluster/tenant admin              → allow (S3-root bypass, same
//                                         shortcut Shares uses)
//   - explicit Deny match               → deny (always wins)
//   - explicit Allow match              → allow
//   - no statement matches              → policyStrict ? deny : allow
//
// The last branch is the policy-strict knob (config.PolicyStrict
// → server.policyStrict). Off by default to avoid upgrade-time
// lock-outs from a bucket that has an Allow-only policy ; on
// gives the AWS-aligned default-deny once the operator is sure.
//
// key may be "" for bucket-level actions (s3:ListBucket).
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
	if policyStrict {
		return policyDecision{
			allow:  false,
			reason: "denied by policy : no matching statement (strict mode)",
		}
	}
	return policyDecision{allow: true}
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

// (requirePolicy moved to api_storage.go as requirePolicyCtx ; this
// file keeps just the policy data + evaluator.)

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

// (Bucket / object handlers moved to huma — see api_storage.go.)

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

// ---- shared browsing helpers (used by buckets + shares) ----

// ObjectEntry is one row in an ObjectListing — a file directly under
// the listed prefix. The folders[] siblings carry just the path
// segment (foo/) ; they're rendered as nav items in the SPA.
type ObjectEntry struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	SizeHuman   string `json:"sizeHuman"`
	Modified    string `json:"modified"`
	ContentType string `json:"contentType"`
}

// ObjectListing is what /api/buckets/{name}/objects and the
// equivalent shares endpoint return. Bucket carries the bucket
// name on the buckets path ; the shares path leaves it empty since
// the Share name lives in the URL.
type ObjectListing struct {
	Bucket  string        `json:"bucket,omitempty"`
	Share   string        `json:"share,omitempty"`
	Prefix  string        `json:"prefix"`
	Folders []string      `json:"folders"`
	Objects []ObjectEntry `json:"objects"`
}

// ObjectDetail is one object's metadata + a (capped) preview for
// previewable content. Previewable is true when ContentType begins
// with text/ or matches the small allowlist in previewable().
type ObjectDetail struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	SizeHuman   string `json:"sizeHuman"`
	Modified    string `json:"modified"`
	ContentType string `json:"contentType"`
	Previewable bool   `json:"previewable"`
	Content     string `json:"content"`
}

// listEntries splits the objects under prefix into folders (common prefixes
// one level down) and the files directly at that level.
func listEntries(objs []s3object, prefix string) ([]string, []ObjectEntry) {
	seen := map[string]bool{}
	folders := []string{}
	entries := []ObjectEntry{}
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
		entries = append(entries, ObjectEntry{
			Name: rest, Key: o.Key, Size: o.Size, SizeHuman: humanSize(o.Size),
			Modified: o.Modified, ContentType: o.ContentType,
		})
	}
	sort.Strings(folders)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return folders, entries
}

// objectDetail returns one object's metadata + a text preview, if present.
func objectDetail(objs []s3object, key string) (*ObjectDetail, bool) {
	for _, o := range objs {
		if o.Key == key {
			return &ObjectDetail{
				Key: o.Key, Size: o.Size, SizeHuman: humanSize(o.Size),
				Modified: o.Modified, ContentType: o.ContentType,
				Previewable: previewable(o.ContentType), Content: o.Content,
			}, true
		}
	}
	return nil, false
}

// (readUploads moved to api_storage.go as readUploadsHuma, which
// takes huma.FormFile rather than *multipart.FileHeader. Same body
// semantics — 256 KiB preview cap for text/* content.)
