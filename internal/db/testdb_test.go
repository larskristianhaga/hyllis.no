package db

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// migrationsDir is relative to this package's directory (internal/db).
const migrationsDir = "../../migrations"

// testPool is a shared pool, backed by a single Postgres testcontainer with
// every migration applied, used by all repository tests in this package.
// migrations_test.go spins up its own separate container instead of reusing
// this one, since it needs to tear tables down.
var testPool *pgxpool.Pool

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
	pool, cleanup, err := startPostgres(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db: start postgres:", err)
		return 1
	}
	defer cleanup()

	if err := applyUpMigrations(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "db: apply migrations:", err)
		return 1
	}

	testPool = pool
	return m.Run()
}

// startPostgres starts a fresh Postgres testcontainer and returns a
// connected pool plus a cleanup func that closes the pool and terminates
// the container.
func startPostgres(ctx context.Context) (*pgxpool.Pool, func(), error) {
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

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("connect pool: %w", err)
	}

	cleanup := func() {
		pool.Close()
		_ = container.Terminate(ctx)
	}
	return pool, cleanup, nil
}

// migrationFiles returns migration files matching pattern (e.g. "*.up.sql"),
// sorted lexicographically so numbered pairs apply in order.
func migrationFiles(pattern string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(migrationsDir, pattern))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func applyUpMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := migrationFiles("*.up.sql")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .up.sql files found under %s", migrationsDir)
	}
	for _, f := range files {
		if err := execSQLFile(ctx, pool, f); err != nil {
			return err
		}
	}
	return nil
}

// applyDownMigrations runs every .down.sql file in reverse order (undoing
// the most recent migration first), mirroring how `migrate ... down` walks
// the migration chain backwards.
func applyDownMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := migrationFiles("*.down.sql")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .down.sql files found under %s", migrationsDir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, f := range files {
		if err := execSQLFile(ctx, pool, f); err != nil {
			return err
		}
	}
	return nil
}

func execSQLFile(ctx context.Context, pool *pgxpool.Pool, path string) error {
	sql, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("exec %s: %w", path, err)
	}
	return nil
}

// withTx begins a transaction on testPool and registers a cleanup that
// rolls it back, so each test runs in isolation without needing to truncate
// tables or spin up a fresh container. pgx.Tx satisfies the dbtx interface
// directly, so it can be passed straight into the repository constructors.
func withTx(t *testing.T) pgx.Tx {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping testcontainers-based test in -short mode")
	}

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(ctx)
	})
	return tx
}
