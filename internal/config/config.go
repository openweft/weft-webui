// Package config loads weft-webui's runtime configuration from
// environment variables, with flag overrides for a few common knobs
// (listen address, weft socket, dev mode). Env-first keeps the binary
// friendly to systemd / Kubernetes / Nomad deployment without a config
// file; flags stay handy for one-off `go run .` invocations.
//
// Two operating modes are supported :
//
//   - dev   (WEBUI_DEV_MODE=true)  — no auth, mock fallback allowed,
//     insecure cookies. Prints a banner to stderr so it's obvious.
//   - prod  (default)              — OIDC required, signed-cookie
//     sessions on a strong key, --weft-socket required.
//
// Validate() is strict in prod : missing OIDC issuer / client ID /
// redirect URL / session key is a hard error. A misconfigured prod
// deployment fails loud at boot rather than silently letting requests
// through unauthenticated.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds the resolved runtime settings. Values are normalised
// here ; downstream packages should treat the zero value as "feature
// off" (e.g. SSHSocket="" means no SSH transport).
type Config struct {
	// HTTP
	ListenAddr string
	TLSCert    string
	TLSKey     string

	// Weft daemon (empty = mock mode, only allowed in DevMode)
	WeftSocket string

	// Auth mode : "oidc" (default in prod) or "none" (dev only).
	AuthMode string

	// OIDC
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	OIDCScopes       []string

	// Session
	SessionKey    []byte // decoded HMAC key, ≥ 32 bytes in prod
	CookieDomain  string
	CookieSecure  bool
	CookieName    string
	SessionMaxAge int // seconds ; default 12h

	// Misc
	DevMode      bool
	PublicURL    string // used to build absolute redirect URLs when OIDCRedirectURL is empty
	TrustProxies bool   // honour X-Forwarded-Proto when building redirects
}

const (
	defaultListenAddr   = ":8080"
	defaultAuthModeProd = "oidc"
	defaultAuthModeDev  = "none"
	defaultCookieName   = "weft_webui_session"
	defaultMaxAge       = 12 * 3600 // 12h
)

// Load reads the configuration from environment variables, then
// applies the optional command-line flags as overrides. Returns the
// merged Config without validating it ; call Validate before use.
//
// flagSet is the *flag.FlagSet to register the supported overrides on.
// Pass flag.CommandLine for the standard CLI ; tests can pass a
// throwaway set. Returns the unparsed args function so the caller can
// trigger parsing at the right moment (after registering its own
// flags).
func Load(flagSet *flag.FlagSet) (*Config, error) {
	cfg := &Config{
		ListenAddr:    envOr("WEBUI_LISTEN_ADDR", defaultListenAddr),
		TLSCert:       os.Getenv("WEBUI_TLS_CERT"),
		TLSKey:        os.Getenv("WEBUI_TLS_KEY"),
		WeftSocket:    os.Getenv("WEBUI_WEFT_SOCKET"),
		OIDCIssuer:    os.Getenv("WEBUI_OIDC_ISSUER"),
		OIDCClientID:  os.Getenv("WEBUI_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("WEBUI_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:  os.Getenv("WEBUI_OIDC_REDIRECT_URL"),
		CookieDomain:  os.Getenv("WEBUI_COOKIE_DOMAIN"),
		CookieName:    envOr("WEBUI_COOKIE_NAME", defaultCookieName),
		PublicURL:     os.Getenv("WEBUI_PUBLIC_URL"),
		DevMode:       envBool("WEBUI_DEV_MODE", false),
		TrustProxies:  envBool("WEBUI_TRUST_PROXIES", false),
	}

	cfg.OIDCScopes = splitCSV(envOr("WEBUI_OIDC_SCOPES", "openid,email,profile,groups"))

	// CookieSecure defaults : true unless dev mode, env override wins.
	if v, ok := os.LookupEnv("WEBUI_COOKIE_SECURE"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("WEBUI_COOKIE_SECURE: %w", err)
		}
		cfg.CookieSecure = b
	} else {
		cfg.CookieSecure = !cfg.DevMode
	}

	if v := os.Getenv("WEBUI_SESSION_MAX_AGE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("WEBUI_SESSION_MAX_AGE: %w", err)
		}
		cfg.SessionMaxAge = n
	} else {
		cfg.SessionMaxAge = defaultMaxAge
	}

	// Session key : hex or base64-encoded. ≥ 32 raw bytes required in prod.
	if v := os.Getenv("WEBUI_SESSION_KEY"); v != "" {
		key, err := decodeKey(v)
		if err != nil {
			return nil, fmt.Errorf("WEBUI_SESSION_KEY: %w", err)
		}
		cfg.SessionKey = key
	}

	// Auth mode : explicit env wins ; otherwise default per mode.
	if v := os.Getenv("WEBUI_AUTH_MODE"); v != "" {
		cfg.AuthMode = v
	} else if cfg.DevMode {
		cfg.AuthMode = defaultAuthModeDev
	} else {
		cfg.AuthMode = defaultAuthModeProd
	}

	// Flags override env. Defaults track whatever env produced so that
	// passing --addr alone (without env) still gives the expected value.
	flagSet.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "listen address")
	flagSet.StringVar(&cfg.WeftSocket, "weft-socket", cfg.WeftSocket, "weft daemon socket (unix path or ssh://) ; empty = mock mode (dev only)")
	flagSet.BoolVar(&cfg.DevMode, "dev", cfg.DevMode, "dev mode : disables auth, allows mock fallback")
	flagSet.StringVar(&cfg.AuthMode, "auth-mode", cfg.AuthMode, `"oidc" or "none" ("none" is dev-only)`)
	flagSet.StringVar(&cfg.PublicURL, "public-url", cfg.PublicURL, "external base URL (used to compute the OIDC redirect when not set explicitly)")
	return cfg, nil
}

// Validate is the production sanity check. It refuses to start when
// auth is enabled but OIDC is half-configured, when the session key
// would let an attacker forge cookies, or when a non-dev deployment
// would silently serve mock data.
func (c *Config) Validate() error {
	if c.AuthMode != "" && c.AuthMode != "oidc" && c.AuthMode != "none" {
		return fmt.Errorf("auth-mode must be oidc or none, got %q", c.AuthMode)
	}
	if c.AuthMode == "none" && !c.DevMode {
		return errors.New("auth-mode=none is only allowed with WEBUI_DEV_MODE=true")
	}
	if !c.DevMode && c.WeftSocket == "" {
		return errors.New("WEBUI_WEFT_SOCKET (or --weft-socket) is required outside dev mode ; mock data must not be served in production")
	}
	if c.AuthMode == "oidc" {
		if c.OIDCIssuer == "" {
			return errors.New("WEBUI_OIDC_ISSUER is required when auth-mode=oidc")
		}
		if c.OIDCClientID == "" {
			return errors.New("WEBUI_OIDC_CLIENT_ID is required when auth-mode=oidc")
		}
		// Client secret may be empty for public clients (PKCE), but
		// confidential clients (dex default) need it. Warn-don't-fail
		// here is fine — the OIDC exchange will fail loudly anyway.
		if c.resolveRedirectURL() == "" {
			return errors.New("WEBUI_OIDC_REDIRECT_URL or WEBUI_PUBLIC_URL is required when auth-mode=oidc")
		}
		if _, err := url.Parse(c.resolveRedirectURL()); err != nil {
			return fmt.Errorf("OIDC redirect URL is not a valid URL: %w", err)
		}
		if len(c.SessionKey) < 32 {
			return errors.New("WEBUI_SESSION_KEY must be ≥ 32 bytes when auth-mode=oidc (hex or base64)")
		}
	}
	if c.TLSCert != "" && c.TLSKey == "" || c.TLSKey != "" && c.TLSCert == "" {
		return errors.New("WEBUI_TLS_CERT and WEBUI_TLS_KEY must be set together")
	}
	return nil
}

// RedirectURL returns the resolved OIDC redirect URL — either the
// explicit OIDCRedirectURL or PublicURL + /api/auth/callback.
func (c *Config) RedirectURL() string { return c.resolveRedirectURL() }

func (c *Config) resolveRedirectURL() string {
	if c.OIDCRedirectURL != "" {
		return c.OIDCRedirectURL
	}
	if c.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(c.PublicURL, "/") + "/api/auth/callback"
}

// Banner returns a short multi-line description suitable for the
// startup log. Useful so the operator sees at a glance which mode is
// active.
func (c *Config) Banner() string {
	var b strings.Builder
	fmt.Fprintf(&b, "weft-webui mode=%s auth=%s", modeLabel(c.DevMode), c.AuthMode)
	if c.WeftSocket == "" {
		b.WriteString(" weft=mock")
	} else {
		fmt.Fprintf(&b, " weft=%s", c.WeftSocket)
	}
	if c.DevMode {
		b.WriteString("  ⚠ DEV MODE — do not expose to untrusted networks")
	}
	return b.String()
}

func modeLabel(dev bool) string {
	if dev {
		return "dev"
	}
	return "prod"
}

func envOr(key, dflt string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return dflt
}

func envBool(key string, dflt bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return dflt
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return dflt
	}
	return b
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// decodeKey accepts hex or base64 (std or URL-safe). 64 hex chars or a
// well-formed base64 string both decode to a 32-byte key.
func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, errors.New("not valid hex or base64")
}
