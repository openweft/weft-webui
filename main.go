// Command weft-webui serves the Weft web dashboard : a Horizon-style
// UI for the platform's object types (tenants, projects, users,
// networks, security groups, volumes, shares, hosts, …). One Go
// binary serves a small JSON API and the embedded SvelteJS single-page
// app.
//
// Configuration is env-first (WEBUI_*) with a handful of CLI flag
// overrides ; see internal/config for the full list. Two modes :
//
//   - prod (default)            OIDC auth, signed-cookie sessions,
//                               --weft-socket required
//   - dev  (WEBUI_DEV_MODE=true) no auth, mock data fallback, insecure
//                               cookies, banner printed to stderr
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
	"syscall"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/config"
	"github.com/openweft/weft-webui/internal/server"
	"github.com/openweft/weft-webui/internal/wclient"
)

//go:embed all:web/dist
var webDist embed.FS

func main() {
	if err := run(); err != nil {
		// slog already logged the structured form ; print a one-liner
		// for terminals that don't render JSON well, then exit 1.
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

	// Live gRPC client : optional in dev, mandatory in prod (Validate
	// already enforced this).
	var live *wclient.Client
	if cfg.WeftSocket != "" {
		live = wclient.New(cfg.WeftSocket)
		defer live.Close()
	}

	// Auth assembly : signed-cookie sessions + OIDC provider (prod) or
	// a synthetic dev user (dev).
	mw, oidcAuth, err := buildAuth(logger, cfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: server.New(server.Deps{
			Logger:  logger,
			Static:  static,
			Live:    live,
			Auth:    mw,
			OIDC:    oidcAuth,
			DevMode: cfg.DevMode,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// Boot announcement : one structured line + the human banner so the
	// operator can spot dev mode at a glance in the journal.
	logger.Info("weft-webui starting",
		"addr", cfg.ListenAddr, "dev", cfg.DevMode, "auth", cfg.AuthMode,
		"weft", weftLabel(cfg.WeftSocket),
	)
	os.Stderr.WriteString(cfg.Banner() + "\n")

	serveErr := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			serveErr <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			serveErr <- srv.ListenAndServe()
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutdown signal", "signal", ctx.Err())
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Warn("shutdown", "err", err)
	}
	logger.Info("weft-webui stopped")
	return nil
}

func weftLabel(s string) string {
	if s == "" {
		return "mock"
	}
	return s
}

// buildAuth instantiates the session store, the OIDC provider (in prod)
// and the middleware that ties them together. Centralised so main()
// stays the wiring diagram, not the policy.
func buildAuth(logger *slog.Logger, cfg *config.Config) (*auth.Middleware, *auth.OIDC, error) {
	if cfg.AuthMode == "none" {
		// Dev synthetic user — matches what /api/me will return.
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

	// Discovery against the IdP. Bounded so a slow or down dex doesn't
	// stall startup forever ; failing here is preferable to a daemon
	// that 500s on first login.
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

	return &auth.Middleware{Mode: auth.ModeOIDC, Sessions: sessions}, o, nil
}
