// prune_test.go — pin PruneOlderThan : deletes rotated siblings
// older than cutoff, leaves the current file alone, copes with
// stray non-audit files in the same dir.

package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneOlderThan_KeepsCurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Log(context.Background(), Event{Action: "test"})

	// Pretend everything is ancient.
	removed, err := l.PruneOlderThan(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (current file must survive)", removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("current file missing after prune : %v", err)
	}
}

func TestPruneOlderThan_DropsAgedRotated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// Synthesise two rotated siblings : one ancient, one fresh.
	ancient := path + ".2026-05-01T00:00:00Z"
	fresh := path + ".2026-06-02T00:00:00Z"
	for _, p := range []string{ancient, fresh} {
		if err := os.WriteFile(p, []byte("payload\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Stamp the ancient one's mtime to a week ago.
	weekAgo := time.Now().Add(-7 * 24 * time.Hour)
	if err := os.Chtimes(ancient, weekAgo, weekAgo); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	removed, err := l.PruneOlderThan(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(ancient); !os.IsNotExist(err) {
		t.Errorf("ancient still on disk : %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh sibling missing : %v", err)
	}
}

func TestPruneOlderThan_IgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// A neighbouring file that's NOT a rotation of ours — different
	// base name. Prune must leave it alone even when ancient.
	neighbour := filepath.Join(dir, "other.log")
	if err := os.WriteFile(neighbour, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	weekAgo := time.Now().Add(-7 * 24 * time.Hour)
	if err := os.Chtimes(neighbour, weekAgo, weekAgo); err != nil {
		t.Fatal(err)
	}

	removed, err := l.PruneOlderThan(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (must not touch other.log)", removed)
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Errorf("neighbour file got deleted : %v", err)
	}
}

func TestTailAll_ReadsCurrentAndRotated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// 2 events in the CURRENT file, plus a rotated sibling with 3 events.
	l.Log(context.Background(), Event{Action: "current.1"})
	l.Log(context.Background(), Event{Action: "current.2"})

	// Synthesise the rotated sibling. Each line must be a valid JSON
	// audit event ; just an action field is enough.
	rotated := path + ".2026-05-01T00:00:00Z"
	body := `{"ts":"2026-05-01T00:00:00Z","action":"old.1"}
{"ts":"2026-05-01T00:00:01Z","action":"old.2"}
{"ts":"2026-05-01T00:00:02Z","action":"old.3"}
`
	if err := os.WriteFile(rotated, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	weekAgo := time.Now().Add(-7 * 24 * time.Hour)
	if err := os.Chtimes(rotated, weekAgo, weekAgo); err != nil {
		t.Fatal(err)
	}

	// Tail() (current-file-only) sees 2.
	got, err := l.Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("Tail = %d, want 2 (current file only)", len(got))
	}

	// TailAll() sees 2 + 3 = 5.
	gotAll, err := l.TailAll(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotAll) != 5 {
		t.Errorf("TailAll = %d, want 5 (current + rotated)", len(gotAll))
	}
	// Newest-first across files : current.2 / current.1 / old.3 / old.2 / old.1
	wantActions := []string{"current.2", "current.1", "old.3", "old.2", "old.1"}
	for i := range gotAll {
		if gotAll[i].Action != wantActions[i] {
			t.Errorf("gotAll[%d].Action = %q, want %q", i, gotAll[i].Action, wantActions[i])
		}
	}
}

func TestTailAll_RespectsLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	for i := 0; i < 3; i++ {
		l.Log(context.Background(), Event{Action: "c"})
	}
	rotated := path + ".2026-05-01T00:00:00Z"
	body := `{"ts":"2026-05-01T00:00:00Z","action":"o"}
{"ts":"2026-05-01T00:00:01Z","action":"o"}
`
	_ = os.WriteFile(rotated, []byte(body), 0o644)
	old := time.Now().Add(-24 * time.Hour)
	_ = os.Chtimes(rotated, old, old)

	// Ask for 2 — should stop at current file, never touch rotated.
	got, err := l.TailAll(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d events, want 2 (limit)", len(got))
	}
	for _, ev := range got {
		if ev.Action != "c" {
			t.Errorf("limit blew past current file ; got %q", ev.Action)
		}
	}
}

func TestPruneOlderThan_EmptyDirIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	removed, err := l.PruneOlderThan(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (no rotated siblings)", removed)
	}
}
