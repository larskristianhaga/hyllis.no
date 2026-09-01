package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/larskristianhaga/hyllis.no/internal/user"
)

// UserRepository is a Postgres-backed implementation of user.Repository,
// built on the Bun ORM.
type UserRepository struct {
	db dbtx
}

// NewUserRepository builds a UserRepository. db is typically a *bun.DB in
// production or a bun.Tx in tests.
func NewUserRepository(db dbtx) *UserRepository {
	return &UserRepository{db: db}
}

// userModel is Bun's row-mapped view of the users table. See bookModel's
// doc comment for why this is kept separate from user.User.
type userModel struct {
	bun.BaseModel `bun:"table:users"`

	ID          string       `bun:"id,pk,nullzero,default:gen_random_uuid()"`
	Email       string       `bun:"email"`
	DisplayName string       `bun:"display_name"`
	CreatedAt   sql.NullTime `bun:"created_at,nullzero,default:now()"`
}

func toUser(m *userModel) *user.User {
	u := &user.User{
		ID:          m.ID,
		Email:       m.Email,
		DisplayName: m.DisplayName,
	}
	if m.CreatedAt.Valid {
		u.CreatedAt = m.CreatedAt.Time
	}
	return u
}

func fromUser(u *user.User) *userModel {
	return &userModel{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
	}
}

func (r *UserRepository) Create(ctx context.Context, u *user.User) error {
	m := fromUser(u)
	_, err := r.db.NewInsert().Model(m).Returning("id, created_at").Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return user.ErrDuplicateEmail
		}
		return fmt.Errorf("db: create user: %w", err)
	}
	u.ID = m.ID
	if m.CreatedAt.Valid {
		u.CreatedAt = m.CreatedAt.Time
	}
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*user.User, error) {
	var m userModel
	err := r.db.NewSelect().Model(&m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, user.ErrNotFound
		}
		return nil, fmt.Errorf("db: get user by id: %w", err)
	}
	return toUser(&m), nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*user.User, error) {
	var m userModel
	err := r.db.NewSelect().Model(&m).Where("email = ?", email).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, user.ErrNotFound
		}
		return nil, fmt.Errorf("db: get user by email: %w", err)
	}
	return toUser(&m), nil
}

func (r *UserRepository) List(ctx context.Context) ([]*user.User, error) {
	var models []userModel
	if err := r.db.NewSelect().Model(&models).OrderExpr("created_at DESC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("db: list users: %w", err)
	}
	out := make([]*user.User, 0, len(models))
	for i := range models {
		out = append(out, toUser(&models[i]))
	}
	return out, nil
}

func (r *UserRepository) Update(ctx context.Context, u *user.User) error {
	m := fromUser(u)
	res, err := r.db.NewUpdate().Model(m).Column("email", "display_name").Where("id = ?", m.ID).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return user.ErrDuplicateEmail
		}
		return fmt.Errorf("db: update user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return user.ErrNotFound
	}
	return nil
}

// Upsert creates u or, if u.ID already exists, refreshes its email/display
// name — see the Repository interface doc comment for why this exists
// instead of a plain Create call on every login.
func (r *UserRepository) Upsert(ctx context.Context, u *user.User) error {
	m := fromUser(u)
	_, err := r.db.NewInsert().
		Model(m).
		On("CONFLICT (id) DO UPDATE").
		Set("email = EXCLUDED.email").
		Set("display_name = EXCLUDED.display_name").
		Returning("created_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("db: upsert user: %w", err)
	}
	if m.CreatedAt.Valid {
		u.CreatedAt = m.CreatedAt.Time
	}
	return nil
}

func (r *UserRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.NewDelete().Model((*userModel)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("db: delete user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return user.ErrNotFound
	}
	return nil
}

var _ user.Repository = (*UserRepository)(nil)
