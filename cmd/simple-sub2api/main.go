package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/logging"
	"github.com/0xForce-Network/simple-sub2api/internal/server"
	"github.com/0xForce-Network/simple-sub2api/internal/version"
)

const shutdownTimeout = 10 * time.Second

type managedHTTPServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

type tunnelStopper interface {
	StopTunnel() error
}

func main() {
	configPath := flag.String("config", config.DefaultPath, "path to simple-sub2api JSON config")
	bind := flag.String("bind", "", "server bind address, for example 127.0.0.1:8080")
	allowLAN := flag.Bool("allow-lan", false, "explicitly allow non-loopback bind addresses")
	adminPassword := flag.String("admin-password", "", "dashboard admin password; required for LAN bind")
	corsOrigins := flag.String("cors-origins", "", "comma-separated explicit CORS origins; empty keeps CORS closed")
	debugDashboard := flag.Bool("debug-dashboard", false, "explicitly enable admin-only sanitized dashboard debug snapshot output")
	debugAPI := flag.Bool("debug-api", false, "enable sanitized gateway API request debug logs for compatibility diagnosis")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	logger := logging.New()
	overrides := config.RuntimeOverrides{}
	setFlags := visitedFlags()
	if setFlags["bind"] {
		overrides.BindSet = true
		overrides.Bind = *bind
	}
	if setFlags["allow-lan"] {
		overrides.AllowLANSet = true
		overrides.AllowLAN = *allowLAN
	}
	if setFlags["admin-password"] {
		overrides.AdminPasswordSet = true
		overrides.AdminPassword = *adminPassword
	} else if envPassword := os.Getenv("SIMPLE_SUB2API_ADMIN_PASSWORD"); envPassword != "" {
		overrides.AdminPasswordSet = true
		overrides.AdminPassword = envPassword
	}
	if setFlags["cors-origins"] {
		overrides.CORSAllowedOriginsSet = true
		overrides.CORSAllowedOrigins = splitCSV(*corsOrigins)
	}

	store, created, err := config.Open(*configPath)
	if err != nil {
		logger.Error("config load failed", slog.String("error", err.Error()), slog.String("config_path", *configPath))
		os.Exit(1)
	}
	if overrides.HasAny() {
		if err := store.ApplyOverrides(overrides); err != nil {
			logger.Error("config override failed", slog.String("error", err.Error()), slog.String("config_path", *configPath))
			os.Exit(1)
		}
	}

	cfg := store.Snapshot()
	logger.Info(
		"simple-sub2api starting",
		slog.String("version", version.String()),
		slog.String("config_path", *configPath),
		slog.Bool("created_config", created),
		slog.String("bind", cfg.Server.Bind),
		slog.Bool("allow_lan", cfg.Server.AllowLAN),
		slog.Bool("cors_enabled", len(cfg.Server.CORSAllowedOrigins) > 0),
		slog.Bool("debug_dashboard", debugDashboardEnabled(*debugDashboard)),
		slog.Bool("debug_api", debugAPIEnabled(*debugAPI)),
	)

	app := server.NewWithOptions(store, logger, server.Options{DebugDashboard: debugDashboardEnabled(*debugDashboard), DebugAPI: debugAPIEnabled(*debugAPI)})
	httpServer := &http.Server{Addr: cfg.Server.Bind, Handler: app.Handler()}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := serveWithGracefulShutdown(ctx, httpServer, app, logger); err != nil {
		logger.Error("server stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func serveWithGracefulShutdown(ctx context.Context, httpServer managedHTTPServer, stopper tunnelStopper, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		if logger != nil {
			logger.Info("shutdown signal received")
		}
		return shutdownHTTPAndTunnel(httpServer, stopper)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return stopTunnel(stopper)
		}
		stopErr := stopTunnel(stopper)
		return errors.Join(err, stopErr)
	}
}

func shutdownHTTPAndTunnel(httpServer managedHTTPServer, stopper tunnelStopper) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := httpServer.Shutdown(ctx)
	if errors.Is(shutdownErr, http.ErrServerClosed) {
		shutdownErr = nil
	}
	return errors.Join(shutdownErr, stopTunnel(stopper))
}

func stopTunnel(stopper tunnelStopper) error {
	if stopper == nil {
		return nil
	}
	return stopper.StopTunnel()
}

func debugDashboardEnabled(flagValue bool) bool {
	if flagValue {
		return true
	}
	value := strings.TrimSpace(strings.ToLower(os.Getenv("SIMPLE_SUB2API_DEBUG_DASHBOARD")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func debugAPIEnabled(flagValue bool) bool {
	if flagValue {
		return true
	}
	value := strings.TrimSpace(strings.ToLower(os.Getenv("SIMPLE_SUB2API_DEBUG_API")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func visitedFlags() map[string]bool {
	seen := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		seen[f.Name] = true
	})
	return seen
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
