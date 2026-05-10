package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/p-repin/bybit_mirror/internal/auth"
	"github.com/p-repin/bybit_mirror/internal/bybit"
	"github.com/p-repin/bybit_mirror/internal/config"
	"github.com/p-repin/bybit_mirror/internal/hub"
	"github.com/p-repin/bybit_mirror/internal/server"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}

	h := hub.New()

	a, err := auth.New(cfg.PasswordHash, cfg.SessionSecret, cfg.SessionTTL(), !cfg.CookieInsecure)
	if err != nil {
		slog.Error("auth init", "err", err)
		os.Exit(1)
	}

	bb := bybit.NewClient(cfg, h)
	srv := server.New(a, h)
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go bb.Run(ctx)

	go func() {
		slog.Info("http listening", "addr", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = httpServer.Shutdown(shutCtx)
}
