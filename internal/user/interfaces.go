package user

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a user lookup finds no matching row.
var ErrNotFound = errors.New("user: not found")

// ErrDuplicateEmail is returned when creating a user whose email already
// exists.
var ErrDuplicateEmail = errors.New("user: duplicate email")

// Repository defines persistence operations for User. Implementations are
// expected to live outside this package (e.g. backed by a SQL database).
type Repository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context) ([]*User, error)
	Update(ctx context.Context, u *User) error
	Delete(ctx context.Context, id string) error
	// Upsert creates u or, if a row with u.ID already exists, refreshes its
	// Email/DisplayName. Called on every successful Supabase login/signup to
	// keep this local profile mirror in sync with Supabase's own user
	// record, since Go never owns the signup flow itself.
	Upsert(ctx context.Context, u *User) error
}
