// auth_keypair.go — HTTP handler for the dev ed25519 keypair fallback.
//
// Mounted at POST /api/auth/keypair when (and only when) the operator
// started weft-webui with --keypair-allowlist <path>. Without the
// flag the route isn't even registered, so a probe gets 404 — that's
// the documented "off" knob.
//
// On a successful POST the handler :
//
//   1. Verifies the JWS body via auth.VerifyAssertion (signature, aud,
//      exp/iat, allowlist).
//   2. Mints a session cookie via the existing session store so the
//      SPA's subsequent /api/* calls authenticate normally.
//   3. Returns {id_token, kind:"keypair", expires_at_unix} to the
//      desktop client so it can cache the bearer in macOS Keychain.
//
// Trust boundary : the allowlist lookup is the single trust decision.
// A well-formed JWS signed by a key that ISN'T in the allowlist gets
// 401 ; the JWS-validity layer alone proves no identity.
package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
)

// keypairResponse mirrors what weft-app-osx's postKeypairAssertion
// parses. Field names are wire-stable.
type keypairResponse struct {
	IDToken       string `json:"id_token"`
	Kind          string `json:"kind"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
}

// keypairHandlerDeps groups everything the handler closure needs.
// Pulled out so registerKeypairAuth stays a thin wiring helper.
type keypairHandlerDeps struct {
	Allowlist     *auth.KeypairAllowlist
	Sessions      *auth.SessionStore
	Audience      string // absolute URL the assertion's `aud` must match
	SessionMaxAge time.Duration
}

// registerKeypairAuth mounts POST /api/auth/keypair on the given mux
// IFF the allowlist is non-nil and holds at least one entry. The
// "two-knob" disable design lives here : the caller (server.New /
// buildHandler) passes Allowlist=nil when no path was configured, and
// this function silently skips the mount.
func registerKeypairAuth(mux *http.ServeMux, deps keypairHandlerDeps) bool {
	if deps.Allowlist == nil || deps.Allowlist.Size() == 0 {
		return false
	}
	if deps.Audience == "" || deps.Sessions == nil {
		return false
	}
	mux.Handle("POST /api/auth/keypair", keypairAuthHandler(deps))
	return true
}

// keypairAuthHandler returns the http.Handler bound to
// POST /api/auth/keypair. Carved out so tests can exercise it without
// spinning up the full server.
func keypairAuthHandler(deps keypairHandlerDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
			return
		}
		jws := strings.TrimSpace(string(body))
		entry, claims, err := auth.VerifyAssertion(jws, deps.Audience, deps.Allowlist)
		if err != nil {
			status := statusForVerifyErr(err)
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		// Mint a session. The "id_token" we hand back to the desktop is
		// the same signed cookie value the SPA would carry — the
		// desktop replays it as a Bearer header, and the auth
		// middleware's CookieFromHeader path reads it. Sharing the
		// session shape keeps the trust chain single.
		groups := []string{"keypair"}
		if entry.IsSuperadmin() {
			groups = append(groups, "admin")
		}
		exp := time.Now().Add(deps.SessionMaxAge)
		if deps.SessionMaxAge <= 0 {
			exp = time.Now().Add(12 * time.Hour)
		}
		payload := &auth.SessionPayload{
			Subject:   "keypair:" + claims.Sub,
			Email:     entry.EntryEmail(),
			Name:      entry.Label,
			Groups:    groups,
			ExpiresAt: exp.Unix(),
		}
		// Generate a fresh "id_token" : it's the encoded cookie value so
		// the desktop can include it verbatim as Bearer. The webui's
		// middleware accepts the encoded session form via
		// Authorization: Bearer ... in addition to the cookie.
		idToken, err := deps.Sessions.Encode(payload)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session encode"})
			return
		}
		// Also drop the cookie so a browser-driven debug invocation
		// (operator pasting the JWS via curl with -c) gets a usable
		// session for follow-up /api/* requests.
		_ = deps.Sessions.Set(w, payload)
		writeJSON(w, http.StatusOK, keypairResponse{
			IDToken:       idToken,
			Kind:          "keypair",
			ExpiresAtUnix: exp.Unix(),
		})
	})
}

// statusForVerifyErr maps the sentinel errors to HTTP codes. Anything
// not enumerated lands at 500 — that's a programming bug, not an
// expected client error.
func statusForVerifyErr(err error) int {
	switch {
	case errors.Is(err, auth.ErrKPMalformed):
		return http.StatusBadRequest
	case errors.Is(err, auth.ErrKPBadSignature),
		errors.Is(err, auth.ErrKPAudienceMismatch),
		errors.Is(err, auth.ErrKPExpired),
		errors.Is(err, auth.ErrKPFutureIat),
		errors.Is(err, auth.ErrKPKeyNotAllowed):
		return http.StatusUnauthorized
	case errors.Is(err, auth.ErrKPAllowlistEmpty):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// nonce returns 16 base64-std bytes, used by tests as a placeholder
// nonce. Not used by the handler itself ; kept here so the test file
// stays decoupled from the verifier's internals.
func nonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.StdEncoding.EncodeToString(b[:])
}

// keypairAudienceFor builds the canonical `aud` value the verifier
// requires. Pinned to <publicBase>/api/auth/keypair so an attacker who
// captures an assertion can't replay it against a sibling endpoint.
// publicBase typically comes from the listener's public URL (the same
// value used to seed OIDC redirects).
func keypairAudienceFor(publicBase string) string {
	return strings.TrimRight(publicBase, "/") + "/api/auth/keypair"
}

// keypairResponse is JSON-encoded inline ; the marshalable shape is
// the same as the struct definition (no custom MarshalJSON needed).
var _ = json.Marshal // keep encoding/json in the import set
