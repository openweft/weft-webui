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
