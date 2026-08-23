// Command thinkpixeltg runs the ThinkPixelTG HTTP service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpadapter "github.com/bdobrica/ThinkPixelTG/internal/adapters/http"
	postgresadapter "github.com/bdobrica/ThinkPixelTG/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelTG/internal/config"
	"github.com/bdobrica/ThinkPixelTG/internal/telemetry"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "healthcheck" {
		healthcheck(os.Args[2])
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "thinkpixeltg:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:], os.Environ())
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	logger := telemetry.NewLogger(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	observability, err := telemetry.Bootstrap(ctx, telemetry.BootstrapConfig{
		ServiceName: "thinkpixeltg", ServiceVersion: version, TraceMode: telemetry.TraceMode(cfg.Telemetry.Mode), OTLPEndpoint: cfg.Telemetry.Endpoint,
	})
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
		defer cancel()
		_ = observability.Shutdown(shutdownCtx)
	}()
	readiness := httpadapter.Readiness(func(context.Context) error { return nil })
	if cfg.Database.URL != "" {
		pool, openErr := postgresadapter.Open(ctx, postgresadapter.PoolConfig{
			URL: cfg.Database.URL, MaxConnections: cfg.Database.MaxConnections, MinConnections: cfg.Database.MinConnections,
			MaxConnectionLifetime: cfg.Database.MaxConnectionLifetime, MaxConnectionIdleTime: cfg.Database.MaxConnectionIdleTime,
			HealthCheckPeriod: cfg.Database.HealthCheckPeriod, ConnectTimeout: cfg.Database.ConnectTimeout,
			ReadinessTimeout: cfg.Database.ReadinessTimeout, StatementTimeout: cfg.Database.StatementTimeout,
			LockTimeout: cfg.Database.LockTimeout, IdleTransactionTimeout: cfg.Database.IdleTransactionTimeout,
			TransactionTimeout: cfg.Database.TransactionTimeout, ShutdownTimeout: cfg.Database.ShutdownTimeout,
		}, observability.Registry)
		if openErr != nil {
			return fmt.Errorf("database: %w", openErr)
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Database.ShutdownTimeout)
			defer cancel()
			_ = pool.Shutdown(shutdownCtx)
		}()
		readiness = pool.Ready
	}
	server, err := httpadapter.New(httpadapter.Options{Config: cfg.HTTP, Logger: logger, Observability: observability, Readiness: readiness})
	if err != nil {
		return fmt.Errorf("HTTP server: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.HTTP.Address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	_ = telemetry.LogEvent(ctx, logger, slog.LevelInfo, "service.started", slog.String("service.version", version), slog.String("service.commit", commit), slog.String("http.address", cfg.HTTP.Address))
	if err := server.Run(ctx, listener); err != nil {
		return err
	}
	_ = telemetry.LogEvent(context.Background(), logger, slog.LevelInfo, "service.stopped")
	return nil
}

func healthcheck(url string) {
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url)
	if err != nil || response.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	_ = response.Body.Close()
}
