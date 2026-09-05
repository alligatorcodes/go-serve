package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/go-serve/internal/config"
	"github.com/example/go-serve/internal/controlplane"
	"github.com/example/go-serve/internal/dataplane"
)

func main() {
	configPath := flag.String("config", "", "path to a TOML configuration file")
	flag.Parse()

	cfg := config.Default()
	if *configPath != "" {
		loaded, err := config.LoadFile(*configPath)
		if err != nil {
			slog.Error("failed to load configuration", "path", *configPath, "error", err)
			os.Exit(1)
		}
		cfg = loaded
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	dataPlane, err := dataplane.NewServer(cfg)
	if err != nil {
		slog.Error("invalid data-plane configuration", "error", err)
		os.Exit(1)
	}

	controlServer := &http.Server{
		Addr:              cfg.Server.AdminAddr,
		Handler:           controlplane.NewServer(cfg).Handler(),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}
	publicServer := &http.Server{
		Addr:              cfg.Server.PublicAddr,
		Handler:           dataPlane.Handler(),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		MaxHeaderBytes:    cfg.Limits.MaxHeaderBytes,
	}
	controlListener, err := net.Listen("tcp", controlServer.Addr)
	if err != nil {
		slog.Error("failed to listen for control plane", "addr", controlServer.Addr, "error", err)
		os.Exit(1)
	}
	publicListener, err := net.Listen("tcp", publicServer.Addr)
	if err != nil {
		_ = controlListener.Close()
		slog.Error("failed to listen for data plane", "addr", publicServer.Addr, "error", err)
		os.Exit(1)
	}
	publicListener = dataplane.LimitListener(publicListener, cfg.Limits.MaxConnections)

	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 2)
	go func() { serverErrors <- controlServer.Serve(controlListener) }()
	go func() { serverErrors <- publicServer.Serve(publicListener) }()
	go func() {
		<-shutdownContext.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()
		if err := controlServer.Shutdown(shutdown); err != nil {
			slog.Error("control-plane shutdown failed", "error", err)
		}
		if err := publicServer.Shutdown(shutdown); err != nil {
			slog.Error("data-plane shutdown failed", "error", err)
		}
	}()

	slog.Info("starting control plane", "addr", controlServer.Addr, "ui", cfg.ControlPlane.UI)
	slog.Info("starting data plane", "addr", publicServer.Addr)
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
