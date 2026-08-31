package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/larskristianhaga/hyllis.no/internal/user"
)

// UserRepository is a Postgres-backed implementation of user.Repository.
type UserRepository struct {
	db dbtx
}

// NewUserRepository builds a UserRepository. db is typically a
// *pgxpool.Pool in production or a pgx.Tx in tests.
func NewUserRepository(db dbtx) *UserRepository {
	return &UserRepository{db: db}
}

const userColumns = "id, email, display_name, password_hash, created_at"

func scanUser(row scanner) (*user.User, error) {
	var u user.User
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) Create(ctx context.Context, u *user.User) error {
	const q = `INSERT INTO users (email, display_name, password_hash)
	           VALUES ($1, $2, $3)
	           RETURNING id, created_at`
	err := r.db.QueryRow(ctx, q, u.Email, u.DisplayName, u.PasswordHash).Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return user.ErrDuplicateEmail
		}
		return fmt.Errorf("db: create user: %w", err)
	}
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	u, err := scanUser(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, user.ErrNotFound
		}
		return nil, fmt.Errorf("db: get user by id: %w", err)
	}
	return u, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = $1`
	u, err := scanUser(r.db.QueryRow(ctx, q, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, user.ErrNotFound
		}
		return nil, fmt.Errorf("db: get user by email: %w", err)
	}
	return u, nil
}

func (r *UserRepository) List(ctx context.Context) ([]*user.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("db: list users: %w", err)
	}
	defer rows.Close()

	var out []*user.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *UserRepository) Update(ctx context.Context, u *user.User) error {
	const q = `UPDATE users SET email=$2, display_name=$3, password_hash=$4 WHERE id=$1`
	tag, err := r.db.Exec(ctx, q, u.ID, u.Email, u.DisplayName, u.PasswordHash)
	if err != nil {
		if isUniqueViolation(err) {
			return user.ErrDuplicateEmail
		}
		return fmt.Errorf("db: update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return user.ErrNotFound
	}
	return nil
}

func (r *UserRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id=$1`
	tag, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("db: delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return user.ErrNotFound
	}
	return nil
}

var _ user.Repository = (*UserRepository)(nil)
