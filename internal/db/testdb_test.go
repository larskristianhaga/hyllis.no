package db

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// testDB is a shared Bun handle, backed by a single Postgres testcontainer
// with every migration applied, used by all repository tests in this
// package. migrations_test.go spins up its own separate container instead
// of reusing this one, since it needs to tear tables down.
var testDB *bun.DB

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	// testing.Short() reads the -short flag, which isn't parsed yet this
	// early in TestMain — parse it ourselves before checking it.
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}

	ctx := context.Background()
	db, cleanup, err := startPostgres(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db: start postgres:", err)
		return 1
	}
	defer cleanup()

	if err := RunMigrations(ctx, db); err != nil {
		fmt.Fprintln(os.Stderr, "db: apply migrations:", err)
		return 1
	}

	testDB = db
	return m.Run()
}

// startPostgres starts a fresh Postgres testcontainer and returns a
// connected Bun handle plus a cleanup func that closes it and terminates
// the container.
func startPostgres(ctx context.Context) (*bun.DB, func(), error) {
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("hyllis_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		// The postgres image restarts once during initdb; without this the
		// container can be reported "ready" while still mid-restart, and the
		// first connection attempt gets an EOF.
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start container: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("connection string: %w", err)
	}

	db, err := openBun(dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("connect: %w", err)
	}

	cleanup := func() {
		_ = Close(db)
		_ = container.Terminate(ctx)
	}
	return db, cleanup, nil
}

// openBun connects to dsn via pgx's database/sql driver (forcing the simple
// query protocol, same as NewPool) and wraps it in a Bun handle.
func openBun(dsn string) (*bun.DB, error) {
	cfg, err := stdlibConnConfig(dsn)
	if err != nil {
		return nil, err
	}
	sqldb := stdlib.OpenDB(*cfg)
	return bun.NewDB(sqldb, pgdialect.New()), nil
}

// withTx begins a transaction on testDB and registers a cleanup that rolls
// it back, so each test runs in isolation without needing to truncate
// tables or spin up a fresh container. bun.Tx satisfies the dbtx interface
// directly, so it can be passed straight into the repository constructors.
func withTx(t *testing.T) bun.Tx {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping testcontainers-based test in -short mode")
	}

	ctx := context.Background()
	tx, err := testDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback()
	})
	return tx
}
