package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation is the Postgres SQLSTATE code for a unique-constraint
// violation.
const pgUniqueViolation = "23505"

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
