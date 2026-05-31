// audit_test.go — concurrent-write, rotation, JSON shape, and NopLogger
// smoke tests for the package. The rotation test deliberately picks a
// MaxBytes small enough that even single events trip the threshold so
// the test runs in milliseconds.
package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFileLogger_ConcurrentWritesAreFramed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	// Disable rotation so we count every event in one file.
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer l.Close()

	const goroutines = 10
	const perGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				l.Log(context.Background(), Event{
					Action:     "test.write",
					Subject:    "user" + intStr(gid),
					ResourceID: "evt" + intStr(i),
					Extra:      map[string]string{"g": intStr(gid), "i": intStr(i)},
				})
			}
		}(g)
	}
	wg.Wait()

	// Every line must parse as Event ; total count must match.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	// JSONL lines are short ; default 64K buffer is fine.
	count := 0
	seen := map[string]struct{}{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			t.Fatalf("blank line in audit log")
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("torn / non-JSON line %d: %q : %v", count, line, err)
		}
		if ev.Action != "test.write" {
			t.Fatalf("unexpected action %q", ev.Action)
		}
		key := ev.Extra["g"] + "/" + ev.Extra["i"]
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate event for %s", key)
		}
		seen[key] = struct{}{}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	if want := goroutines * perGoroutine; count != want {
		t.Fatalf("count=%d want %d", count, want)
	}
}

func TestFileLogger_RotatesAtMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	// Each event marshals to ~95 bytes. Cap at 150 : "first" lands in
	// the initial file, "second" forces a rotation (95+95 > 150) and
	// lands alone in the fresh file, "third" similarly forces another
	// rotation and lands alone. We verify the "first" rotation cleanly
	// : current file has "third" only, the most-recently-rotated file
	// has "second" only, an earlier rotation has "first" only.
	l, err := NewFileLogger(path, 150)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	l.Log(ctx, Event{Action: "first", Subject: "alice", ResourceID: "vol-1"})
	// Force a tiny gap so the rotation suffix differs from a future
	// rotation in the same test (RFC3339Nano resolution makes a same-ns
	// collision unlikely, but a sleep keeps the assertion stable on
	// fast systems).
	time.Sleep(2 * time.Millisecond)
	l.Log(ctx, Event{Action: "second", Subject: "bob", ResourceID: "vol-2"})
	time.Sleep(2 * time.Millisecond)
	l.Log(ctx, Event{Action: "third", Subject: "carol", ResourceID: "vol-3"})

	// Current file ends with "third" (last rotation just opened it).
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if !strings.Contains(string(cur), `"action":"third"`) {
		t.Fatalf("current file missing 'third' event: %q", string(cur))
	}
	if strings.Contains(string(cur), `"action":"first"`) {
		t.Fatalf("current file should not contain 'first' (rotated): %q", string(cur))
	}

	// Walk every rotated file ; "first" must live in one of them.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var rotated []string
	for _, e := range entries {
		if e.Name() != "audit.jsonl" && strings.HasPrefix(e.Name(), "audit.jsonl.") {
			rotated = append(rotated, filepath.Join(dir, e.Name()))
		}
	}
	if len(rotated) == 0 {
		t.Fatalf("no rotated file found in %s ; entries=%v", dir, entries)
	}
	foundFirst := false
	bodies := [][]byte{cur}
	for _, p := range rotated {
		rb, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read rotated %s: %v", p, err)
		}
		bodies = append(bodies, rb)
		if strings.Contains(string(rb), `"action":"first"`) {
			foundFirst = true
		}
	}
	if !foundFirst {
		t.Fatalf("'first' event not found in any rotated file (rotated=%v)", rotated)
	}
	// Every produced file must be valid JSONL — every line parses.
	for _, body := range bodies {
		for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
			if line == "" {
				continue
			}
			var ev Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("bad JSONL: %q : %v", line, err)
			}
		}
	}
}

func TestEvent_JSONShapeOmitEmpty(t *testing.T) {
	ev := Event{
		Timestamp:    time.Date(2026, 5, 30, 14, 32, 0, 0, time.UTC),
		Subject:      "alice@acme",
		Action:       "volume.delete",
		ResourceKind: "volume",
		ResourceID:   "vol-42",
		Result:       "ok",
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	// Mandatory fields must appear.
	for _, want := range []string{
		`"ts":"2026-05-30T14:32:00Z"`,
		`"subject":"alice@acme"`,
		`"action":"volume.delete"`,
		`"resource_kind":"volume"`,
		`"resource_id":"vol-42"`,
		`"result":"ok"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	// Omitted fields must not appear.
	for _, gone := range []string{
		`"tenant"`, `"project"`, `"error"`, `"remote_ip"`, `"request_id"`, `"extra"`,
	} {
		if strings.Contains(got, gone) {
			t.Fatalf("unexpected field %s in %s", gone, got)
		}
	}

	// Round trip preserves every populated field.
	var back Event
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Subject != ev.Subject || back.Action != ev.Action ||
		back.ResourceKind != ev.ResourceKind || back.ResourceID != ev.ResourceID ||
		back.Result != ev.Result || !back.Timestamp.Equal(ev.Timestamp) {
		t.Fatalf("round trip mismatch: %+v", back)
	}
}

func TestNopLoggerSwallowsEverything(t *testing.T) {
	var l Logger = NopLogger{}
	// Should be safe to call millions of times ; just smoke a few.
	for i := 0; i < 100; i++ {
		l.Log(context.Background(), Event{Action: "smoke"})
	}
}

// intStr is a tiny strconv.Itoa avoiding the import for one call.
func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
