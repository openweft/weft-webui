// Command weft-webui serves the Weft web dashboard : a Horizon-style UI
// for the platform's object types (tenants, projects, users, networks,
// security groups, volumes, shares, hosts, …). It serves a small JSON API
// and the embedded SvelteJS single-page app from one binary.
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
	"time"

	"github.com/openweft/weft-webui/internal/server"
	"github.com/openweft/weft-webui/internal/wclient"
)

//go:embed all:web/dist
var webDist embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	weftSocket := flag.String("weft-socket", "", "weft daemon socket (e.g. ~/.vzd/vzd.sock or ssh://host) ; empty = mock mode")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	static, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		logger.Error("embed", "err", err)
		os.Exit(1)
	}

	var live *wclient.Client
	if *weftSocket != "" {
		live = wclient.New(*weftSocket)
		logger.Info("weft live mode", "socket", *weftSocket)
		defer live.Close()
	} else {
		logger.Info("mock mode (no --weft-socket)")
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(logger, static, live),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("weft-webui listening", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	logger.Info("weft-webui stopped")
}
