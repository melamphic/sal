package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/migrations"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func connectPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connectPool: parse config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connectPool: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connectPool: ping: %w", err)
	}
	return pool, nil
}

func runMigrations(ctx context.Context, databaseURL string, log *slog.Logger) error {
	db, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("runMigrations: open: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(&gooseLog{log: log})

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("runMigrations: set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("runMigrations: up: %w", err)
	}
	return nil
}

type gooseLog struct{ log *slog.Logger }

func (g *gooseLog) Fatalf(format string, v ...interface{}) {
	g.log.Error(fmt.Sprintf(format, v...))
}
func (g *gooseLog) Printf(format string, v ...interface{}) {
	g.log.Info(fmt.Sprintf(format, v...))
}
