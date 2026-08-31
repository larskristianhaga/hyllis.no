package db

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"

	"github.com/larskristianhaga/hyllis.no/migrations"
)

// Migrations is discovered once from the embedded /migrations SQL files.
// bun/migrate matches our existing "NNNNNN_name.up.sql"/".down.sql"
// filenames as-is, so no renaming was needed to adopt it.
var Migrations = migrate.NewMigrations()

func init() {
	if err := Migrations.Discover(migrations.FS); err != nil {
		panic(fmt.Sprintf("db: discover migrations: %v", err))
	}
}

// RunMigrations applies every unapplied migration in Migrations against db,
// creating bun/migrate's own tracking tables first if needed (bun_migrations,
// bun_migration_locks). It's safe to call on every startup: with nothing
// left to apply it's a no-op.
func RunMigrations(ctx context.Context, db *bun.DB) error {
	migrator := migrate.NewMigrator(db, Migrations)

	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("db: init migrator: %w", err)
	}

	if err := migrator.Lock(ctx); err != nil {
		return fmt.Errorf("db: lock migrator: %w", err)
	}
	defer func() { _ = migrator.Unlock(ctx) }()

	if _, err := migrator.Migrate(ctx); err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	return nil
}
