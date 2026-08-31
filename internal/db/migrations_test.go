package db

import (
	"context"
	"testing"
)

// TestMigrationsUpAndDown exercises the acceptance criterion that
// migrations apply up and down without errors. It runs against its own
// dedicated container (rather than the shared testPool) since it tears
// every table down at the end.
func TestMigrationsUpAndDown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers-based test in -short mode")
	}

	ctx := context.Background()
	pool, cleanup, err := startPostgres(ctx)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	defer cleanup()

	if err := applyUpMigrations(ctx, pool); err != nil {
		t.Fatalf("apply up migrations: %v", err)
	}

	// Sanity-check the schema actually landed: pg_trgm active, tables and
	// the unique constraint present.
	var trgmEnabled bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`).Scan(&trgmEnabled); err != nil {
		t.Fatalf("check pg_trgm: %v", err)
	}
	if !trgmEnabled {
		t.Error("expected pg_trgm extension to be enabled after up migrations")
	}

	for _, table := range []string{"users", "books", "library_entries"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %q to exist after up migrations", table)
		}
	}

	if err := applyDownMigrations(ctx, pool); err != nil {
		t.Fatalf("apply down migrations: %v", err)
	}

	for _, table := range []string{"users", "books", "library_entries"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if exists {
			t.Errorf("expected table %q to be gone after down migrations", table)
		}
	}
}
