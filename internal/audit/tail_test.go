// tail_test.go — pin the JSONL-tail reader on a few synthetic
// fixtures : N events present, fewer-than-N events, malformed line
// skipped, empty file returns nil, multiple-chunk boundary stitches
// cleanly.

package audit

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTail_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 5; i++ {
		l.Log(context.Background(), Event{
			Timestamp: time.Date(2026, 6, 1, 12, 0, i, 0, time.UTC),
			Action:    "test.event",
			Subject:   "alice",
			Extra:     map[string]string{"i": "x"},
		})
	}
	got, err := l.Tail(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	// Newest first : timestamps should be 4 / 3 / 2 in that order.
	if got[0].Timestamp.Second() != 4 {
		t.Errorf("got[0] second = %d, want 4 (newest)", got[0].Timestamp.Second())
	}
	if got[2].Timestamp.Second() != 2 {
		t.Errorf("got[2] second = %d, want 2", got[2].Timestamp.Second())
	}
}

func TestTail_FewerThanRequested(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Log(context.Background(), Event{Action: "only.one"})
	got, _ := l.Tail(100)
	if len(got) != 1 {
		t.Errorf("want 1 (file has 1), got %d", len(got))
	}
}

func TestTail_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got, err := l.Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty file: want 0 events, got %d", len(got))
	}
}

func TestTail_SkipsMalformedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Log(context.Background(), Event{Action: "good.1"})
	// Append a malformed line directly to the file under the lock.
	l.mu.Lock()
	_, _ = l.f.Write([]byte("not-json-at-all\n"))
	l.size += int64(len("not-json-at-all\n"))
	l.mu.Unlock()
	l.Log(context.Background(), Event{Action: "good.2"})

	got, _ := l.Tail(10)
	if len(got) != 2 {
		t.Errorf("want 2 good events (malformed skipped), got %d", len(got))
	}
	if got[0].Action != "good.2" || got[1].Action != "good.1" {
		t.Errorf("order/contents = %+v", got)
	}
}

func TestTail_MultipleChunks(t *testing.T) {
	// Force a multi-chunk read by writing big payloads. Each event
	// is ~700 bytes ; 100 events = ~70 KiB which is >> the 32 KiB
	// chunk size, so the boundary stitch is exercised. Identify each
	// event by its Extra["i"] tag — Timestamp.Second() wraps at 60
	// so it can't disambiguate events 60..99.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := NewFileLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	bigSubject := strings.Repeat("x", 600)
	for i := 0; i < 100; i++ {
		l.Log(context.Background(), Event{
			Action:  "big.event",
			Subject: bigSubject,
			Extra:   map[string]string{"i": itoa(i)},
		})
	}
	got, err := l.Tail(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("want 50 events, got %d", len(got))
	}
	if got[0].Extra["i"] != "99" {
		t.Errorf("got[0].i = %q, want 99 (newest)", got[0].Extra["i"])
	}
	if got[49].Extra["i"] != "50" {
		t.Errorf("got[49].i = %q, want 50", got[49].Extra["i"])
	}
}

// itoa is a tiny strconv.Itoa shim so this test file stays lean ;
// the audit package already pulls in encoding/json which is heavier.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
