// Package diagnoses receives Diagnosis records published by
// weft-doctor on NATS (subjects weft.diagnosis.<severity>.<hash>) and
// keeps the latest copy of each pattern in an in-memory cache for the
// webui's Diagnoses panel. Mirror image of weft-doctor's GitHubSink
// rendering, but live in-process instead of as GitHub issue bodies.
//
// Lifecycle :
//   cache := diagnoses.NewCache(opts)
//   defer cache.Close()                      // drains NATS, closes streams
//   cache.Snapshot()                         // current sorted slice
//   cache.Subscribe()                        // returns chan<-Diagnosis for SSE
//   cache.Unsubscribe(ch)
package diagnoses

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Severity matches weft-doctor's classify.Severity. We re-declare
// the string constants locally rather than depend on weft-doctor's
// classify package — that package is meant for the producer side,
// and pulling it into the consumer would create a circular-ish
// philosophical dependency. The wire format is JSON ; the strings
// are the contract.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// Diagnosis is the JSON shape weft-doctor publishes on NATS. Fields
// match classify.Diagnosis exactly so we can json.Unmarshal directly.
type Diagnosis struct {
	PatternHash     string     `json:"pattern_hash"`
	Severity        Severity   `json:"severity"`
	Title           string     `json:"title"`
	RootCause       string     `json:"root_cause,omitempty"`
	SuggestedAction string     `json:"suggested_action,omitempty"`
	FileLocation    string     `json:"file_location,omitempty"`
	Occurrences     int        `json:"occurrences"`
	FirstSeen       time.Time  `json:"first_seen,omitempty"`
	LastSeen        time.Time  `json:"last_seen,omitempty"`
	Examples        []LogEvent `json:"examples,omitempty"`
}

// LogEvent mirrors classify.LogEvent.
type LogEvent struct {
	Time   time.Time      `json:"time,omitempty"`
	Level  string         `json:"level,omitempty"`
	Msg    string         `json:"msg,omitempty"`
	Attrs  map[string]any `json:"attrs,omitempty"`
	Source string         `json:"source,omitempty"`
}

// Options configures the cache.
type Options struct {
	// NATSURL is the connection target. Empty disables subscription
	// (the cache stays empty ; Snapshot returns []). Useful in dev
	// and test modes — the panel still loads, just empty.
	NATSURL string
	// Subject is the wildcard the cache subscribes to. Default
	// "weft.diagnosis.>".
	Subject string
	// MaxRecent caps the cache size. Older entries (by LastSeen) drop
	// off when the cache exceeds this count. Default 100.
	MaxRecent int
	// Logger is the slog logger. Default slog.Default().
	Logger *slog.Logger
}

// Cache is the thread-safe in-memory store of recent Diagnoses.
type Cache struct {
	subject   string
	maxRecent int
	log       *slog.Logger

	mu     sync.RWMutex
	byHash map[string]Diagnosis

	// streams listed in subscribers receive every new Diagnosis
	// (deduped or fresh, by pattern_hash). The webui SSE handler
	// holds one per connected browser ; the slice membership is
	// guarded by streamMu.
	streamMu sync.Mutex
	streams  map[chan Diagnosis]struct{}

	conn *nats.Conn
	sub  *nats.Subscription
}

// NewCache builds the cache. When NATSURL is non-empty it dials and
// starts the subscription synchronously ; dial failure returns the
// error (let main.go decide whether to fail-loud or degrade).
//
// When NATSURL is empty, the cache is "offline" : it accepts manual
// Add() calls (useful in tests) but never receives NATS messages.
func NewCache(opts Options) (*Cache, error) {
	if opts.Subject == "" {
		opts.Subject = "weft.diagnosis.>"
	}
	if opts.MaxRecent == 0 {
		opts.MaxRecent = 100
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	c := &Cache{
		subject:   opts.Subject,
		maxRecent: opts.MaxRecent,
		log:       opts.Logger,
		byHash:    map[string]Diagnosis{},
		streams:   map[chan Diagnosis]struct{}{},
	}
	if opts.NATSURL == "" {
		return c, nil
	}
	conn, err := nats.Connect(opts.NATSURL, nats.Name("weft-webui-diagnoses"))
	if err != nil {
		return nil, err
	}
	sub, err := conn.Subscribe(opts.Subject, c.onMsg)
	if err != nil {
		conn.Drain() //nolint:errcheck
		return nil, err
	}
	c.conn = conn
	c.sub = sub
	c.log.Info("diagnoses cache subscribed", "subject", opts.Subject)
	return c, nil
}

// Close drops the NATS subscription + connection AND closes every
// open subscriber stream. Safe to call multiple times.
func (c *Cache) Close() error {
	if c.sub != nil {
		_ = c.sub.Unsubscribe()
		c.sub = nil
	}
	if c.conn != nil {
		c.conn.Drain() //nolint:errcheck
		c.conn = nil
	}
	c.streamMu.Lock()
	for ch := range c.streams {
		close(ch)
		delete(c.streams, ch)
	}
	c.streamMu.Unlock()
	return nil
}

// Add inserts or updates one Diagnosis (dedup by PatternHash). Public
// so tests can drive the cache without NATS ; also called from onMsg.
func (c *Cache) Add(d Diagnosis) {
	if d.PatternHash == "" {
		// Reject malformed records — we'd otherwise overwrite each
		// other under the empty-string key.
		return
	}
	c.mu.Lock()
	c.byHash[d.PatternHash] = d
	// Cap the cache : evict the oldest by LastSeen until we're at
	// maxRecent. Cheap O(N log N) sort per insert is fine at N=100.
	if len(c.byHash) > c.maxRecent {
		c.evictOldest()
	}
	c.mu.Unlock()
	// Notify subscribers OUTSIDE the data lock (don't block one
	// browser's slow read on the next NATS message).
	c.broadcast(d)
}

// Snapshot returns the current cache sorted for display : severity
// descending (critical first), then occurrences descending. Safe to
// call concurrently with Add.
func (c *Cache) Snapshot() []Diagnosis {
	c.mu.RLock()
	out := make([]Diagnosis, 0, len(c.byHash))
	for _, d := range c.byHash {
		out = append(out, d)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		si, sj := severityRank(out[i].Severity), severityRank(out[j].Severity)
		if si != sj {
			return si < sj
		}
		return out[i].Occurrences > out[j].Occurrences
	})
	return out
}

// Subscribe returns a fresh channel that receives every Diagnosis
// passed through Add() from now on. Call Unsubscribe(ch) to free it
// when the consumer is done (typically when the SSE connection
// closes).
//
// Slow consumers : the channel is buffered (16) ; if the buffer
// fills, the broadcast drops messages for THIS subscriber to keep
// up — we never block the NATS dispatcher.
func (c *Cache) Subscribe() <-chan Diagnosis {
	ch := make(chan Diagnosis, 16)
	c.streamMu.Lock()
	c.streams[ch] = struct{}{}
	c.streamMu.Unlock()
	return ch
}

// Unsubscribe removes ch from the broadcast list and closes it.
// Pass the same channel returned by Subscribe.
func (c *Cache) Unsubscribe(ch <-chan Diagnosis) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	for s := range c.streams {
		// Compare by reading the receive end vs the stored send end —
		// channels are pointers under the hood, identity compare via
		// reflect is what we want, but a direct cast works in Go.
		if (<-chan Diagnosis)(s) == ch {
			close(s)
			delete(c.streams, s)
			return
		}
	}
}

// onMsg is the NATS Handler. Decodes the JSON, drops invalid records,
// feeds Add. Errors are logged at Warn — losing one diagnosis isn't
// fatal, the next burst will re-publish.
func (c *Cache) onMsg(msg *nats.Msg) {
	var d Diagnosis
	if err := json.Unmarshal(msg.Data, &d); err != nil {
		c.log.Warn("diagnoses: decode failed", "subject", msg.Subject, "err", err)
		return
	}
	if errors.Is(validate(d), errInvalid) {
		c.log.Warn("diagnoses: invalid record dropped", "subject", msg.Subject, "hash", d.PatternHash)
		return
	}
	c.Add(d)
}

// broadcast fans the diagnosis out to every subscriber, dropping for
// slow consumers (never blocks the producer).
func (c *Cache) broadcast(d Diagnosis) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	for ch := range c.streams {
		select {
		case ch <- d:
		default:
			// Slow consumer ; skip. Their next reconnect picks up the
			// full Snapshot anyway.
		}
	}
}

// evictOldest drops one entry whose LastSeen is the oldest. Called
// under the write lock when the cache exceeds maxRecent. We don't
// keep a heap because at N=100 a linear scan is cheaper than the
// heap maintenance overhead.
func (c *Cache) evictOldest() {
	var (
		oldestKey  string
		oldestTime time.Time
		found      bool
	)
	for k, d := range c.byHash {
		if !found || d.LastSeen.Before(oldestTime) {
			oldestKey = k
			oldestTime = d.LastSeen
			found = true
		}
	}
	if found {
		delete(c.byHash, oldestKey)
	}
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 3
	}
	return 4
}

var errInvalid = errors.New("diagnosis: invalid")

func validate(d Diagnosis) error {
	if d.PatternHash == "" || d.Title == "" {
		return errInvalid
	}
	switch d.Severity {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return nil
	}
	return errInvalid
}
