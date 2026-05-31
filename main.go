// Command weft-webui serves the Weft web dashboard : a Horizon-style
// UI for the platform's object types (tenants, projects, users,
// networks, security groups, volumes, shares, hosts, …). One Go
// binary serves the JSON API and the embedded SvelteJS single-page
// app from two listeners :
//
//   - user-UI  (WEBUI_USER_ADDR,  default :8080) — public, OIDC,
//                                                  project-scoped views
//   - admin-UI (WEBUI_ADMIN_ADDR, default empty) — bind to a WireGuard
//                                                  interface ; surfaces
//                                                  cluster-wide resources
//                                                  (Hosts, Users, Tenants)
//                                                  and /metrics
//
// Setting WEBUI_ADMIN_ADDR to "" disables the admin port. Bind it on a
// loopback or WireGuard address — never 0.0.0.0 in prod.
//
// Two modes :
//
//   - prod (default)            OIDC auth, signed-cookie sessions,
//                               --weft-socket required
//   - dev  (WEBUI_DEV_MODE=true) no auth, mock data fallback, insecure
//                               cookies, dev banner printed to stderr
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/config"
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
		logger.Info("audit log ready", "path", cfg.AuditLogPath, "rotate_bytes", cfg.AuditRotateBytes)
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
		DevMode:      cfg.DevMode,
		PolicyStrict: cfg.PolicyStrict,
	}

	// Boot announcement before opening the listeners so the journal is
	// readable even if a bind fails immediately.
	logger.Info("weft-webui starting",
		"version", version, "user", cfg.UserAddr,
		"admin", labelOr(cfg.AdminAddr, "disabled"),
		"weft", labelOr(cfg.WeftSocket, "mock"),
		"dev", cfg.DevMode, "auth", cfg.AuthMode,
	)
	os.Stderr.WriteString(cfg.Banner() + "\n")

	// Run both listeners with shared shutdown. A failure on one tears
	// down the other so an operator never has a half-up daemon.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	userSrv := newHTTPServer(cfg.UserAddr, server.New(deps))
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := listenAndServe(userSrv, cfg); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	var adminSrv *http.Server
	if cfg.AdminAddr != "" {
		adminSrv = newHTTPServer(cfg.AdminAddr, server.NewAdmin(deps))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := listenAndServe(adminSrv, cfg); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}()
	}

	select {
	case err := <-errs:
		logger.Error("listener crashed — shutting down", "err", err)
	case <-ctx.Done():
		logger.Info("shutdown signal", "signal", ctx.Err())
	}

	// Shut everything down within a single deadline so an unresponsive
	// connection on one port can't drag the other past the timeout.
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := userSrv.Shutdown(shutCtx); err != nil {
		logger.Warn("user shutdown", "err", err)
	}
	if adminSrv != nil {
		if err := adminSrv.Shutdown(shutCtx); err != nil {
			logger.Warn("admin shutdown", "err", err)
		}
	}
	wg.Wait()
	logger.Info("weft-webui stopped")
	return nil
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

func listenAndServe(s *http.Server, cfg *config.Config) error {
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
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
