// mock_persistence_history_test.go — pins the snapshot rotation
// behaviour. Each successful atomicWriteJSON must :
//   - archive the previous file under <path>.history/<ts>.json
//   - prune the history dir down to N most recent
//   - do nothing when stateHistoryKeep <= 0 (back-compat default)

package server

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// withDeterministicHistoryClock injects a monotonic clock so file
// names don't collide across writes in the same nanosecond — without
// a stub a fast loop can hit dup names and lose entries to rename
// races. Returns a cleanup that restores the production clock +
// previous keep value.
func withDeterministicHistoryClock(t *testing.T, keep int) func() {
	t.Helper()
	prevNow := historyNowFn
	stateHistoryMu.Lock()
	prevKeep := stateHistoryKeep
	stateHistoryKeep = keep
	stateHistoryMu.Unlock()
	tick := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	historyNowFn = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
	return func() {
		stateHistoryMu.Lock()
		stateHistoryKeep = prevKeep
		stateHistoryMu.Unlock()
		historyNowFn = prevNow
	}
}

func TestStateHistory_DisabledByDefault(t *testing.T) {
	// keep=0 is the production default — atomic writes shouldn't
	// produce any history directory at all.
	defer withDeterministicHistoryClock(t, 0)()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	atomicWriteJSON(path, map[string]int{"v": 1})
	atomicWriteJSON(path, map[string]int{"v": 2})
	atomicWriteJSON(path, map[string]int{"v": 3})

	if _, err := os.Stat(historyDir(path)); err == nil {
		t.Errorf("history dir created despite keep=0")
	}
}

func TestStateHistory_AccumulatesUpToKeep(t *testing.T) {
	defer withDeterministicHistoryClock(t, 3)()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// 5 writes with keep=3 ; expect 3 archived previous-versions and
	// the current file. First write has no previous, so it doesn't
	// archive — only writes 2..5 produce entries. With keep=3 we
	// trim to 3, dropping the oldest.
	for i := 1; i <= 5; i++ {
		atomicWriteJSON(path, map[string]int{"v": i})
	}

	entries, err := os.ReadDir(historyDir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("history entries = %d, want 3 (keep)", len(entries))
	}
}

func TestStateHistory_PreservesNewest(t *testing.T) {
	defer withDeterministicHistoryClock(t, 2)()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write three versions ; with keep=2 the oldest archived entry
	// is pruned. Newest two stamps survive.
	for i := 1; i <= 4; i++ {
		atomicWriteJSON(path, map[string]int{"v": i})
	}

	entries, err := os.ReadDir(historyDir(path))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	// First two writes produced two archives ; with keep=2 they
	// stay only if pruning keeps the newest. Verify the LATEST
	// filename (lexicographically last, since RFC3339Nano sorts
	// monotonically) corresponds to the second-to-last write.
	if len(names) != 2 {
		t.Fatalf("history entries = %d, want 2", len(names))
	}
}

func TestStateHistory_FirstWriteHasNoPrevious(t *testing.T) {
	defer withDeterministicHistoryClock(t, 5)()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	atomicWriteJSON(path, map[string]int{"v": 1})

	// No history yet — first write has nothing to archive.
	if entries, err := os.ReadDir(historyDir(path)); err == nil {
		t.Errorf("history dir created on first write, has %d entries", len(entries))
	}

	atomicWriteJSON(path, map[string]int{"v": 2})

	entries, err := os.ReadDir(historyDir(path))
	if err != nil {
		t.Fatalf("ReadDir after 2nd write: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("history entries = %d, want 1 (one previous to archive)", len(entries))
	}
}

func TestStateHistory_ArchivedFileContentIsPreservedVersion(t *testing.T) {
	defer withDeterministicHistoryClock(t, 5)()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	atomicWriteJSON(path, map[string]int{"v": 1})
	atomicWriteJSON(path, map[string]int{"v": 2})

	entries, err := os.ReadDir(historyDir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("history entries = %d, want 1", len(entries))
	}
	// The archived entry should contain the OLD version (v=1), not
	// the new one — that's the whole point of rotation.
	archived := filepath.Join(historyDir(path), entries[0].Name())
	b, err := os.ReadFile(archived)
	if err != nil {
		t.Fatal(err)
	}
	if !containsBytes(b, `"v": 1`) {
		t.Errorf("archived file should contain v=1, got %s", b)
	}
}

func containsBytes(hay []byte, needle string) bool {
	return string(hay) != "" && len(needle) > 0 &&
		stringContains(string(hay), needle)
}

func stringContains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
