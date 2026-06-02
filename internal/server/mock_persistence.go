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
	"sort"
	"strings"
	"sync"
	"time"
)

// stateHistoryKeep is the number of timestamped pre-mutation snapshots
// the persistence layer keeps under <path>.history/ for each tracked
// state file. <= 0 = disabled (no history written) ; default. Set
// once at startup from Config.StateHistoryKeep.
var (
	stateHistoryMu   sync.RWMutex
	stateHistoryKeep int
	// historyNowFn is overridable from tests so we can produce
	// deterministic filenames without sleeping. Production reads
	// time.Now ; tests inject a monotonic stub.
	historyNowFn = time.Now
)

// SetStateHistoryKeep arms history rotation across every mock-layer
// state file (inventory, dns, security, scripts). Each successful
// flush renames the PREVIOUS file to <path>.history/<RFC3339Nano>.json
// before installing the new one, then prunes the directory down to
// the N most-recent entries. Idempotent ; safe to call once at
// startup. <= 0 disables the feature entirely.
func SetStateHistoryKeep(n int) {
	stateHistoryMu.Lock()
	stateHistoryKeep = n
	stateHistoryMu.Unlock()
}

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
	// History rotation : if there's an existing file AND the operator
	// armed retention, archive it under <path>.history/ before the
	// rename installs the new content. The archive name carries the
	// time the snapshot was TAKEN (i.e. the previous version's birth
	// time would be ideal, but the file timestamp is unreliable on
	// some filesystems — use the time we're moving it instead).
	stateHistoryMu.RLock()
	keep := stateHistoryKeep
	stateHistoryMu.RUnlock()
	if keep > 0 {
		if _, err := os.Stat(path); err == nil {
			archiveStateFile(path)
			pruneStateHistory(path, keep)
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		slog.Error("mock-persistence: rename failed", "from", tmp, "to", path, "err", err.Error())
		_ = os.Remove(tmp)
		return
	}
}

// historyDir returns the per-state-file archive directory : the
// sibling <basename>.history/ next to the path. Keeps the archive
// co-located so an operator who moves the state file in a deploy
// also moves the history.
func historyDir(path string) string {
	return path + ".history"
}

// archiveStateFile moves the current path to <path>.history/<ts>.json
// where ts is RFC3339Nano UTC. Best-effort : on failure we slog +
// continue ; the new file still gets installed on top of the old.
// The point is undo support, not transactional guarantees.
func archiveStateFile(path string) {
	dir := historyDir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("mock-persistence: history mkdir failed", "dir", dir, "err", err.Error())
		return
	}
	stamp := historyNowFn().UTC().Format(time.RFC3339Nano)
	// Filesystems on Windows / older HFS don't accept colons in names.
	// RFC3339Nano contains them ; substitute to '-' so the archive is
	// portable. The audit log uses the same convention via
	// FileLogger.rotateLocked.
	stamp = strings.ReplaceAll(stamp, ":", "-")
	target := filepath.Join(dir, stamp+".json")
	if err := os.Rename(path, target); err != nil {
		slog.Error("mock-persistence: history archive failed",
			"from", path, "to", target, "err", err.Error())
	}
}

// pruneStateHistory drops the oldest entries beyond keep. Sorts by
// filename which is monotonic for RFC3339Nano-stamped files. No-op
// when the directory has fewer than keep entries.
func pruneStateHistory(path string, keep int) {
	dir := historyDir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Most-recent rotation created the dir ; ReadDir failing
		// here is "no history yet" or "permission" — silently move on.
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names) // RFC3339Nano sorts lexicographically by time
	// Remove the oldest (front of slice) until len == keep.
	excess := len(names) - keep
	for i := 0; i < excess; i++ {
		victim := filepath.Join(dir, names[i])
		if err := os.Remove(victim); err != nil {
			slog.Error("mock-persistence: history prune failed",
				"path", victim, "err", err.Error())
		}
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
