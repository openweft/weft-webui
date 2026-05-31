// Package ratelimit provides a token-bucket HTTP middleware for the
// weft-webui API surface.
//
// Why per-(session|IP) instead of a global bucket : a single chatty
// SPA tab must never starve the rest of the userbase. The expensive
// operations we actually want to throttle are :
//
//   - POST /api/networking/floating-ips/allocate  (cluster-wide IPAM
//     contention)
//   - POST /api/microvms                          (full provisioning
//     pipeline ; touches imagestore, scheduler, hypervisor driver)
//   - POST /api/volumes, /api/scripts             (object-storage roundtrips)
//
// Default rates are picked so that a well-behaved SPA never hits them
// — a UI that polls /api/events + a handful of GETs is far below 20
// rps, let alone 100 — while a script that floods /api/microvms gets
// firmly held back. The burst lets short clicky flows through (e.g.
// "create 5 VMs in a row from the wizard").
//
// Identity key (in order of preference) :
//
//  1. authenticated session subject (auth.UserFromContext → User.Subject)
//  2. X-Forwarded-For left-most IP  — only when TrustForwardedFor=true
//  3. RemoteAddr (host part)
//
// Per-key state is held in a sync.Map of *rate.Limiter ; idle entries
// are reaped lazily by a background sweeper so memory stays bounded
// without a per-request mutex contention point.
package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/openweft/weft-webui/internal/auth"
)

// Default rates. Picked from the rationale documented at the top of
// the file ; the constants are exported so tests + callers can refer
// to them by name.
const (
	DefaultUserRPS    = 100
	DefaultUserBurst  = 50
	DefaultAnonRPS    = 20
	DefaultAnonBurst  = 10
	defaultIdleReap   = 10 * time.Minute
	defaultSweepEvery = 1 * time.Minute
)

// Options configures a Limiter. Zero values fall back to the
// Default* constants ; callers typically set only what they want to
// override.
type Options struct {
	// UserRPS / UserBurst : per-session token bucket (key = User.Subject).
	UserRPS   float64
	UserBurst int

	// AnonRPS / AnonBurst : per-IP token bucket (key derived from
	// RemoteAddr / X-Forwarded-For).
	AnonRPS   float64
	AnonBurst int

	// TrustForwardedFor : when true, the left-most IP in
	// X-Forwarded-For is honoured as the client address. Leave false
	// unless the listener really is behind a reverse proxy that sets
	// the header — otherwise a client can spoof their key by sending
	// the header themselves.
	TrustForwardedFor bool

	// Now is the clock injector. Defaults to time.Now ; tests override
	// it indirectly through rate.Limiter's own time-based methods.
	Now func() time.Time

	// IdleReap controls how long an unused bucket stays in the map
	// before being evicted. 0 → defaultIdleReap.
	IdleReap time.Duration
}

// Limiter is the HTTP middleware. Safe for concurrent use ; one
// instance is meant to wrap the API handler chain.
type Limiter struct {
	userRPS    float64
	userBurst  int
	anonRPS    float64
	anonBurst  int
	trustXFF   bool
	now        func() time.Time
	idleReap   time.Duration

	buckets sync.Map // key string → *bucket

	// stopOnce / stopCh allow Stop() to be idempotent.
	stopOnce sync.Once
	stopCh   chan struct{}
}

// bucket pairs the rate.Limiter with a last-seen timestamp (stored as
// unix-nanos in an atomic so the sweep + hot path don't contend).
type bucket struct {
	lim      *rate.Limiter
	lastSeen atomic.Int64
}

// NewLimiter builds a Limiter from opts and starts the idle-bucket
// sweeper. Call Stop() at shutdown to release the goroutine ; in
// test/short-lived scenarios it's also fine to leak — the goroutine
// holds no resources beyond the Limiter itself.
func NewLimiter(opts Options) *Limiter {
	l := &Limiter{
		userRPS:   opts.UserRPS,
		userBurst: opts.UserBurst,
		anonRPS:   opts.AnonRPS,
		anonBurst: opts.AnonBurst,
		trustXFF:  opts.TrustForwardedFor,
		now:       opts.Now,
		idleReap:  opts.IdleReap,
		stopCh:    make(chan struct{}),
	}
	if l.userRPS <= 0 {
		l.userRPS = DefaultUserRPS
	}
	if l.userBurst <= 0 {
		l.userBurst = DefaultUserBurst
	}
	if l.anonRPS <= 0 {
		l.anonRPS = DefaultAnonRPS
	}
	if l.anonBurst <= 0 {
		l.anonBurst = DefaultAnonBurst
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.idleReap <= 0 {
		l.idleReap = defaultIdleReap
	}
	go l.sweep()
	return l
}

// Stop terminates the background sweeper. Idempotent.
func (l *Limiter) Stop() {
	l.stopOnce.Do(func() { close(l.stopCh) })
}

// Middleware returns an http.Handler that enforces the configured
// per-key rate. On deny it writes 429 with Retry-After and a small
// JSON body the SPA can render. On allow it forwards to next.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, isUser := l.keyFor(r)
		b := l.bucketFor(key, isUser)
		now := l.now()
		b.lastSeen.Store(now.UnixNano())

		res := b.lim.ReserveN(now, 1)
		if !res.OK() {
			// Burst exceeds the limiter's capacity — practically
			// unreachable with N=1 but the API mandates handling.
			writeDenied(w, time.Second)
			return
		}
		if delay := res.DelayFrom(now); delay > 0 {
			res.Cancel()
			writeDenied(w, delay)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bucketFor returns (or lazily creates) the bucket for key.
func (l *Limiter) bucketFor(key string, isUser bool) *bucket {
	if v, ok := l.buckets.Load(key); ok {
		return v.(*bucket)
	}
	var rps float64
	var burst int
	if isUser {
		rps, burst = l.userRPS, l.userBurst
	} else {
		rps, burst = l.anonRPS, l.anonBurst
	}
	nb := &bucket{lim: rate.NewLimiter(rate.Limit(rps), burst)}
	nb.lastSeen.Store(l.now().UnixNano())
	actual, _ := l.buckets.LoadOrStore(key, nb)
	return actual.(*bucket)
}

// keyFor extracts the rate-limit key for r. Returns (key, isUser).
// User keys are prefixed "u:" and IP keys "ip:" so a subject "1.2.3.4"
// cannot accidentally collide with the IP-keyed bucket of the same
// literal value.
func (l *Limiter) keyFor(r *http.Request) (string, bool) {
	if u := auth.UserFromContext(r.Context()); u != nil && u.Subject != "" {
		return "u:" + u.Subject, true
	}
	return "ip:" + clientIP(r, l.trustXFF), false
}

// clientIP returns the IP the request appears to come from. Honours
// X-Forwarded-For only when trustXFF is true (otherwise a client can
// spoof their key by sending the header). Falls back to the host part
// of RemoteAddr — and to RemoteAddr verbatim when SplitHostPort fails
// (e.g. unit tests that pass "test-client").
func clientIP(r *http.Request, trustXFF bool) string {
	if trustXFF {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Left-most entry = original client (per RFC 7239 §5.2).
			if comma := strings.IndexByte(xff, ','); comma >= 0 {
				xff = xff[:comma]
			}
			if ip := strings.TrimSpace(xff); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// writeDenied serialises a 429. Retry-After is in seconds per
// RFC 7231 §7.1.3 (we round up so a 200ms delay still surfaces as 1s
// rather than 0).
func writeDenied(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(retryAfter / time.Second)
	if retryAfter%time.Second > 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	fmt.Fprintf(w, `{"error":"rate limit exceeded","retry_after_seconds":%d}`+"\n", secs)
}

// sweep evicts buckets that have been idle for longer than idleReap.
// Runs on a coarse ticker (1m) — exactness doesn't matter, the goal
// is bounded memory under a long-tail of one-shot IPs.
func (l *Limiter) sweep() {
	t := time.NewTicker(defaultSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-t.C:
			cutoff := l.now().Add(-l.idleReap).UnixNano()
			l.buckets.Range(func(k, v any) bool {
				if v.(*bucket).lastSeen.Load() < cutoff {
					l.buckets.Delete(k)
				}
				return true
			})
		}
	}
}
