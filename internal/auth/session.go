// session.go — signed-cookie session implementation.
//
// Format (after base64.RawURLEncoding):
//
//	"<json-payload>.<hex-hmac-sha256(json-payload)>"
//
// HMAC is computed over the raw JSON bytes (not the base64 form), so
// we can keep the payload self-describing. Cookies expire client-side
// via MaxAge AND server-side via the embedded ExpiresAt — both checks
// run on every request, the stricter one wins.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// SessionPayload is the JSON blob baked into the cookie. Stays small ;
// the gRPC token is the heaviest field.
//
// Tenant + Project together form the user's "scope". Both can be empty :
//   - tenant="" project=""  → cluster-wide view (cluster admin only)
//   - tenant="acme" project="" → tenant-aggregate view (all projects of acme)
//   - tenant="acme" project="team-alpha" → project-scoped view
type SessionPayload struct {
	Subject      string   `json:"sub"`
	Email        string   `json:"email,omitempty"`
	Name         string   `json:"name,omitempty"`
	Groups       []string `json:"groups,omitempty"`
	Tenant       string   `json:"tenant,omitempty"`
	Project      string   `json:"project,omitempty"`
	AccessToken  string   `json:"at,omitempty"`
	IDToken      string   `json:"it,omitempty"`
	RefreshToken string   `json:"rt,omitempty"`
	ExpiresAt    int64    `json:"exp"` // unix seconds
}

// SessionStore wraps an HMAC key + cookie configuration. Safe for
// concurrent use — Encode / Decode are pure.
type SessionStore struct {
	Key      []byte
	Name     string
	Domain   string
	Secure   bool
	MaxAge   int // seconds
	SameSite http.SameSite
	Path     string
}

// NewSessionStore builds a store from raw config. Path defaults to "/" ;
// SameSite defaults to Lax so the OIDC redirect from the IdP carries
// the cookie back.
func NewSessionStore(key []byte, name, domain string, secure bool, maxAge int) *SessionStore {
	if name == "" {
		name = "weft_webui_session"
	}
	return &SessionStore{
		Key:      key,
		Name:     name,
		Domain:   domain,
		Secure:   secure,
		MaxAge:   maxAge,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	}
}

// Encode signs the payload and returns the cookie value (NOT yet a
// cookie header — Set issues the cookie).
func (s *SessionStore) Encode(p *SessionPayload) (string, error) {
	if len(s.Key) == 0 {
		return "", errors.New("session: empty signing key")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.Key)
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded + "." + sig, nil
}

// Decode verifies the HMAC, parses the JSON, and rejects expired
// payloads. Returns nil + ErrExpired / ErrBadSignature distinctly so
// the middleware can decide whether to redirect (expired = re-login)
// or 401 (forged).
func (s *SessionStore) Decode(value string) (*SessionPayload, error) {
	if len(s.Key) == 0 {
		return nil, errors.New("session: empty signing key")
	}
	dot := strings.LastIndexByte(value, '.')
	if dot <= 0 || dot == len(value)-1 {
		return nil, ErrBadSignature
	}
	encoded, sig := value[:dot], value[dot+1:]
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrBadSignature
	}
	wantSig, err := hex.DecodeString(sig)
	if err != nil {
		return nil, ErrBadSignature
	}
	mac := hmac.New(sha256.New, s.Key)
	mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), wantSig) {
		return nil, ErrBadSignature
	}
	var p SessionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ErrBadSignature
	}
	if p.ExpiresAt > 0 && time.Now().Unix() > p.ExpiresAt {
		return &p, ErrExpired
	}
	return &p, nil
}

// Set issues the Set-Cookie header. Pass an empty payload + MaxAge=-1
// via Clear to log out.
func (s *SessionStore) Set(w http.ResponseWriter, p *SessionPayload) error {
	v, err := s.Encode(p)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.Name,
		Value:    v,
		Path:     s.Path,
		Domain:   s.Domain,
		MaxAge:   s.MaxAge,
		Secure:   s.Secure,
		HttpOnly: true,
		SameSite: s.SameSite,
	})
	return nil
}

// Clear unsets the cookie (Max-Age=-1 instructs the browser to drop it).
func (s *SessionStore) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.Name,
		Value:    "",
		Path:     s.Path,
		Domain:   s.Domain,
		MaxAge:   -1,
		Secure:   s.Secure,
		HttpOnly: true,
		SameSite: s.SameSite,
	})
}

// Read parses the request's cookie. Missing = (nil, ErrNoSession).
func (s *SessionStore) Read(r *http.Request) (*SessionPayload, error) {
	c, err := r.Cookie(s.Name)
	if err != nil {
		return nil, ErrNoSession
	}
	return s.Decode(c.Value)
}

// Sentinels for the middleware to switch on.
var (
	ErrNoSession    = errors.New("auth: no session cookie")
	ErrExpired      = errors.New("auth: session expired")
	ErrBadSignature = errors.New("auth: bad signature")
)

// payloadToUser projects the cookie blob into the User shape exposed
// to handlers / wclient. Kept here so session.go owns the
// serialisation boundary.
func payloadToUser(p *SessionPayload) *User {
	return &User{
		Subject:     p.Subject,
		Email:       p.Email,
		Name:        p.Name,
		Groups:      p.Groups,
		Tenant:      p.Tenant,
		Project:     p.Project,
		AccessToken: p.AccessToken,
		IDToken:     p.IDToken,
		Refresh:     p.RefreshToken,
		ExpiresAt:   p.ExpiresAt,
	}
}

func userToPayload(u *User) *SessionPayload {
	return &SessionPayload{
		Subject:      u.Subject,
		Email:        u.Email,
		Name:         u.Name,
		Groups:       u.Groups,
		Tenant:       u.Tenant,
		Project:      u.Project,
		AccessToken:  u.AccessToken,
		IDToken:      u.IDToken,
		RefreshToken: u.Refresh,
		ExpiresAt:    u.ExpiresAt,
	}
}
