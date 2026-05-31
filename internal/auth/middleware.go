// middleware.go — request-level wrapping for auth + a couple of pure
// HTTP-handler helpers (/api/me, /api/session/project).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Refresher is the slice of OIDC behaviour the middleware needs to swap
// expiring access tokens transparently. Kept narrow so tests can inject
// a fake without standing up a real provider. *OIDC satisfies this.
type Refresher interface {
	RefreshSession(ctx context.Context, p *SessionPayload) (*SessionPayload, error)
}

// Mode is what callers pass to Middleware to pick a policy. None is
// dev-only and short-circuits to a fixed synthetic user.
type Mode int

const (
	ModeOIDC Mode = iota
	ModeNone
)

// Middleware injects the authenticated User into the request context
// for protected routes. Anything under /api/ that is NOT in the
// publicPaths set requires a valid session ; the SPA itself, healthz,
// and the /api/auth/* routes pass through.
//
// On 401 we deliberately return JSON (not a redirect) for /api/*
// requests so that the SPA's fetch helper can handle it ; the SPA then
// triggers the redirect to /api/auth/login itself.
type Middleware struct {
	Mode     Mode
	Sessions *SessionStore

	// Refresher, when non-nil, gets called on each authenticated
	// request whose access token is within refreshLeeway of expiring.
	// Production wires this to *OIDC ; tests can inject a stub.
	// Leaving it nil disables refresh — the session simply expires as
	// before.
	Refresher Refresher

	// Logger receives a single warn line when a refresh attempt fails ;
	// the request then proceeds as if no refresh was tried (the session
	// is left intact, but its access token is about to expire — the
	// next request will get a 401). Optional ; nil falls back to the
	// default slog logger.
	Logger *slog.Logger

	// MockUser is what gets injected when Mode == ModeNone.
	MockUser User

	// devScope is the in-memory persistence layer for the cascading
	// topbar selection in dev mode (where there's no signed cookie to
	// carry it). Process-global on purpose — one dev session per
	// running binary. Mutex protects the assignments since SetScope
	// runs on a request goroutine while Wrap reads on another.
	devScopeMu      sync.RWMutex
	devTenant       string
	devProject      string
}

// publicPath returns true when an /api/ path is allowed without auth.
func publicPath(p string) bool {
	switch p {
	case "/api/healthz", "/api/readyz":
		return true
	}
	return strings.HasPrefix(p, "/api/auth/")
}

// Wrap returns the middleware chain.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static + SPA assets : no auth needed (the SPA itself checks).
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Dev mode short-circuit : every API call sees the same user,
		// overlaid with whatever scope the SPA has set via
		// /api/session/scope (kept in m.devTenant / m.devProject so
		// it survives the request boundary even without a cookie).
		if m.Mode == ModeNone {
			user := m.MockUser
			user.DevMode = true
			m.devScopeMu.RLock()
			user.Tenant, user.Project = m.devTenant, m.devProject
			m.devScopeMu.RUnlock()
			// Dev impersonation : ?as_user=alice@... [&as_groups=g1,g2]
			// lets the smoke harness exercise non-admin code paths
			// without wiring a real OIDC IdP. Honoured only in ModeNone
			// — production never sees this branch. Omitting as_groups
			// clears the synthetic user's groups so the impersonated
			// identity is non-admin by default (the common case worth
			// testing).
			if as := r.URL.Query().Get("as_user"); as != "" {
				user.Subject = as
				user.Email = as
				user.Name = as
				if g := r.URL.Query().Get("as_groups"); g != "" {
					user.Groups = strings.Split(g, ",")
				} else {
					user.Groups = nil
				}
			}
			r = r.WithContext(WithUser(r.Context(), &user))
			next.ServeHTTP(w, r)
			return
		}

		p, err := m.Sessions.Read(r)
		if err != nil {
			writeAuthErr(w, err)
			return
		}
		// Transparent refresh : if the access token is about to expire
		// and we have a refresh token + a Refresher, swap them in
		// before handing off. Failure is logged and we fall through to
		// the existing session — the next request will get a 401 once
		// it actually expires, which then redirects to /api/auth/login.
		if m.Refresher != nil && p.RefreshToken != "" && p.NeedsRefresh(time.Now()) {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			np, rerr := m.Refresher.RefreshSession(ctx, p)
			cancel()
			if rerr == nil {
				if serr := m.Sessions.Set(w, np); serr == nil {
					p = np
				} else {
					m.log().Warn("session re-encode after refresh failed", "err", serr)
				}
			} else if !errors.Is(rerr, ErrNoRefreshToken) {
				m.log().Warn("oidc refresh failed", "err", rerr, "sub", p.Subject)
			}
		}
		user := payloadToUser(p)
		r = r.WithContext(WithUser(r.Context(), user))
		next.ServeHTTP(w, r)
	})
}

// log returns the configured logger or the slog default so callers
// never have to nil-check.
func (m *Middleware) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// MeHandler exposes the current user as JSON. Returns 401 if there is
// none ; the SPA uses this to populate the Topbar.
func (m *Middleware) MeHandler(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sub":      u.Subject,
		"email":    u.Email,
		"name":     u.Name,
		"groups":   u.Groups,
		"project":  u.Project,
		"initials": u.Initials(),
		"dev":      u.DevMode,
	})
}

// SetScopeHandler updates the session's selected (tenant, project)
// pair. The SPA posts {"tenant": "...", "project": "..."} ; we re-mint
// the cookie with the new fields.
//
// Either field can be omitted to clear it. An empty tenant means
// "cluster-wide" (cluster admin only — server-side enforced
// elsewhere) ; an empty project means "all projects of the selected
// tenant" (tenant-aggregate view).
//
// In dev mode there's no persistent cookie ; we still mutate the
// in-context user so the rest of the request sees the new scope, but
// the next request resets to the synthesised default. The SPA caches
// the choice client-side, which is enough.
func (m *Middleware) SetScopeHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tenant  string `json:"tenant"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	u := UserFromContext(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no session"})
		return
	}
	u.Tenant = strings.TrimSpace(body.Tenant)
	u.Project = strings.TrimSpace(body.Project)

	scope := map[string]string{"tenant": u.Tenant, "project": u.Project}
	if m.Mode == ModeNone {
		// Stash on the middleware so the next request reads it back.
		m.devScopeMu.Lock()
		m.devTenant, m.devProject = u.Tenant, u.Project
		m.devScopeMu.Unlock()
		writeJSON(w, http.StatusOK, scope)
		return
	}
	if err := m.Sessions.Set(w, userToPayload(u)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, scope)
}

func writeAuthErr(w http.ResponseWriter, err error) {
	code := http.StatusUnauthorized
	msg := "auth required"
	switch {
	case errors.Is(err, ErrNoSession):
		msg = "no session"
	case errors.Is(err, ErrExpired):
		msg = "session expired"
	case errors.Is(err, ErrBadSignature):
		msg = "bad session"
		code = http.StatusUnauthorized
	}
	w.Header().Set("WWW-Authenticate", `Session realm="weft-webui"`)
	writeJSON(w, code, map[string]string{"error": msg, "login": "/api/auth/login"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HMAC helpers shared with the OIDC state cookie.

func signHex(key, raw []byte) (string, error) {
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func verifyHex(key, raw []byte, sigHex string) bool {
	want, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return hmac.Equal(mac.Sum(nil), want)
}
