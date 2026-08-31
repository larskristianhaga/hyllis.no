package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/larskristianhaga/hyllis.no/internal/library"
)

// LibraryRepository is a Postgres-backed implementation of
// library.Repository, built on the Bun ORM.
type LibraryRepository struct {
	db dbtx
}

// NewLibraryRepository builds a LibraryRepository. db is typically a
// *bun.DB in production or a bun.Tx in tests.
func NewLibraryRepository(db dbtx) *LibraryRepository {
	return &LibraryRepository{db: db}
}

// entryModel is Bun's row-mapped view of the library_entries table. See
// bookModel's doc comment for why this is kept separate from
// library.Entry.
type entryModel struct {
	bun.BaseModel `bun:"table:library_entries"`

	ID       string       `bun:"id,pk,nullzero,default:gen_random_uuid()"`
	UserID   string       `bun:"user_id"`
	BookID   string       `bun:"book_id"`
	AddedAt  sql.NullTime `bun:"added_at,nullzero,default:now()"`
	Notes    *string      `bun:"notes"`
	Location *string      `bun:"location"`
}

func toEntry(m *entryModel) *library.Entry {
	e := &library.Entry{
		ID:     m.ID,
		UserID: m.UserID,
		BookID: m.BookID,
	}
	if m.AddedAt.Valid {
		e.AddedAt = m.AddedAt.Time
	}
	if m.Notes != nil {
		e.Notes = *m.Notes
	}
	if m.Location != nil {
		e.Location = *m.Location
	}
	return e
}

func fromEntry(e *library.Entry) *entryModel {
	return &entryModel{
		ID:       e.ID,
		UserID:   e.UserID,
		BookID:   e.BookID,
		Notes:    nullableString(e.Notes),
		Location: nullableString(e.Location),
	}
}

func (r *LibraryRepository) Create(ctx context.Context, e *library.Entry) error {
	m := fromEntry(e)
	_, err := r.db.NewInsert().Model(m).Returning("id, added_at").Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return library.ErrDuplicateEntry
		}
		return fmt.Errorf("db: create library entry: %w", err)
	}
	e.ID = m.ID
	if m.AddedAt.Valid {
		e.AddedAt = m.AddedAt.Time
	}
	return nil
}

func (r *LibraryRepository) GetByID(ctx context.Context, id string) (*library.Entry, error) {
	var m entryModel
	err := r.db.NewSelect().Model(&m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, library.ErrNotFound
		}
		return nil, fmt.Errorf("db: get library entry by id: %w", err)
	}
	return toEntry(&m), nil
}

func (r *LibraryRepository) GetByUserAndBook(ctx context.Context, userID, bookID string) (*library.Entry, error) {
	var m entryModel
	err := r.db.NewSelect().Model(&m).Where("user_id = ? AND book_id = ?", userID, bookID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, library.ErrNotFound
		}
		return nil, fmt.Errorf("db: get library entry by user and book: %w", err)
	}
	return toEntry(&m), nil
}

func (r *LibraryRepository) ListByUser(ctx context.Context, userID string) ([]*library.Entry, error) {
	var models []entryModel
	err := r.db.NewSelect().Model(&models).Where("user_id = ?", userID).OrderExpr("added_at DESC").Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: list library entries: %w", err)
	}
	out := make([]*library.Entry, 0, len(models))
	for i := range models {
		out = append(out, toEntry(&models[i]))
	}
	return out, nil
}

func (r *LibraryRepository) Update(ctx context.Context, e *library.Entry) error {
	m := fromEntry(e)
	res, err := r.db.NewUpdate().Model(m).Column("notes", "location").Where("id = ?", m.ID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("db: update library entry: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return library.ErrNotFound
	}
	return nil
}

func (r *LibraryRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.NewDelete().Model((*entryModel)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("db: delete library entry: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return library.ErrNotFound
	}
	return nil
}

var _ library.Repository = (*LibraryRepository)(nil)
