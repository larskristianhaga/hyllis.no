package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/larskristianhaga/hyllis.no/internal/library"
)

// LibraryRepository is a Postgres-backed implementation of
// library.Repository.
type LibraryRepository struct {
	db dbtx
}

// NewLibraryRepository builds a LibraryRepository. db is typically a
// *pgxpool.Pool in production or a pgx.Tx in tests.
func NewLibraryRepository(db dbtx) *LibraryRepository {
	return &LibraryRepository{db: db}
}

const libraryColumns = "id, user_id, book_id, added_at, notes, location"

func scanEntry(row scanner) (*library.Entry, error) {
	var e library.Entry
	var notes, location *string
	if err := row.Scan(&e.ID, &e.UserID, &e.BookID, &e.AddedAt, &notes, &location); err != nil {
		return nil, err
	}
	if notes != nil {
		e.Notes = *notes
	}
	if location != nil {
		e.Location = *location
	}
	return &e, nil
}

func (r *LibraryRepository) Create(ctx context.Context, e *library.Entry) error {
	const q = `INSERT INTO library_entries (user_id, book_id, notes, location)
	           VALUES ($1, $2, $3, $4)
	           RETURNING id, added_at`
	err := r.db.QueryRow(ctx, q, e.UserID, e.BookID, nullableString(e.Notes), nullableString(e.Location)).Scan(&e.ID, &e.AddedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return library.ErrDuplicateEntry
		}
		return fmt.Errorf("db: create library entry: %w", err)
	}
	return nil
}

func (r *LibraryRepository) GetByID(ctx context.Context, id string) (*library.Entry, error) {
	const q = `SELECT ` + libraryColumns + ` FROM library_entries WHERE id = $1`
	e, err := scanEntry(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, library.ErrNotFound
		}
		return nil, fmt.Errorf("db: get library entry by id: %w", err)
	}
	return e, nil
}

func (r *LibraryRepository) GetByUserAndBook(ctx context.Context, userID, bookID string) (*library.Entry, error) {
	const q = `SELECT ` + libraryColumns + ` FROM library_entries WHERE user_id = $1 AND book_id = $2`
	e, err := scanEntry(r.db.QueryRow(ctx, q, userID, bookID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, library.ErrNotFound
		}
		return nil, fmt.Errorf("db: get library entry by user and book: %w", err)
	}
	return e, nil
}

func (r *LibraryRepository) ListByUser(ctx context.Context, userID string) ([]*library.Entry, error) {
	const q = `SELECT ` + libraryColumns + ` FROM library_entries WHERE user_id = $1 ORDER BY added_at DESC`
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("db: list library entries: %w", err)
	}
	defer rows.Close()

	var out []*library.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan library entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *LibraryRepository) Update(ctx context.Context, e *library.Entry) error {
	const q = `UPDATE library_entries SET notes=$2, location=$3 WHERE id=$1`
	tag, err := r.db.Exec(ctx, q, e.ID, nullableString(e.Notes), nullableString(e.Location))
	if err != nil {
		return fmt.Errorf("db: update library entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return library.ErrNotFound
	}
	return nil
}

func (r *LibraryRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM library_entries WHERE id=$1`
	tag, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("db: delete library entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return library.ErrNotFound
	}
	return nil
}

var _ library.Repository = (*LibraryRepository)(nil)
