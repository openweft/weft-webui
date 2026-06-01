// mock_persistence.go — shared infrastructure for the dev/mock-layer
// state files (inventory, dns, security-groups, scripts). Each mock
// layer owns its own snapshot type + flush function ; this file
// provides the atomic-write + best-effort-log glue they all need.
//
// Why per-resource files rather than one big state.json :
//   - Hot paths only touch one file (DNS edit doesn't pay for a
//     security-groups marshal).
//   - Operators can move / version / nuke each independently.
//   - The eventual etcd migration is per-resource anyway (different
//     RPCs, different keys) ; the on-disk shape mirrors that.

package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// atomicWriteJSON marshals v (with two-space indent for human-diffable
// files) and writes it to path through a <path>.tmp staging file +
// os.Rename. A torn write never leaves an unparseable file on disk.
//
// Errors are logged at slog.Error and absorbed — persistence is a
// best-effort backstop for the in-memory state, NEVER the source of
// truth at mutation time. A failed flush still leaves the in-memory
// change correct ; the next successful flush will pick it up.
func atomicWriteJSON(path string, v any) {
	if path == "" {
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		slog.Error("mock-persistence: marshal failed", "path", path, "err", err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Error("mock-persistence: mkdir failed", "path", path, "err", err.Error())
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		slog.Error("mock-persistence: write tmp failed", "tmp", tmp, "err", err.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Error("mock-persistence: rename failed", "from", tmp, "to", path, "err", err.Error())
		_ = os.Remove(tmp)
		return
	}
}

// readJSON loads path into v. Returns (loaded=true, nil) on success,
// (false, nil) when the file doesn't exist (first-run case), and
// (false, err) on every other failure. Callers decide whether to
// fatal on a load error ; mock layers typically just slog.Warn and
// keep the in-memory seed.
func readJSON(path string, v any) (loaded bool, err error) {
	if path == "" {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, err
	}
	return true, nil
}
