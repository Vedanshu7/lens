// Package main is the entry point for the Lens cache-visibility sidecar.
// Provider selection is controlled by LENS_* environment variables or a
// lens.yaml config file; the blank imports below register the default set.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vedanshu/lens/internal/agent"

	// Default providers — each registers itself via init().
	// Swap or extend by adding or removing blank imports here.
	_ "github.com/vedanshu/lens/internal/discovery/memberlist"
	_ "github.com/vedanshu/lens/internal/discovery/static"
	_ "github.com/vedanshu/lens/internal/observability/noop"
	_ "github.com/vedanshu/lens/internal/observability/prometheus"
	_ "github.com/vedanshu/lens/internal/observability/sql"
	_ "github.com/vedanshu/lens/internal/observability/stdout"
	_ "github.com/vedanshu/lens/internal/observability/webhook"
	_ "github.com/vedanshu/lens/internal/persistence/memory"
	_ "github.com/vedanshu/lens/internal/persistence/redis"
	_ "github.com/vedanshu/lens/internal/transport/grpc"
	_ "github.com/vedanshu/lens/internal/transport/nats"
	// Optional — compile with -tags lens_otel to enable:
	// _ "github.com/vedanshu/lens/internal/observability/otel"
)

func main() {
	cfg := agent.LoadConfig()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: agent.ParseLogLevel(cfg.LogLevel),
	})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := agent.New(cfg)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      a.Routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		slog.Info("lens agent listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("http server error", "err", err)
		}
	}()

	go a.Connect(ctx)

	<-ctx.Done()
	slog.Info("shutting down")
	a.Shutdown(context.Background())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}
