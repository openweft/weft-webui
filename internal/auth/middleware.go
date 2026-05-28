// middleware.go — request-level wrapping for auth + a couple of pure
// HTTP-handler helpers (/api/me, /api/session/project).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

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

	// MockUser is what gets injected when Mode == ModeNone.
	MockUser User
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

		// Dev mode short-circuit : every API call sees the same user.
		if m.Mode == ModeNone {
			user := m.MockUser
			user.DevMode = true
			r = r.WithContext(WithUser(r.Context(), &user))
			next.ServeHTTP(w, r)
			return
		}

		p, err := m.Sessions.Read(r)
		if err != nil {
			writeAuthErr(w, err)
			return
		}
		user := payloadToUser(p)
		r = r.WithContext(WithUser(r.Context(), user))
		next.ServeHTTP(w, r)
	})
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

// SetProjectHandler updates the session's selected project. The SPA
// posts {"project": "..."} ; we re-mint the cookie with the new field.
//
// In dev mode there's no cookie to re-mint, so we just store the
// project on the synthesised user — the next request gets the default
// again. The SPA caches the choice client-side, which is enough.
func (m *Middleware) SetProjectHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
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
	u.Project = strings.TrimSpace(body.Project)

	if m.Mode == ModeNone {
		writeJSON(w, http.StatusOK, map[string]string{"project": u.Project})
		return
	}
	if err := m.Sessions.Set(w, userToPayload(u)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"project": u.Project})
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
