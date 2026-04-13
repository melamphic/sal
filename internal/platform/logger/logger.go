// Package logger configures the application-wide structured logger.
// All code must use log/slog — never fmt.Println or log.Printf in production paths.
package logger

import (
	"log/slog"
	"os"
)

// New returns a slog.Logger configured for the given environment.
// In development: human-readable text output.
// In production: JSON output suitable for log aggregation (Datadog, Loki, etc.).
func New(env string) *slog.Logger {
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		// Include source file and line in development for easier debugging.
		AddSource: env == "development",
		Level:     slog.LevelDebug,
	}

	if env == "production" || env == "staging" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
