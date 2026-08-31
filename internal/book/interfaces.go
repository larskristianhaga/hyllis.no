package book

import "context"

// Repository defines persistence operations for Book. Implementations are
// expected to live outside this package (e.g. backed by a SQL database).
type Repository interface {
	Create(ctx context.Context, b *Book) error
	GetByID(ctx context.Context, id string) (*Book, error)
	List(ctx context.Context) ([]*Book, error)
	Update(ctx context.Context, b *Book) error
	Delete(ctx context.Context, id string) error
}

// Service defines the business-logic operations for books, built on top of
// a Repository. Implementations are expected to live outside this package.
type Service interface {
	Create(ctx context.Context, b *Book) (*Book, error)
	Get(ctx context.Context, id string) (*Book, error)
	List(ctx context.Context) ([]*Book, error)
	Update(ctx context.Context, b *Book) (*Book, error)
	Delete(ctx context.Context, id string) error
}
