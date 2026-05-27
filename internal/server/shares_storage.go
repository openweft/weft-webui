package server

import (
	"net/http"
	"strings"
	"sync"
)

// Shares — the POSIX (RWX) face of CubeFS. Same browsing model as buckets,
// but a share is a filesystem mounted by many microVMs at once. Seeded with
// mock trees ; the share names match the "shares" registry rows.

var (
	sharesMu   sync.Mutex
	shareFiles = map[string][]s3object{
		"team-data": {
			obj("README.md", "# team-data (share)\n\nRWX filesystem mounted across team-alpha workloads.\n"),
			obj("datasets/q1/sales.csv", "month,region,total\njan,dc-a,18422\nfeb,dc-b,20140\nmar,dc-c,19980\n"),
			obj("datasets/q1/notes.md", "# Q1\n\nNumbers look healthy ; dc-c recovered after the rack-2 drain.\n"),
			obj("scripts/etl.py", "import csv, sys\n\nfor row in csv.reader(sys.stdin):\n    print(row)\n"),
			obj("models/model.pt", ""),
		},
		"notebooks": {
			obj("yann/explore.ipynb", "{\n  \"cells\": [],\n  \"metadata\": {},\n  \"nbformat\": 4\n}\n"),
			obj("alice/train.py", "import weft\n\n# train against the shared dataset mount\nds = open('/data/dataset.parquet', 'rb')\n"),
			obj("shared/dataset.parquet", ""),
		},
		"models": {
			obj("registry.json", "{\n  \"models\": [\"llama\", \"mistral\"],\n  \"backend\": \"cubefs\"\n}\n"),
			obj("llama/config.json", "{\n  \"context\": 8192,\n  \"params\": \"8B\"\n}\n"),
			obj("llama/weights.bin", ""),
		},
	}
)

func handleListShareObjects(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	sharesMu.Lock()
	defer sharesMu.Unlock()
	objs, ok := shareFiles[r.PathValue("name")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such share"})
		return
	}
	folders, entries := listEntries(objs, prefix)
	writeJSON(w, http.StatusOK, map[string]any{
		"share": r.PathValue("name"), "prefix": prefix, "folders": folders, "objects": entries,
	})
}

func handleGetShareObject(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	sharesMu.Lock()
	defer sharesMu.Unlock()
	objs, ok := shareFiles[r.PathValue("name")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such share"})
		return
	}
	if d, ok := objectDetail(objs, key); ok {
		writeJSON(w, http.StatusOK, d)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such object"})
}

func handleUploadShareObject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		badUpload(w, "invalid multipart form")
		return
	}
	prefix := strings.TrimSpace(r.FormValue("prefix"))
	name := r.PathValue("name")
	sharesMu.Lock()
	defer sharesMu.Unlock()
	objs, ok := shareFiles[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such share"})
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) == 0 {
		badUpload(w, "no files")
		return
	}
	uploaded := readUploads(r.MultipartForm.File["file"], prefix)
	shareFiles[name] = append(objs, uploaded...)
	writeJSON(w, http.StatusCreated, map[string]any{"share": name, "added": len(uploaded)})
}
