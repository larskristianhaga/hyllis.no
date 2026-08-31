package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// BookRepository is a Postgres-backed implementation of book.Repository.
type BookRepository struct {
	db dbtx
}

// NewBookRepository builds a BookRepository. db is typically a
// *pgxpool.Pool in production or a pgx.Tx in tests.
func NewBookRepository(db dbtx) *BookRepository {
	return &BookRepository{db: db}
}

const bookColumns = "id, isbn, title, author, publisher, year, cover_url, language, pages, source, created_at"

func scanBook(row scanner) (*book.Book, error) {
	var b book.Book
	var publisher, coverURL, language *string
	var year, pages *int
	if err := row.Scan(&b.ID, &b.ISBN, &b.Title, &b.Author, &publisher, &year, &coverURL, &language, &pages, &b.Source, &b.CreatedAt); err != nil {
		return nil, err
	}
	if publisher != nil {
		b.Publisher = *publisher
	}
	if year != nil {
		b.Year = *year
	}
	if coverURL != nil {
		b.CoverURL = *coverURL
	}
	if language != nil {
		b.Language = *language
	}
	if pages != nil {
		b.Pages = *pages
	}
	return &b, nil
}

func (r *BookRepository) Create(ctx context.Context, b *book.Book) error {
	const q = `INSERT INTO books (isbn, title, author, publisher, year, cover_url, language, pages, source)
	           VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	           RETURNING id, created_at`
	err := r.db.QueryRow(ctx, q,
		b.ISBN, b.Title, b.Author,
		nullableString(b.Publisher), nullableInt(b.Year), nullableString(b.CoverURL), nullableString(b.Language), nullableInt(b.Pages), b.Source,
	).Scan(&b.ID, &b.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return book.ErrDuplicateISBN
		}
		return fmt.Errorf("db: create book: %w", err)
	}
	return nil
}

func (r *BookRepository) GetByID(ctx context.Context, id string) (*book.Book, error) {
	const q = `SELECT ` + bookColumns + ` FROM books WHERE id = $1`
	b, err := scanBook(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, book.ErrNotFound
		}
		return nil, fmt.Errorf("db: get book by id: %w", err)
	}
	return b, nil
}

// GetByISBN looks a book up by its ISBN, which has a unique index — this is
// the O(1)-ish indexed lookup path scanning a barcode uses, as opposed to a
// full table scan.
func (r *BookRepository) GetByISBN(ctx context.Context, isbn string) (*book.Book, error) {
	const q = `SELECT ` + bookColumns + ` FROM books WHERE isbn = $1`
	b, err := scanBook(r.db.QueryRow(ctx, q, isbn))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, book.ErrNotFound
		}
		return nil, fmt.Errorf("db: get book by isbn: %w", err)
	}
	return b, nil
}

func (r *BookRepository) List(ctx context.Context) ([]*book.Book, error) {
	const q = `SELECT ` + bookColumns + ` FROM books ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("db: list books: %w", err)
	}
	defer rows.Close()

	var out []*book.Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan book: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Search returns books whose title or author fuzzily matches query, using
// pg_trgm's trigram similarity operator (%), backed by the GIN trigram
// indexes on both columns.
func (r *BookRepository) Search(ctx context.Context, query string) ([]*book.Book, error) {
	const q = `SELECT ` + bookColumns + `
	           FROM books
	           WHERE title % $1 OR author % $1
	           ORDER BY GREATEST(similarity(title, $1), similarity(author, $1)) DESC
	           LIMIT 20`
	rows, err := r.db.Query(ctx, q, query)
	if err != nil {
		return nil, fmt.Errorf("db: search books: %w", err)
	}
	defer rows.Close()

	var out []*book.Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan book: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BookRepository) Update(ctx context.Context, b *book.Book) error {
	const q = `UPDATE books SET isbn=$2, title=$3, author=$4, publisher=$5, year=$6, cover_url=$7, language=$8, pages=$9, source=$10 WHERE id=$1`
	tag, err := r.db.Exec(ctx, q,
		b.ID, b.ISBN, b.Title, b.Author,
		nullableString(b.Publisher), nullableInt(b.Year), nullableString(b.CoverURL), nullableString(b.Language), nullableInt(b.Pages), b.Source,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return book.ErrDuplicateISBN
		}
		return fmt.Errorf("db: update book: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return book.ErrNotFound
	}
	return nil
}

func (r *BookRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM books WHERE id=$1`
	tag, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("db: delete book: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return book.ErrNotFound
	}
	return nil
}

var _ book.Repository = (*BookRepository)(nil)
