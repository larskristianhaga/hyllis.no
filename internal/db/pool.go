// Package db contains the Postgres-backed implementations of the
// internal/book, internal/library and internal/user Repository interfaces,
// built on Bun (an ORM) over a pgx-backed database/sql connection.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// NewPool builds a Bun database handle for dsn, backed by pgx's
// database/sql driver. It forces pgx's simple query protocol rather than
// the default extended protocol (prepared statements), because Supabase's
// connection pooler (PgBouncer, transaction mode) does not reliably support
// session-scoped prepared statements — without this, queries over a pooled
// Supabase connection string can fail intermittently.
func NewPool(ctx context.Context, dsn string) (*bun.DB, error) {
	cfg, err := stdlibConnConfig(dsn)
	if err != nil {
		return nil, err
	}

	sqldb := stdlib.OpenDB(*cfg)
	if err := sqldb.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return bun.NewDB(sqldb, pgdialect.New()), nil
}

// stdlibConnConfig parses dsn into a pgx config forced onto the simple query
// protocol (see NewPool's doc comment), ready for stdlib.OpenDB. Shared with
// the test package's own bun.DB setup so both go through the exact same
// connection behavior.
func stdlibConnConfig(dsn string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return cfg, nil
}

// Close is a small helper so callers don't need to know NewPool returns a
// bun.DB wrapping a *sql.DB under the hood.
func Close(db *bun.DB) error {
	var sqldb *sql.DB = db.DB
	return sqldb.Close()
}
