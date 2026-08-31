// Package db contains the Postgres-backed implementations of the
// internal/book, internal/library and internal/user Repository interfaces,
// built on pgx.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds a connection pool for dsn. It forces pgx's simple query
// protocol rather than the default extended protocol (prepared statements),
// because Supabase's connection pooler (PgBouncer, transaction mode) does
// not reliably support session-scoped prepared statements — without this,
// queries over a pooled Supabase connection string can fail intermittently.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
