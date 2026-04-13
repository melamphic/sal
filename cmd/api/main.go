// Command api is the Salvia backend API server.
// It reads configuration from environment variables, runs database migrations,
// and starts the HTTP server. See .env.example for all required variables.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/melamphic/sal/internal/app"
	"github.com/melamphic/sal/internal/platform/config"
)

func main() {
	ctx := context.Background()

	// Load .env in development. In production, variables are injected by the
	// runtime environment — godotenv.Load is a no-op if .env does not exist.
	_ = godotenv.Load()

	cfg, err := config.Load(ctx)
	if err != nil {
		slog.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	a, err := app.Build(ctx, cfg)
	if err != nil {
		slog.Error("failed to build app", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	// Listen for SIGINT / SIGTERM and give in-flight requests 10s to complete.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start the River job worker in the background.
	if err := a.RiverClient.Start(ctx); err != nil {
		a.Log.Error("failed to start river worker", slog.String("error", err.Error()))
		os.Exit(1)
	}

	go func() {
		a.Log.Info("server starting", slog.String("addr", a.Server.Addr))
		if err := a.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Error("server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	<-quit
	a.Log.Info("shutting down gracefully")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop River before closing the DB pool — River needs the pool to complete
	// in-flight jobs gracefully.
	if err := a.RiverClient.Stop(shutdownCtx); err != nil {
		a.Log.Error("river stop error", slog.String("error", err.Error()))
	}

	if err := a.Server.Shutdown(shutdownCtx); err != nil {
		a.Log.Error("shutdown error", slog.String("error", err.Error()))
	}

	a.DB.Close()
	a.Log.Info("server stopped")
}
