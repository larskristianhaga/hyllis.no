package library

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a library entry lookup finds no matching row.
var ErrNotFound = errors.New("library: not found")

// ErrDuplicateEntry is returned when creating an entry for a (user, book)
// pair that already exists in the user's library.
var ErrDuplicateEntry = errors.New("library: duplicate entry")

// Repository defines persistence operations for Entry. Implementations are
// expected to live outside this package (e.g. backed by a SQL database).
type Repository interface {
	Create(ctx context.Context, e *Entry) error
	GetByID(ctx context.Context, id string) (*Entry, error)
	GetByUserAndBook(ctx context.Context, userID, bookID string) (*Entry, error)
	ListByUser(ctx context.Context, userID string) ([]*Entry, error)
	Update(ctx context.Context, e *Entry) error
	Delete(ctx context.Context, id string) error
}
