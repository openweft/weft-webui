// keypair.go — server-side validator for the dev ed25519 keypair
// fallback flow.
//
// The matching client side lives in
// github.com/openweft/weft-app-core/auth (SignAssertion / Assertion).
// We do NOT depend on that module here ; the JWS schema is locked
// (header={"alg":"EdDSA","typ":"JWT"} ; payload={iss, sub, aud, iat,
// exp, nonce}) so a tiny pure-stdlib verifier keeps the server side
// independent of the desktop module set.
//
// Trust model : the JWS is signature-verified using the pubkey embedded
// in the payload's `sub` claim. That signature check alone proves
// nothing about identity — it just confirms the JWS is internally
// consistent. The Allowlist holds the actual trust decision : a key
// not present there returns ErrKPKeyNotAllowed regardless of how
// well-formed the assertion is.
//
// This endpoint is opt-in twice : the client must enable
// auth.keypair_fallback in app.json AND weft-webui must be started
// with --keypair-allowlist <path>. The handler isn't even registered
// on the mux when the path is empty (see server/auth_keypair.go).
package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// AssertionClaims mirrors the payload baked into the JWS on the client
// side. Field names match the keys produced by
// github.com/openweft/weft-app-core/auth.SignAssertion exactly.
type AssertionClaims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Aud   string `json:"aud"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
	Nonce string `json:"nonce"`
}

// KeypairAllowlistEntry is one entry of the --keypair-allowlist JSON
// file. Pubkey holds the base64-std-encoded 32-byte ed25519 public key
// (the exact form `weft-app-osx --print-pubkey` prints to stdout).
// Role drives the synthesised session : "superadmin" maps to a
// cluster-admin User, "tenant-admin" / "user" map to scoped sessions
// via tenantMembership. Label is a free-form description (e.g.
// "alice-laptop") used in audit logs.
type KeypairAllowlistEntry struct {
	Pubkey string `json:"pubkey"`
	Role   string `json:"role"`
	Label  string `json:"label,omitempty"`
	// Email is the synthetic "user" the session reports. When empty we
	// fall back to label@keypair.local so the audit log still has a
	// stable identifier per device.
	Email string `json:"email,omitempty"`
}

// KeypairAllowlistFile is the on-disk shape of the --keypair-allowlist
// JSON. A leading "_comment" key is conventional but ignored by the
// loader.
type KeypairAllowlistFile struct {
	Comment string                  `json:"_comment,omitempty"`
	Entries []KeypairAllowlistEntry `json:"entries"`
}

// KeypairAllowlist is the in-memory lookup table the verifier consults
// after a successful signature check. Concurrent-safe : Load swaps the
// internal map atomically so a SIGHUP-style reload (future work) won't
// race with in-flight verifications.
type KeypairAllowlist struct {
	mu      sync.RWMutex
	byKey   map[string]KeypairAllowlistEntry // base64-std pubkey -> entry
	loadErr error
}

// LoadKeypairAllowlist reads and parses the JSON file at path. Returns
// a non-nil *KeypairAllowlist even on error — callers can present the
// loader error via Err() while the verifier returns ErrKPAllowlistEmpty
// for every request. An empty entries list is treated like a missing
// file at handler level (no surface).
func LoadKeypairAllowlist(path string) (*KeypairAllowlist, error) {
	a := &KeypairAllowlist{byKey: map[string]KeypairAllowlistEntry{}}
	if path == "" {
		a.loadErr = errors.New("keypair: allowlist path is empty")
		return a, a.loadErr
	}
	b, err := os.ReadFile(path)
	if err != nil {
		a.loadErr = fmt.Errorf("keypair: read allowlist: %w", err)
		return a, a.loadErr
	}
	var f KeypairAllowlistFile
	if err := json.Unmarshal(b, &f); err != nil {
		a.loadErr = fmt.Errorf("keypair: parse allowlist: %w", err)
		return a, a.loadErr
	}
	for i, e := range f.Entries {
		if e.Pubkey == "" {
			a.loadErr = fmt.Errorf("keypair: allowlist entry %d has empty pubkey", i)
			return a, a.loadErr
		}
		if e.Role == "" {
			a.loadErr = fmt.Errorf("keypair: allowlist entry %d (%s) has empty role", i, e.Label)
			return a, a.loadErr
		}
		// Validate the pubkey is a well-formed 32-byte ed25519 key so a
		// fat-fingered allowlist fails at startup, not at first request.
		if _, err := decodePubKey(e.Pubkey); err != nil {
			a.loadErr = fmt.Errorf("keypair: allowlist entry %d (%s) : %w", i, e.Label, err)
			return a, a.loadErr
		}
		a.byKey[e.Pubkey] = e
	}
	return a, nil
}

// Err returns the error encountered the last time the allowlist was
// loaded ; nil when the file was parsed cleanly.
func (a *KeypairAllowlist) Err() error {
	if a == nil {
		return errors.New("keypair: nil allowlist")
	}
	return a.loadErr
}

// Lookup returns the entry for the given base64-std pubkey, or
// ErrKPKeyNotAllowed when the key isn't on the list.
func (a *KeypairAllowlist) Lookup(pubkey string) (KeypairAllowlistEntry, error) {
	if a == nil {
		return KeypairAllowlistEntry{}, ErrKPAllowlistEmpty
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.byKey) == 0 {
		return KeypairAllowlistEntry{}, ErrKPAllowlistEmpty
	}
	e, ok := a.byKey[pubkey]
	if !ok {
		return KeypairAllowlistEntry{}, ErrKPKeyNotAllowed
	}
	return e, nil
}

// Size returns the number of entries currently loaded. Useful for the
// startup log line.
func (a *KeypairAllowlist) Size() int {
	if a == nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.byKey)
}

// MaxClockSkew matches the client side : up to 30s of clock skew is
// tolerated on the iat / exp checks.
const MaxClockSkew = 30 * time.Second

// Sentinel errors the handler maps to HTTP status codes. Prefixed with
// "ErrKP" so they don't collide with session.go's ErrExpired /
// ErrBadSignature which carry the cookie-layer semantics.
//
//   - ErrKPMalformed         400 (bad bytes / structure / claims)
//   - ErrKPBadSignature      401 (sig fails verification)
//   - ErrKPAudienceMismatch  401 (aud doesn't match the endpoint URL)
//   - ErrKPExpired           401 (exp passed)
//   - ErrKPFutureIat         401 (iat too far in the future)
//   - ErrKPKeyNotAllowed     401 (sig OK but pubkey not in allowlist)
//   - ErrKPAllowlistEmpty    503 (file loaded but no entries — handler
//                            should not even be registered in this
//                            case ; surfaced for completeness)
var (
	ErrKPMalformed        = errors.New("keypair: malformed assertion")
	ErrKPBadSignature     = errors.New("keypair: bad signature")
	ErrKPAudienceMismatch = errors.New("keypair: audience mismatch")
	ErrKPExpired          = errors.New("keypair: assertion expired")
	ErrKPFutureIat        = errors.New("keypair: iat in the future beyond skew")
	ErrKPKeyNotAllowed    = errors.New("keypair: pubkey not on allowlist")
	ErrKPAllowlistEmpty   = errors.New("keypair: allowlist empty")
)

// VerifyAssertion validates the JWS' bytes (structure, alg, signature,
// audience, time bounds) AND checks the pubkey embedded in the
// signature against the allowlist. Returns the matched allowlist
// entry + the parsed claims so the handler can mint a session.
func VerifyAssertion(jws string, allowedAudience string, list *KeypairAllowlist) (KeypairAllowlistEntry, AssertionClaims, error) {
	if allowedAudience == "" {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}
	if hdr.Alg != "EdDSA" {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}
	var claims AssertionClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}
	if len(sig) != ed25519.SignatureSize {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}

	pub, err := decodePubKey(claims.Sub)
	if err != nil {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPMalformed
	}

	signingInput := parts[0] + "." + parts[1]
	if !ed25519.Verify(pub, []byte(signingInput), sig) {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPBadSignature
	}

	if claims.Aud != allowedAudience {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPAudienceMismatch
	}

	now := time.Now().UTC()
	if claims.Exp <= 0 || time.Unix(claims.Exp, 0).Before(now) {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPExpired
	}
	if claims.Iat > 0 && time.Unix(claims.Iat, 0).After(now.Add(MaxClockSkew)) {
		return KeypairAllowlistEntry{}, AssertionClaims{}, ErrKPFutureIat
	}

	// Trust check : the JWS is well-formed and signed by the key
	// embedded in claims.Sub, but until we look that key up in the
	// allowlist we have no authorisation.
	entry, err := list.Lookup(claims.Sub)
	if err != nil {
		return KeypairAllowlistEntry{}, AssertionClaims{}, err
	}
	return entry, claims, nil
}

// decodePubKey is the server-side parallel of
// weft-app-core/auth.DecodePubKey. Kept private so external callers
// route through the verifier instead of the raw key parser.
func decodePubKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode pubkey: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("pubkey length = %d, want %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// EntryEmail returns the synthetic email the session reports for this
// entry. Fall-back is "<label>@keypair.local" so the audit log carries
// a stable identifier even when the operator skipped the email field.
func (e KeypairAllowlistEntry) EntryEmail() string {
	if e.Email != "" {
		return e.Email
	}
	if e.Label != "" {
		return e.Label + "@keypair.local"
	}
	return "anonymous@keypair.local"
}

// IsSuperadmin reports whether the role string grants cluster-admin
// privileges. Treat the comparison case-insensitively so an operator
// who writes "Superadmin" doesn't accidentally lock themselves out.
func (e KeypairAllowlistEntry) IsSuperadmin() bool {
	r := strings.ToLower(strings.TrimSpace(e.Role))
	return r == "superadmin" || r == "admin"
}
