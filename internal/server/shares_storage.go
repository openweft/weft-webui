// shares_storage.go — in-memory store for the POSIX (RWX) shares
// browser. The HTTP handlers moved to api_storage.go (huma) ; this
// file keeps the mock seed.
package server

import "sync"

// shareFiles seeds a few demonstrative shares so the browser has
// something to render on first open. Share names match the "shares"
// registry rows. Same shape as the bucket store ; the only difference
// is the access model (RWX vs S3) — that's not visible at the wire.
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
