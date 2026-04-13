// Package db manages the PostgreSQL connection pool and database migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	// Register the pgx driver with goose.
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Connect opens and validates a pgxpool connection to the given database URL.
// The pool is configured for production use — adjust pool size via DATABASE_URL
// parameters (pool_max_conns, pool_min_conns) if needed.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db.Connect: parse config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db.Connect: new pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db.Connect: ping: %w", err)
	}

	return pool, nil
}

// Migrate runs all pending goose migrations from the embedded migrations directory.
// It is safe to call on every startup — goose is idempotent.
func Migrate(ctx context.Context, databaseURL string, log *slog.Logger) error {
	// goose uses database/sql — open a separate connection for migrations.
	db, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("db.Migrate: open: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations)
	goose.SetLogger(&gooseLogger{log: log})

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db.Migrate: set dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("db.Migrate: up: %w", err)
	}

	return nil
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, databaseURL string, log *slog.Logger) error {
	db, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("db.MigrateDown: open: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations)
	goose.SetLogger(&gooseLogger{log: log})

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db.MigrateDown: set dialect: %w", err)
	}

	if err := goose.DownContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("db.MigrateDown: down: %w", err)
	}

	return nil
}

// gooseLogger adapts slog.Logger to goose's Logger interface.
type gooseLogger struct {
	log *slog.Logger
}

func (g *gooseLogger) Fatalf(format string, v ...interface{}) {
	g.log.Error(fmt.Sprintf(format, v...))
}

func (g *gooseLogger) Printf(format string, v ...interface{}) {
	g.log.Info(fmt.Sprintf(format, v...))
}
