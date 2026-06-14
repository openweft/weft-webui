// Command weft-webui serves the Weft web dashboard : a Horizon-style
// UI for the platform's object types (tenants, projects, users,
// networks, security groups, volumes, shares, hosts, …). One Go
// binary serves the JSON API and the embedded SvelteJS single-page
// app from up to three listeners (the three-portal split) :
//
//   - user-portal   (--addr,        default :8080) — public Internet,
//                                                    own-scope only
//   - tenant-portal (--tenant-addr, default empty) — tenant VLAN ;
//                                                    tenant-admin +
//                                                    regular users
//   - infra-portal  (--infra-addr,  default empty) — WireGuard mesh
//                                                    only ; cluster-
//                                                    wide ops, plugins,
//                                                    federation,
//                                                    /metrics
//
// Each listener exposes a DIFFERENT set of registered endpoints (see
// internal/server/portals.go). A user who hits :8080 cannot reach
// /api/hosts even by crafting a URL — the handler isn't registered on
// that mux.
//
// Backward compatibility : when neither --tenant-addr nor --infra-addr
// is set, the binary boots in legacy single-listener mode — UserAddr
// serves the full surface (everything the infra portal would expose).
// This keeps `task run` / `go run .` working as before.
//
// Legacy --admin-addr is an alias for --tenant-addr ; a deprecation
// notice fires when only the legacy name is set.
//
// Two operating modes :
//
//   - prod (default)            OIDC auth, signed-cookie sessions,
//                               --weft-socket required
//   - dev  (WEBUI_DEV_MODE=true) no auth, mock data fallback, insecure
//                               cookies, dev banner printed to stderr
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/config"
	"github.com/openweft/weft-webui/internal/diagnoses"
	"github.com/openweft/weft-webui/internal/etcdsource"
	"github.com/openweft/weft-webui/internal/ratelimit"
	"github.com/openweft/weft-webui/internal/server"
	"github.com/openweft/weft-webui/internal/telemetry"
	"github.com/openweft/weft-webui/internal/wclient"
)

//go:embed all:web/dist
var webDist embed.FS

// version is overridable via -ldflags "-X main.version=..." at build
// time ; surfaces as weft_webui_build_info.
var version = "dev"

func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString("weft-webui: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(flag.CommandLine)
	if err != nil {
		return err
	}
	flag.Parse()
	if err := cfg.Validate(); err != nil {
		return err
	}

	static, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		return err
	}

	// Telemetry registry first so the wclient + handlers can record
	// into it from the start.
	metrics := telemetry.New(version)

	// Live gRPC client (optional in dev, mandatory in prod).
	var live *wclient.Client
	if cfg.WeftSocket != "" {
		live = wclient.New(cfg.WeftSocket)
		live.Metrics = metrics
		defer live.Close()
	}
	// Sibling weft-network controller : optional everywhere. When
	// unset, the resources owned by it (routers, LBs, DNS, scheduling
	// rules) fall back to the in-memory mock store.
	var liveNet *wclient.NetworkClient
	if cfg.WeftNetworkSocket != "" {
		liveNet = wclient.NewNetwork(cfg.WeftNetworkSocket)
		liveNet.Metrics = metrics
		defer liveNet.Close()
	}

	mw, oidcAuth, err := buildAuth(logger, cfg)
	if err != nil {
		return err
	}
	if oidcAuth != nil {
		oidcAuth.OnLogin = metrics.Login
	}

	// Persistent audit log : opt-in via --audit-log-path. When unset
	// the server falls back to audit.NopLogger so handlers never branch
	// on "is audit on ?".
	var auditLog audit.Logger
	if cfg.AuditLogPath != "" {
		fl, err := audit.NewFileLogger(cfg.AuditLogPath, cfg.AuditRotateBytes)
		if err != nil {
			return err
		}
		defer fl.Close()
		auditLog = fl
		// Hand the live FileLogger to the /api/audit-log read endpoint
		// so the dashboard can tail recent entries.
		server.SetAuditTailer(fl)
		logger.Info("audit log ready", "path", cfg.AuditLogPath, "rotate_bytes", cfg.AuditRotateBytes)
	}

	// Bridge OIDC auth-lifecycle events into the audit log so a
	// brute-force probe / replayed-state-cookie attempt / nonce
	// mismatch leaves a persistent trail. Falls back to NopLogger
	// when --audit-log-path is unset (no-op, never nil-deref'd).
	if oidcAuth != nil {
		ll := auditLog
		if ll == nil {
			ll = audit.NopLogger{}
		}
		oidcAuth.OnAuthEvent = func(action, result, reason, remoteIP, subject string) {
			ev := audit.Event{
				Timestamp:    time.Now().UTC(),
				Action:       "auth." + action,
				ResourceKind: "session",
				Subject:      subject,
				Result:       result,
				RemoteIP:     remoteIP,
			}
			if reason != "" {
				ev.Extra = map[string]string{"reason": reason}
				ev.ErrorMessage = reason
			}
			ll.Log(context.Background(), ev)
		}
	}

	// State-file history rotation : every successful flush of a
	// tracked state file (inventory / dns / security / scripts) is
	// pre-archived under <path>.history/<RFC3339Nano>.json so an
	// operator can roll back a fat-fingered delete. Arm before the
	// SetXxxPath calls below so the very first mutation already
	// rotates.
	if cfg.StateHistoryKeep > 0 {
		server.SetStateHistoryKeep(cfg.StateHistoryKeep)
		logger.Info("state history armed", "keep", cfg.StateHistoryKeep)
	}

	// Inventory persistence : opt-in via --inventory-path. Empty path
	// keeps the seed-only behaviour ; when set, the server rehydrates
	// AZ / Rack / Host rows from the JSON file at boot and flushes
	// them back after every CRUD.
	if cfg.InventoryPath != "" {
		server.SetInventoryPath(cfg.InventoryPath)
		logger.Info("inventory persistence ready", "path", cfg.InventoryPath)
	}
	if cfg.DNSPath != "" {
		server.SetDNSPath(cfg.DNSPath)
		logger.Info("dns persistence ready", "path", cfg.DNSPath)
	}
	if cfg.SecurityPath != "" {
		server.SetSecurityPath(cfg.SecurityPath)
		logger.Info("security persistence ready", "path", cfg.SecurityPath)
	}
	if cfg.ScriptsPath != "" {
		server.SetScriptsPath(cfg.ScriptsPath)
		logger.Info("scripts persistence ready", "path", cfg.ScriptsPath)
	}

	// Rate limiter : per-user (session.Subject) or per-IP token bucket
	// in front of /api/*. Defaults documented in the package — 100rps
	// burst 50 per authenticated user, 20rps burst 10 per anonymous IP.
	// Reuses cfg.TrustProxies (already governs X-Forwarded-Proto for
	// OIDC redirect URLs) — same operational decision : "am I behind
	// a trusted proxy that owns the XFF header ?".
	rl := ratelimit.NewLimiter(ratelimit.Options{
		TrustForwardedFor: cfg.TrustProxies,
	})
	defer rl.Stop()

	// Dev keypair fallback : opt-in via --keypair-allowlist. When the
	// flag is empty the verifier stays nil and the handler isn't
	// mounted ; a missing or unparseable file is a hard error so a
	// fat-fingered config can't accidentally leave the endpoint half-
	// up. We compute the audience from cfg.PublicURL (the same value
	// the OIDC redirect uses) so the verifier can reject a captured
	// assertion replayed against a sibling endpoint.
	var keypairAllow *auth.KeypairAllowlist
	keypairAudience := ""
	if cfg.KeypairAllowlistPath != "" {
		a, err := auth.LoadKeypairAllowlist(cfg.KeypairAllowlistPath)
		if err != nil {
			return err
		}
		keypairAllow = a
		base := strings.TrimRight(cfg.PublicURL, "/")
		if base == "" {
			base = "http://" + firstNonEmpty(cfg.UserAddr, ":8080")
		}
		keypairAudience = base + "/api/auth/keypair"
		logger.Warn("keypair fallback enabled — DEV ONLY",
			"entries", a.Size(),
			"endpoint", "/api/auth/keypair",
			"audience", keypairAudience)
	}

	// Reuse the OIDC session store for the keypair handler so the
	// mint-and-cookie path is identical to the OIDC one (one signing
	// key, one cookie format). In dev mode (AuthMode=none) there's no
	// real store, so we synthesise a throwaway one — the keypair
	// fallback owns its own short-lived bearer in that case.
	var keypairSessions *auth.SessionStore
	if keypairAllow != nil {
		if cfg.AuthMode == "oidc" && len(cfg.SessionKey) > 0 {
			keypairSessions = auth.NewSessionStore(cfg.SessionKey, cfg.CookieName, cfg.CookieDomain, cfg.CookieSecure, cfg.SessionMaxAge)
		} else {
			// Dev mode : mint a per-process random key so the
			// keypair-cookie still signs something stable for the run.
			keypairSessions = auth.NewSessionStore(devKeypairKey(), cfg.CookieName, cfg.CookieDomain, false, cfg.SessionMaxAge)
		}
	}

	// Diagnoses cache : in-process subscriber to weft.diagnosis.> on
	// NATS, fed by the weft-doctor pipeline. Empty WEFT_NATS_URL =
	// offline mode (panel renders, list stays empty). A dial failure
	// is logged but doesn't abort startup ; the operator can boot the
	// webui before NATS is reachable, and the panel comes online on
	// next reconnect via the SSE EventSource auto-reconnect.
	var diagCache *diagnoses.Cache
	if natsURL := os.Getenv("WEFT_NATS_URL"); natsURL != "" {
		c, err := diagnoses.NewCache(diagnoses.Options{
			NATSURL: natsURL,
			Logger:  logger,
		})
		if err != nil {
			logger.Warn("diagnoses cache offline — NATS dial failed", "err", err)
		} else {
			diagCache = c
			defer c.Close()
		}
	} else {
		logger.Info("diagnoses cache offline — WEFT_NATS_URL unset")
	}

	// Monitors panel source : dial etcd if endpoints were configured.
	// Failure is non-fatal — the /api/monitors endpoint degrades to
	// "monitors offline" so the rest of the dashboard stays usable
	// even when the etcd quorum is mid-roll. The expected_count
	// override applies regardless of whether the dial succeeded, so
	// an operator can pin a baseline even in detached preview mode.
	if len(cfg.EtcdEndpoints) > 0 {
		src, err := etcdsource.Open(etcdsource.Options{
			Endpoints:   cfg.EtcdEndpoints,
			Prefix:      cfg.EtcdMonitorsPrefix,
			DialTimeout: 5 * time.Second,
		})
		if err != nil {
			logger.Warn("monitors source offline — etcd dial failed", "err", err, "endpoints", cfg.EtcdEndpoints)
		} else {
			server.SetMonitorsSource(src)
			defer src.Close()
			logger.Info("monitors source ready", "endpoints", cfg.EtcdEndpoints)
		}
	} else {
		logger.Info("monitors source offline — WEFT_ETCD_ENDPOINTS unset")
	}
	if cfg.ExpectedMonitors > 0 {
		server.SetExpectedMonitorsOverride(cfg.ExpectedMonitors)
		logger.Info("monitors expected count pinned", "expected", cfg.ExpectedMonitors)
	}

	deps := server.Deps{
		Logger:       logger,
		Static:       static,
		Live:         live,
		LiveNet:      liveNet,
		Auth:         mw,
		OIDC:         oidcAuth,
		Metrics:      metrics,
		Audit:        auditLog,
		RateLimit:    rl,
		DevMode:             cfg.DevMode,
		AllowedOrigins:      cfg.AllowedOrigins,
		MaxRequestBodyBytes: cfg.MaxRequestBodyBytes,
		PolicyStrict: cfg.PolicyStrict,
		Version:      version,
		KeypairAllowlist:        keypairAllow,
		KeypairAudience:         keypairAudience,
		SessionStoreForKeypair:  keypairSessions,
		SessionMaxAgeForKeypair: time.Duration(cfg.SessionMaxAge) * time.Second,
		DiagnosesCache:          diagCache,
	}

	// Fold the legacy --admin-addr into --tenant-addr (deprecation
	// path) so the rest of the boot logic only sees the new flag set.
	if cfg.ResolveAdminAlias() {
		logger.Warn("--admin-addr / WEBUI_ADMIN_ADDR is deprecated — use --tenant-addr / WEBUI_TENANT_ADDR",
			"admin_addr", cfg.AdminAddr, "tenant_addr", cfg.TenantAddr)
	}

	// Boot announcement before opening the listeners so the journal is
	// readable even if a bind fails immediately.
	logger.Info("weft-webui starting",
		"version", version,
		"user", cfg.UserAddr,
		"tenant", labelOr(cfg.TenantAddr, "disabled"),
		"infra", labelOr(cfg.InfraAddr, "disabled"),
		"weft", labelOr(cfg.WeftSocket, "mock"),
		"dev", cfg.DevMode, "auth", cfg.AuthMode,
		"legacy_single_listener", cfg.LegacySingleListener(),
	)
	os.Stderr.WriteString(cfg.Banner() + "\n")

	// Run every configured listener with shared shutdown. A failure on
	// one tears down the others so an operator never has a half-up
	// daemon.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// serverCtx is the parent context every request inherits via
	// BaseContext. Cancelling it after SIGTERM wakes up long-lived
	// handlers (SSE streams) that select on r.Context().Done() so
	// Shutdown's grace deadline is spent waiting for synchronous
	// /api/* handlers, not idle SSE keepalives. Without this, the
	// grace deadline always runs out to the full timeout.
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	// Build the set of (addr, handler, label) tuples to spin up. The
	// legacy single-listener mode wires the full surface on UserAddr ;
	// the three-portal mode wires each portal independently.
	type portalSpec struct {
		label string
		addr  string
		h     http.Handler
	}
	var portals []portalSpec
	if cfg.LegacySingleListener() {
		portals = append(portals, portalSpec{
			label: "legacy",
			addr:  cfg.UserAddr,
			h:     server.NewPortal(deps, server.PortalLegacy),
		})
	} else {
		portals = append(portals, portalSpec{
			label: "user",
			addr:  cfg.UserAddr,
			h:     server.NewPortal(deps, server.PortalUser),
		})
		if cfg.TenantAddr != "" {
			portals = append(portals, portalSpec{
				label: "tenant",
				addr:  cfg.TenantAddr,
				h:     server.NewPortal(deps, server.PortalTenant),
			})
		}
		if cfg.InfraAddr != "" {
			portals = append(portals, portalSpec{
				label: "infra",
				addr:  cfg.InfraAddr,
				h:     server.NewPortal(deps, server.PortalInfra),
			})
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(portals))
	servers := make([]*http.Server, 0, len(portals))
	for _, p := range portals {
		srv := newHTTPServer(p.addr, p.h, serverCtx)
		servers = append(servers, srv)
		label := p.label
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := listenAndServe(srv, cfg); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
			logger.Info("portal listener stopped", "portal", label, "addr", srv.Addr)
		}()
		logger.Info("portal listener up", "portal", label, "addr", p.addr)
	}

	select {
	case err := <-errs:
		logger.Error("listener crashed — shutting down", "err", err)
	case <-ctx.Done():
		logger.Info("shutdown signal", "signal", ctx.Err())
	}

	// Two-phase shutdown :
	//   1) Cancel serverCtx — SSE + WatchEvents loops exit immediately
	//      because their handler ctx is derived from it.
	//   2) http.Server.Shutdown — stops accepting + waits for the
	//      remaining synchronous /api/* handlers under a single deadline
	//      so an unresponsive conn on one port can't drag the others
	//      past the timeout.
	cancelServer()
	timeout := cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for i, srv := range servers {
		if err := srv.Shutdown(shutCtx); err != nil {
			logger.Warn("portal shutdown", "portal", portals[i].label, "err", err)
		}
	}
	wg.Wait()
	logger.Info("weft-webui stopped")
	return nil
}

func newHTTPServer(addr string, h http.Handler, baseCtx context.Context) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// BaseContext returns the parent context for every connection
		// the listener accepts. When serverCtx is cancelled, every
		// in-flight handler's r.Context() unblocks — that's how SSE
		// streams know to exit promptly on SIGTERM.
		BaseContext: func(_ net.Listener) context.Context { return baseCtx },
	}
}

func listenAndServe(s *http.Server, cfg *config.Config) error {
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		// Apply the strict TLS config (MinVersion + curated cipher
		// suites + pinned curves) before serving so any TLS 1.0/1.1
		// client fails the handshake. Done here rather than in
		// newHTTPServer so the non-TLS branch isn't affected.
		s.TLSConfig = cfg.StrictTLSConfig()
		return s.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
	}
	return s.ListenAndServe()
}

func labelOr(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}

// firstNonEmpty returns the first non-empty string ; tiny helper used
// when synthesising a keypair-fallback audience without a configured
// PublicURL.
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// devKeypairKey returns a per-process random 32-byte HMAC key used to
// sign the keypair fallback's "id_token" when the webui is running in
// dev mode (no real session key). The same key powers the Set-Cookie
// header on the response so the in-browser flow stays consistent for
// the lifetime of the binary. Restarting the binary invalidates every
// previously-issued bearer — fine for dev.
func devKeypairKey() []byte {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		// rand.Read can't fail outside catastrophic environments ; a
		// zero key would still work but with no entropy. Surface as a
		// panic so the operator notices the boot anomaly.
		panic("devKeypairKey: rand.Read failed : " + err.Error())
	}
	return k[:]
}

// buildAuth instantiates the session store, the OIDC provider (prod)
// and the middleware that ties them together.
func buildAuth(logger *slog.Logger, cfg *config.Config) (*auth.Middleware, *auth.OIDC, error) {
	if cfg.AuthMode == "none" {
		return &auth.Middleware{
			Mode: auth.ModeNone,
			MockUser: auth.User{
				Subject: "dev-user",
				Email:   "dev@weft.local",
				Name:    "dev",
				Groups:  []string{"admin", "dev"},
			},
		}, nil, nil
	}

	sessions := auth.NewSessionStore(cfg.SessionKey, cfg.CookieName, cfg.CookieDomain, cfg.CookieSecure, cfg.SessionMaxAge)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := auth.NewOIDC(ctx, auth.OIDCConfig{
		Issuer:       cfg.OIDCIssuer,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.RedirectURL(),
		Scopes:       cfg.OIDCScopes,
	}, sessions)
	if err != nil {
		return nil, nil, err
	}
	logger.Info("oidc ready", "issuer", cfg.OIDCIssuer, "redirect", cfg.RedirectURL())

	return &auth.Middleware{
		Mode:      auth.ModeOIDC,
		Sessions:  sessions,
		Refresher: o,
		Logger:    logger,
	}, o, nil
}
