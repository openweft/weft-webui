// Package audit persists admin-classified actions to an append-only
// JSONL log so an operator can reconstruct "who did what, when" after
// the fact ("who deleted volume X at 14:32 yesterday").
//
// The package is deliberately tiny — no fan-out, no shipper, no
// retention policy beyond size-based rotation. The unit of storage is
// one JSON object per line, suitable for `jq`, `grep`, fluentbit and
// the like. Append-only + a single mutex on writes guarantees lines
// stay framed under concurrent producers (no torn writes, no
// duplicates) without paying for an external dependency.
//
// Two implementations ship :
//
//   - FileLogger : JSONL on disk, mutex-guarded writes, size-based
//                  rotation (renames the current file to
//                  <path>.<RFC3339> on threshold).
//   - NopLogger  : drops everything ; the package default and the
//                  test/dev sentinel.
//
// The Logger interface is the one boundary the rest of the codebase
// depends on so handlers can call audit.Log unconditionally without
// branching on "is audit on ?".
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event is one row in the audit log. All optional fields are tagged
// `omitempty` so the JSONL stays light when callers don't fill them
// (e.g. a healthcheck event has no ResourceID).
type Event struct {
	Timestamp    time.Time         `json:"ts"`
	Subject      string            `json:"subject,omitempty"`
	Tenant       string            `json:"tenant,omitempty"`
	Project      string            `json:"project,omitempty"`
	Action       string            `json:"action"`
	ResourceKind string            `json:"resource_kind,omitempty"`
	ResourceID   string            `json:"resource_id,omitempty"`
	Result       string            `json:"result,omitempty"`
	ErrorMessage string            `json:"error,omitempty"`
	RemoteIP     string            `json:"remote_ip,omitempty"`
	RequestID    string            `json:"request_id,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// Logger is the small interface handlers depend on. Implementations
// must be safe for concurrent Log calls.
type Logger interface {
	Log(ctx context.Context, ev Event)
}

// NopLogger drops every event. Safe to embed wherever a Logger is
// required but persistence is not desired (tests, dev default).
type NopLogger struct{}

// Log on NopLogger is a no-op.
func (NopLogger) Log(_ context.Context, _ Event) {}

// FileLogger appends events to a JSONL file with size-based rotation.
// All writes (and the rotation itself) go through a single mutex so
// concurrent producers stay framed and so a rotation never races a
// half-written event.
type FileLogger struct {
	path     string
	maxBytes int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewFileLogger opens (or creates) path for appending. maxBytes is the
// rotation threshold ; <= 0 disables rotation. The parent directory is
// created if missing so callers can point at a fresh /var/log subtree.
func NewFileLogger(path string, maxBytes int64) (*FileLogger, error) {
	if path == "" {
		return nil, fmt.Errorf("audit: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("audit: mkdir parent: %w", err)
	}
	l := &FileLogger{path: path, maxBytes: maxBytes}
	if err := l.openLocked(); err != nil {
		return nil, err
	}
	return l, nil
}

// openLocked opens the current file in append mode and snapshots its
// size for the rotation threshold. Caller holds l.mu when called from
// rotation ; the constructor doesn't need the lock yet (nothing else
// can see *FileLogger before NewFileLogger returns).
func (l *FileLogger) openLocked() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: stat: %w", err)
	}
	l.f = f
	l.size = info.Size()
	return nil
}

// Log writes one event as a single JSON line. Errors are swallowed —
// the application path must not fail because the audit file is full
// or read-only ; the operator notices via the absence of new lines.
// (We could surface to slog but Logger.Log() is intentionally void to
// keep the call sites trivial.)
func (l *FileLogger) Log(_ context.Context, ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	// Single newline framing : the JSON has no embedded newline since
	// encoding/json escapes control chars in strings.
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	// Rotate BEFORE writing so the resulting file's first byte is the
	// new event — a reader that finds an empty current file knows the
	// last rotation was clean.
	if l.maxBytes > 0 && l.size+int64(len(line)) > l.maxBytes {
		if err := l.rotateLocked(); err != nil {
			// Rotation failed ; fall through and keep writing to the
			// (oversized) current file rather than drop the event.
			_ = err
		}
	}
	n, err := l.f.Write(line)
	if err != nil {
		return
	}
	l.size += int64(n)
}

// rotateLocked renames the current file to <path>.<RFC3339> and opens
// a fresh one. Caller holds l.mu. Timestamp resolution is nanoseconds
// so two rotations in the same second still get distinct names.
func (l *FileLogger) rotateLocked() error {
	if l.f == nil {
		return nil
	}
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("audit: close before rotate: %w", err)
	}
	l.f = nil
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	rotated := l.path + "." + stamp
	if err := os.Rename(l.path, rotated); err != nil {
		// Best-effort : reopen the existing file so we don't leak.
		_ = l.openLocked()
		return fmt.Errorf("audit: rename: %w", err)
	}
	return l.openLocked()
}

// Tail returns the last n events from the audit log, newest first.
// Walks the file from end-of-file backwards a chunk at a time so
// gigabyte-scale logs don't pin memory. Malformed lines are skipped
// silently (best-effort recovery from a truncated tail).
//
// Caller holds no lock — Tail takes l.mu briefly to snapshot the
// current file size, then reads independently. New writes appended
// after the snapshot are simply not seen ; callers expecting strict
// recency should re-call.
func (l *FileLogger) Tail(n int) ([]Event, error) {
	if n <= 0 {
		return nil, nil
	}
	l.mu.Lock()
	if l.f == nil {
		l.mu.Unlock()
		return nil, fmt.Errorf("audit: file closed")
	}
	path := l.path
	size := l.size
	l.mu.Unlock()
	return tailJSONL(path, size, n)
}

// tailJSONL is the file-walk implementation Tail delegates to. Kept
// at package level so tests can hit it without owning a FileLogger.
func tailJSONL(path string, size int64, n int) ([]Event, error) {
	if size == 0 || n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audit: tail open: %w", err)
	}
	defer f.Close()

	// Read in 32 KiB chunks from the end. Keep an "overflow" tail of
	// the previous (older) chunk so a line straddling the boundary
	// gets stitched. Stop when we have >= n events or hit BOF.
	const chunk int64 = 32 * 1024
	var events []Event
	var overflow []byte
	pos := size
	for pos > 0 && len(events) < n {
		readSize := chunk
		if pos < readSize {
			readSize = pos
		}
		pos -= readSize
		buf := make([]byte, readSize)
		if _, err := f.ReadAt(buf, pos); err != nil {
			return nil, fmt.Errorf("audit: read: %w", err)
		}
		// Append the prior overflow (older bytes already at the
		// "right end") onto buf so the boundary line is whole.
		buf = append(buf, overflow...)
		// Find the first newline — bytes before it belong to a line
		// that started in an earlier (older) chunk we haven't read yet.
		firstNL := -1
		for i, b := range buf {
			if b == '\n' {
				firstNL = i
				break
			}
		}
		if firstNL < 0 {
			// No newline in the whole window so far ; carry the entire
			// buffer to the next iteration.
			overflow = buf
			continue
		}
		// Save the head (incomplete line at the OLDER end) as overflow
		// for the next chunk's stitch.
		overflow = buf[:firstNL]
		// Process the remaining bytes — split on '\n', newest line is
		// last. We prepend each parsed event so the final slice is
		// newest-first.
		body := buf[firstNL+1:]
		start := 0
		// Walk lines back-to-front : split on '\n' then reverse.
		var lines [][]byte
		for i := 0; i < len(body); i++ {
			if body[i] == '\n' {
				lines = append(lines, body[start:i])
				start = i + 1
			}
		}
		if start < len(body) {
			lines = append(lines, body[start:])
		}
		// Append newest-first (reverse iteration of `lines`).
		for i := len(lines) - 1; i >= 0; i-- {
			line := lines[i]
			if len(line) == 0 {
				continue
			}
			var ev Event
			if err := json.Unmarshal(line, &ev); err != nil {
				continue
			}
			events = append(events, ev)
			if len(events) >= n {
				return events, nil
			}
		}
	}
	// Handle the BOF case — the leftover overflow is the very first
	// line of the file, may also be a valid event.
	if len(events) < n && len(overflow) > 0 {
		var ev Event
		if err := json.Unmarshal(overflow, &ev); err == nil {
			events = append(events, ev)
		}
	}
	return events, nil
}

// PruneOlderThan deletes rotated audit log files (named
// <path>.<RFC3339Nano>) whose modtime is older than `cutoff`.
// The CURRENT path is never deleted — only its rotated siblings.
//
// Returns the number of files removed + the first error if any
// (rest are logged via slog.Error to keep retention best-effort).
// Idempotent : a second call with the same cutoff is a no-op once
// the eligible files are gone.
func (l *FileLogger) PruneOlderThan(cutoff time.Time) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir := filepath.Dir(l.path)
	base := filepath.Base(l.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("audit: prune readdir: %w", err)
	}
	removed := 0
	var firstErr error
	for _, e := range entries {
		name := e.Name()
		// Only touch files whose name starts with "<base>." — the
		// rotation convention. The current file itself doesn't have
		// the trailing dot so it's skipped automatically.
		if name == base || !strings.HasPrefix(name, base+".") {
			continue
		}
		// Skip non-files (a sibling directory shouldn't be touched).
		if e.Type().IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		victim := filepath.Join(dir, name)
		if err := os.Remove(victim); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

// Close flushes (implicitly via OS) and releases the underlying file.
// Safe to call multiple times.
func (l *FileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
