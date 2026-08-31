package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// BookRepository is a Postgres-backed implementation of book.Repository,
// built on the Bun ORM.
type BookRepository struct {
	db dbtx
}

// NewBookRepository builds a BookRepository. db is typically a *bun.DB in
// production or a bun.Tx in tests.
func NewBookRepository(db dbtx) *BookRepository {
	return &BookRepository{db: db}
}

// bookModel is Bun's row-mapped view of the books table. It exists
// separately from book.Book so the domain type stays a plain struct with no
// ORM tags or NULL-handling concerns — toBook/fromBook translate between
// the two.
type bookModel struct {
	bun.BaseModel `bun:"table:books"`

	ID        string       `bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ISBN      string       `bun:"isbn"`
	Title     string       `bun:"title"`
	Author    string       `bun:"author"`
	Publisher *string      `bun:"publisher"`
	Year      *int         `bun:"year"`
	CoverURL  *string      `bun:"cover_url"`
	Language  *string      `bun:"language"`
	Pages     *int         `bun:"pages"`
	Source    string       `bun:"source"`
	CreatedAt sql.NullTime `bun:"created_at,nullzero,default:now()"`
}

func toBook(m *bookModel) *book.Book {
	b := &book.Book{
		ID:     m.ID,
		ISBN:   m.ISBN,
		Title:  m.Title,
		Author: m.Author,
		Source: m.Source,
	}
	if m.Publisher != nil {
		b.Publisher = *m.Publisher
	}
	if m.Year != nil {
		b.Year = *m.Year
	}
	if m.CoverURL != nil {
		b.CoverURL = *m.CoverURL
	}
	if m.Language != nil {
		b.Language = *m.Language
	}
	if m.Pages != nil {
		b.Pages = *m.Pages
	}
	if m.CreatedAt.Valid {
		b.CreatedAt = m.CreatedAt.Time
	}
	return b
}

func fromBook(b *book.Book) *bookModel {
	return &bookModel{
		ID:        b.ID,
		ISBN:      b.ISBN,
		Title:     b.Title,
		Author:    b.Author,
		Publisher: nullableString(b.Publisher),
		Year:      nullableInt(b.Year),
		CoverURL:  nullableString(b.CoverURL),
		Language:  nullableString(b.Language),
		Pages:     nullableInt(b.Pages),
		Source:    b.Source,
	}
}

func (r *BookRepository) Create(ctx context.Context, b *book.Book) error {
	m := fromBook(b)
	_, err := r.db.NewInsert().Model(m).Returning("id, created_at").Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return book.ErrDuplicateISBN
		}
		return fmt.Errorf("db: create book: %w", err)
	}
	b.ID = m.ID
	if m.CreatedAt.Valid {
		b.CreatedAt = m.CreatedAt.Time
	}
	return nil
}

func (r *BookRepository) GetByID(ctx context.Context, id string) (*book.Book, error) {
	var m bookModel
	err := r.db.NewSelect().Model(&m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, book.ErrNotFound
		}
		return nil, fmt.Errorf("db: get book by id: %w", err)
	}
	return toBook(&m), nil
}

// GetByISBN looks a book up by its ISBN, which has a unique index — this is
// the O(1)-ish indexed lookup path scanning a barcode uses, as opposed to a
// full table scan.
func (r *BookRepository) GetByISBN(ctx context.Context, isbn string) (*book.Book, error) {
	var m bookModel
	err := r.db.NewSelect().Model(&m).Where("isbn = ?", isbn).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, book.ErrNotFound
		}
		return nil, fmt.Errorf("db: get book by isbn: %w", err)
	}
	return toBook(&m), nil
}

func (r *BookRepository) List(ctx context.Context) ([]*book.Book, error) {
	var models []bookModel
	if err := r.db.NewSelect().Model(&models).OrderExpr("created_at DESC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("db: list books: %w", err)
	}
	out := make([]*book.Book, 0, len(models))
	for i := range models {
		out = append(out, toBook(&models[i]))
	}
	return out, nil
}

// Search returns books whose title or author fuzzily matches query, using
// pg_trgm's trigram similarity operator (%), backed by the GIN trigram
// indexes on both columns.
func (r *BookRepository) Search(ctx context.Context, query string) ([]*book.Book, error) {
	var models []bookModel
	err := r.db.NewSelect().
		Model(&models).
		Where("title % ? OR author % ?", query, query).
		OrderExpr("GREATEST(similarity(title, ?), similarity(author, ?)) DESC", query, query).
		Limit(20).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: search books: %w", err)
	}
	out := make([]*book.Book, 0, len(models))
	for i := range models {
		out = append(out, toBook(&models[i]))
	}
	return out, nil
}

func (r *BookRepository) Update(ctx context.Context, b *book.Book) error {
	m := fromBook(b)
	res, err := r.db.NewUpdate().Model(m).Where("id = ?", m.ID).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return book.ErrDuplicateISBN
		}
		return fmt.Errorf("db: update book: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return book.ErrNotFound
	}
	return nil
}

func (r *BookRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.NewDelete().Model((*bookModel)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("db: delete book: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return book.ErrNotFound
	}
	return nil
}

var _ book.Repository = (*BookRepository)(nil)
