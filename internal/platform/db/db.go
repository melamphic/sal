// Package db manages the PostgreSQL connection pool.
// Migrations are handled separately in internal/app/db.go using the
// migrations package which embeds the SQL files from the repo root.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens and validates a pgxpool connection to the given database URL.
// Adjust pool size via DATABASE_URL parameters (pool_max_conns, pool_min_conns).
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
