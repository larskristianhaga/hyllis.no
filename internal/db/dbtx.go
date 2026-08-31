package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// dbtx is the subset of pgx's query interface the repositories depend on.
// It's satisfied by both *pgxpool.Pool (production) and pgx.Tx (tests), so a
// repository doesn't need to know whether it's running against the real
// pool or an isolated per-test transaction.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// scanner is satisfied by both pgx.Row (from QueryRow) and pgx.Rows (from
// Query, row by row via Next/Scan), letting row-mapping helpers accept
// either.
type scanner interface {
	Scan(dest ...any) error
}
