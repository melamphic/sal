//go:build integration

package testutil

// Integration test harness — starts a real Postgres container and runs
// migrations once per test binary. Each test calls Truncate() to reset state.
//
// Usage in a repository test package:
//
//	func TestMain(m *testing.M) { testutil.IntegrationMain(m) }
//
//	func TestFoo(t *testing.T) {
//	    db := testutil.NewTestDB(t)
//	    // db is a *pgxpool.Pool connected to the shared container,
//	    // all tables are truncated before the test body runs.
//	}

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/migrations"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// sharedDB is the connection pool wired up once by IntegrationMain.
var sharedDB *pgxpool.Pool

// truncateTables is the ordered list of tables to wipe between tests.
// Order matters: child tables before parent tables (foreign key constraints).
var truncateTables = []string{
	"auth_tokens",
	"recordings",
	"vet_subject_details",
	"subjects",
	"contacts",
	"staff",
	"clinics",
}

// IntegrationMain starts the Postgres container, runs migrations, and hands
// control back to the test binary. Call this from TestMain in any package that
// needs a real database.
func IntegrationMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("saltest"),
		postgres.WithUsername("saltest"),
		postgres.WithPassword("saltest"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testutil: start postgres container: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = ctr.Terminate(ctx) }()

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testutil: get connection string: %v\n", err)
		os.Exit(1)
	}

	sharedDB, err = pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testutil: connect to test db: %v\n", err)
		os.Exit(1)
	}
	defer sharedDB.Close()

	if err := runMigrations(ctx, dsn); err != nil {
		fmt.Fprintf(os.Stderr, "testutil: run migrations: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// NewTestDB returns the shared *pgxpool.Pool and truncates all tenant tables
// so each test starts from a clean state.
func NewTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if sharedDB == nil {
		t.Fatal("testutil.NewTestDB: sharedDB is nil — did you call testutil.IntegrationMain in TestMain?")
	}
	truncate(t, sharedDB)
	return sharedDB
}

// truncate deletes all rows from every table, in dependency order.
func truncate(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, table := range truncateTables {
		if _, err := db.Exec(ctx, "TRUNCATE TABLE "+table+" RESTART IDENTITY CASCADE"); err != nil {
			t.Fatalf("testutil.truncate: table %s: %v", table, err)
		}
	}
}

// runMigrations applies all goose migrations against the test database.
func runMigrations(ctx context.Context, dsn string) error {
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open goose db: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
