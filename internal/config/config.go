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
	"time"
)

// Config holds the resolved runtime settings. Values are normalised
// here ; downstream packages should treat the zero value as "feature
// off" (e.g. SSHSocket="" means no SSH transport).
type Config struct {
	// HTTP — two listeners by design : a public user-UI port and an
	// admin port that should only be exposed on a trusted interface
	// (typically a WireGuard endpoint). Set AdminAddr to "" to disable
	// the admin listener entirely.
	UserAddr  string
	AdminAddr string
	TLSCert   string
	TLSKey    string

	// Weft daemon (empty = mock mode, only allowed in DevMode)
	WeftSocket string

	// Optional weft-network controller socket. Same convention as
	// WeftSocket (unix path or ssh://). When empty the webui falls
	// back to mock data for Routers / LBs / DNS / Scheduling Rules.
	WeftNetworkSocket string

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

	// PolicyStrict flips the bucket-policy evaluator's default from
	// "no matching statement = allow" (today's permissive default) to
	// "no matching statement = deny" (AWS-aligned default-deny when a
	// policy exists at all). Off by default so existing deployments
	// don't lose access on upgrade ; flip when an operator is ready
	// for the stricter model.
	PolicyStrict bool

	// AuditLogPath is the JSONL file where admin-classified actions
	// (microvm start/stop/delete, volume create/delete, floating-ip
	// allocate/map, security-group rule changes, …) are persisted.
	// Empty = audit disabled (events drop through audit.NopLogger).
	AuditLogPath string

	// InventoryPath is the JSON file weft-webui rehydrates AZ / Rack /
	// Host rows from at startup and writes back after every CRUD. Empty
	// = in-memory only (seed survives restart, operator changes don't).
	// Eventually swaps to etcd via weft-network — keep the contract
	// loose (one JSON blob, atomic write) so the migration is a drop-in.
	InventoryPath string

	// DNSPath, SecurityPath, ScriptsPath — same shape as InventoryPath,
	// each guarding the mock-layer state of one resource family. Empty
	// = no persistence (current dev default). Set independently so
	// operators can stage which files survive a restart.
	DNSPath      string
	SecurityPath string
	ScriptsPath  string

	// MaxRequestBodyBytes is the http.MaxBytesReader cap applied to
	// every /api/* request body. Default 1 MiB. Raise for endpoints
	// that legitimately accept large payloads (script bodies, SBOM
	// uploads) ; lower in container deployments that want a tighter
	// DoS profile. Zero / negative disables the wrap entirely.
	MaxRequestBodyBytes int64

	// ShutdownTimeout is the deadline http.Server.Shutdown gets after
	// SIGTERM. The server first cancels its BaseContext (so SSE +
	// WatchEvents handlers exit immediately), then waits up to this
	// timeout for remaining synchronous /api/* handlers to finish.
	// Default 10s ; raise for long-running admin endpoints, lower in
	// container fleets that prefer fast restarts.
	ShutdownTimeout time.Duration

	// AllowedOrigins is the cross-origin allow-list consulted by the
	// withOriginCheck middleware for mutating /api/* requests. Same-
	// origin (Host header) is always permitted ; entries here add
	// additional scheme://host[:port] tuples (no trailing slash, no
	// path). Useful for terraform-provider-weft or a separate ops
	// dashboard hitting the API from a known IP/hostname.
	AllowedOrigins []string

	// AuditRotateBytes is the rotation threshold for AuditLogPath. The
	// current file is renamed to <path>.<RFC3339> and a fresh one is
	// opened when the next write would exceed this limit. Default 100MB.
	AuditRotateBytes int64
}

const (
	defaultUserAddr     = ":8080"
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
		// WEBUI_LISTEN_ADDR is the legacy single-listener variable ; it
		// still works as the user-port default so existing deployments
		// don't break. New variable is WEBUI_USER_ADDR.
		UserAddr:  firstNonEmpty(os.Getenv("WEBUI_USER_ADDR"), os.Getenv("WEBUI_LISTEN_ADDR"), defaultUserAddr),
		AdminAddr: os.Getenv("WEBUI_ADMIN_ADDR"),
		TLSCert:   os.Getenv("WEBUI_TLS_CERT"),
		TLSKey:    os.Getenv("WEBUI_TLS_KEY"),
		WeftSocket:        os.Getenv("WEBUI_WEFT_SOCKET"),
		WeftNetworkSocket: os.Getenv("WEBUI_WEFT_NETWORK_SOCKET"),
		OIDCIssuer:    os.Getenv("WEBUI_OIDC_ISSUER"),
		OIDCClientID:  os.Getenv("WEBUI_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("WEBUI_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:  os.Getenv("WEBUI_OIDC_REDIRECT_URL"),
		CookieDomain:  os.Getenv("WEBUI_COOKIE_DOMAIN"),
		CookieName:    envOr("WEBUI_COOKIE_NAME", defaultCookieName),
		PublicURL:     os.Getenv("WEBUI_PUBLIC_URL"),
		DevMode:       envBool("WEBUI_DEV_MODE", false),
		TrustProxies:  envBool("WEBUI_TRUST_PROXIES", false),
		PolicyStrict:  envBool("WEBUI_POLICY_STRICT", false),
		AuditLogPath:    os.Getenv("WEBUI_AUDIT_LOG_PATH"),
		InventoryPath:   os.Getenv("WEBUI_INVENTORY_PATH"),
		DNSPath:         os.Getenv("WEBUI_DNS_PATH"),
		SecurityPath:    os.Getenv("WEBUI_SECURITY_PATH"),
		ScriptsPath:     os.Getenv("WEBUI_SCRIPTS_PATH"),
		// 100 MiB default ; flag/env can lower (or raise) it. Loaded
		// later from WEBUI_AUDIT_ROTATE_BYTES if set.
		AuditRotateBytes: 100 << 20,
	}
	if v := os.Getenv("WEBUI_AUDIT_ROTATE_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("WEBUI_AUDIT_ROTATE_BYTES: %w", err)
		}
		cfg.AuditRotateBytes = n
	}

	cfg.OIDCScopes = splitCSV(envOr("WEBUI_OIDC_SCOPES", "openid,email,profile,groups"))
	cfg.AllowedOrigins = splitCSV(os.Getenv("WEBUI_ALLOWED_ORIGINS"))

	if v := os.Getenv("WEBUI_MAX_REQUEST_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("WEBUI_MAX_REQUEST_BODY_BYTES: %w", err)
		}
		cfg.MaxRequestBodyBytes = n
	} else {
		cfg.MaxRequestBodyBytes = 1 << 20 // 1 MiB
	}

	if v := os.Getenv("WEBUI_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("WEBUI_SHUTDOWN_TIMEOUT: %w", err)
		}
		cfg.ShutdownTimeout = d
	} else {
		cfg.ShutdownTimeout = 10 * time.Second
	}

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
	flagSet.StringVar(&cfg.UserAddr, "addr", cfg.UserAddr, "user-UI listen address (public)")
	flagSet.StringVar(&cfg.AdminAddr, "admin-addr", cfg.AdminAddr, "admin-UI listen address (bind to a WireGuard interface ; empty disables the admin port)")
	flagSet.StringVar(&cfg.WeftSocket, "weft-socket", cfg.WeftSocket, "weft daemon socket (unix path or ssh://) ; empty = mock mode (dev only)")
	flagSet.StringVar(&cfg.WeftNetworkSocket, "weft-network-socket", cfg.WeftNetworkSocket, "weft-network controller socket ; empty = mock data for routers/LBs/DNS/scheduling-rules")
	flagSet.BoolVar(&cfg.DevMode, "dev", cfg.DevMode, "dev mode : disables auth, allows mock fallback")
	flagSet.BoolVar(&cfg.PolicyStrict, "policy-strict", cfg.PolicyStrict, "bucket policies default-deny when a policy exists (AWS-aligned ; off = today's permissive default)")
	flagSet.StringVar(&cfg.AuthMode, "auth-mode", cfg.AuthMode, `"oidc" or "none" ("none" is dev-only)`)
	flagSet.StringVar(&cfg.PublicURL, "public-url", cfg.PublicURL, "external base URL (used to compute the OIDC redirect when not set explicitly)")
	flagSet.StringVar(&cfg.AuditLogPath, "audit-log-path", cfg.AuditLogPath, "JSONL file for the admin audit log ; empty = disabled")
	flagSet.StringVar(&cfg.InventoryPath, "inventory-path", cfg.InventoryPath, "JSON file the AZ/Rack/Host inventory is rehydrated from at startup + written back after every CRUD ; empty = in-memory only")
	flagSet.StringVar(&cfg.DNSPath, "dns-path", cfg.DNSPath, "JSON file the mock dns-zones + dns-records rows are rehydrated from + flushed back to ; empty = in-memory only")
	flagSet.StringVar(&cfg.SecurityPath, "security-path", cfg.SecurityPath, "JSON file the mock security-groups + rules map are rehydrated from + flushed back to ; empty = in-memory only")
	flagSet.StringVar(&cfg.ScriptsPath, "scripts-path", cfg.ScriptsPath, "JSON file the mock scripts catalogue is rehydrated from + flushed back to ; empty = in-memory only")
	flagSet.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", cfg.ShutdownTimeout, "max time the server spends draining in-flight requests on SIGTERM before the deadline forces exit")
	flagSet.Int64Var(&cfg.MaxRequestBodyBytes, "max-request-body-bytes", cfg.MaxRequestBodyBytes, "cap on /api/* request body size in bytes (DoS guard) ; 0 = disabled")
	flagSet.Int64Var(&cfg.AuditRotateBytes, "audit-rotate-bytes", cfg.AuditRotateBytes, "rotate the audit log when the next write would exceed this size (bytes)")
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
	fmt.Fprintf(&b, "weft-webui mode=%s auth=%s user=%s",
		modeLabel(c.DevMode), c.AuthMode, c.UserAddr)
	if c.AdminAddr != "" {
		fmt.Fprintf(&b, " admin=%s", c.AdminAddr)
	} else {
		b.WriteString(" admin=disabled")
	}
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

// firstNonEmpty returns the first non-empty string ; useful for layered
// env defaults (new var → legacy var → static default).
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
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
