package server

import (
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
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
)

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
			writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
}

// handleListObjects returns the folders (common prefixes) and objects directly
// under ?prefix= inside a bucket.
func handleListObjects(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	b := findBucket(r.PathValue("name"))
	if b == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}

	type entry struct {
		Name        string `json:"name"`
		Key         string `json:"key"`
		Size        int64  `json:"size"`
		SizeHuman   string `json:"sizeHuman"`
		Modified    string `json:"modified"`
		ContentType string `json:"contentType"`
	}
	seen := map[string]bool{}
	folders := []string{}
	objects := []entry{}
	for _, o := range b.Objects {
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
		objects = append(objects, entry{
			Name: rest, Key: o.Key, Size: o.Size, SizeHuman: humanSize(o.Size),
			Modified: o.Modified, ContentType: o.ContentType,
		})
	}
	sort.Strings(folders)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Name < objects[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{
		"bucket": b.Name, "prefix": prefix, "folders": folders, "objects": objects,
	})
}

// handleGetObject returns one object's metadata + a preview for text content.
func handleGetObject(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	b := findBucket(r.PathValue("name"))
	if b == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}
	for _, o := range b.Objects {
		if o.Key == key {
			writeJSON(w, http.StatusOK, map[string]any{
				"key": o.Key, "size": o.Size, "sizeHuman": humanSize(o.Size),
				"modified": o.Modified, "contentType": o.ContentType,
				"previewable": previewable(o.ContentType), "content": o.Content,
			})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such object"})
}

// handleUploadObject stores uploaded files under ?prefix in the bucket.
func handleUploadObject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		badUpload(w, "invalid multipart form")
		return
	}
	prefix := strings.TrimSpace(r.FormValue("prefix"))
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	b := findBucket(r.PathValue("name"))
	if b == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such bucket"})
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) == 0 {
		badUpload(w, "no files")
		return
	}
	added := 0
	for _, fh := range r.MultipartForm.File["file"] {
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
		b.Objects = append(b.Objects, s3object{
			Key: key, Size: fh.Size, Modified: time.Now().UTC().Format("2006-01-02"),
			ContentType: ct, Content: content,
		})
		added++
	}
	writeJSON(w, http.StatusCreated, map[string]any{"bucket": b.Name, "added": added})
}
