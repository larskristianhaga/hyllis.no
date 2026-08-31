package db

import (
	"context"
	"fmt"
	"log/slog"

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
// left to apply it's a no-op. log may be nil (e.g. in tests that don't care
// about migration output), in which case logging is skipped.
//
// Multiple machines/regions can call this concurrently (e.g. a Fly rolling
// deploy) — bun_migration_locks makes Lock fail fast for whichever one loses
// the race, which is logged as a warning rather than treated as fatal by
// this function; the caller decides whether that's fatal for it.
func RunMigrations(ctx context.Context, db *bun.DB, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	migrator := migrate.NewMigrator(db, Migrations)

	log.Info("db: initializing migrator")
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("db: init migrator: %w", err)
	}

	log.Info("db: acquiring migration lock")
	if err := migrator.Lock(ctx); err != nil {
		log.Warn("db: failed to acquire migration lock, another machine may be migrating", "error", err)
		return fmt.Errorf("db: lock migrator: %w", err)
	}
	defer func() {
		if err := migrator.Unlock(ctx); err != nil {
			log.Warn("db: failed to release migration lock", "error", err)
		}
	}()

	group, err := migrator.Migrate(ctx)
	if err != nil {
		log.Error("db: migration failed", "error", err)
		return fmt.Errorf("db: migrate: %w", err)
	}

	if group.IsZero() {
		log.Info("db: no pending migrations")
		return nil
	}

	names := make([]string, len(group.Migrations))
	for i, m := range group.Migrations {
		names[i] = m.Name
	}
	log.Info("db: applied migrations", "group_id", group.ID, "count", len(names), "migrations", names)
	return nil
}
