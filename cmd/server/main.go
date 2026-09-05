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

	"github.com/example/go-serve/internal/auth"
	"github.com/example/go-serve/internal/cluster"
	"github.com/example/go-serve/internal/config"
	"github.com/example/go-serve/internal/controlplane"
	"github.com/example/go-serve/internal/dataplane"
	"github.com/example/go-serve/internal/observability"
)

func main() {
	configPath := flag.String("config", "", "path to a TOML configuration file")
	flag.Parse()

	cfg := config.Default()
	var err error
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
	var authorizer dataplane.Authorizer
	var authentication *auth.Middleware
	if cfg.Auth.Mode != "disabled" {
		authentication, err = auth.New(context.Background(), cfg.Auth)
		if err != nil {
			slog.Error("failed to initialize authentication", "error", err)
			os.Exit(1)
		}
		authorizer = authentication
	}
	dataPlane, err := dataplane.NewServer(cfg, authorizer)
	if err != nil {
		slog.Error("invalid data-plane configuration", "error", err)
		os.Exit(1)
	}
	var clusterNode *cluster.Node
	if cfg.Cluster.Enabled {
		clusterNode, err = cluster.New(context.Background(), cfg.Cluster)
		if err != nil {
			slog.Error("failed to initialize cluster", "error", err)
			os.Exit(1)
		}
	}

	controlHandler := controlplane.NewServer(cfg).Handler()
	if clusterNode != nil {
		controlHandler = clusterNode.Handler(controlHandler)
	}
	metrics := observability.New()
	controlHandler = metrics.Endpoint(metrics.Middleware(controlHandler))
	if authentication != nil {
		controlHandler = authentication.Protect(controlHandler)
	}
	controlServer := &http.Server{
		Addr:              cfg.Server.AdminAddr,
		Handler:           controlHandler,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}
	publicHandler := dataPlane.Handler()
	if clusterNode != nil {
		publicHandler = clusterNode.Proxy(publicHandler)
	}
	publicServer := &http.Server{
		Addr:              cfg.Server.PublicAddr,
		Handler:           metrics.Middleware(publicHandler),
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
	if cfg.Server.TLSCertFile != "" {
		publicListener, err = dataplane.TLSListener(publicListener, cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile)
		if err != nil {
			_ = controlListener.Close()
			_ = publicListener.Close()
			slog.Error("failed to configure data-plane TLS", "error", err)
			os.Exit(1)
		}
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
		dataPlane.Close()
		if clusterNode != nil {
			if err := clusterNode.Close(); err != nil {
				slog.Error("cluster shutdown failed", "error", err)
			}
		}
	}()

	slog.Info("starting control plane", "addr", controlServer.Addr, "ui", cfg.ControlPlane.UI)
	slog.Info("starting data plane", "addr", publicServer.Addr)
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
