package db

import (
	"context"
	"errors"
	"testing"

	"github.com/larskristianhaga/hyllis.no/internal/user"
)

func TestUserRepository_CreateGetUpdateDelete(t *testing.T) {
	tx := withTx(t)
	repo := NewUserRepository(tx)
	ctx := context.Background()

	u := &user.User{Email: "ada@example.no", DisplayName: "Ada", PasswordHash: "hash1"}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if u.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}

	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Email != u.Email {
		t.Fatalf("GetByID email = %q, want %q", got.Email, u.Email)
	}

	got, err = repo.GetByEmail(ctx, u.Email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("GetByEmail id = %q, want %q", got.ID, u.ID)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d users, want 1", len(list))
	}

	u.DisplayName = "Ada Lovelace"
	if err := repo.Update(ctx, u); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.DisplayName != "Ada Lovelace" {
		t.Fatalf("GetByID after update display name = %q, want %q", got.DisplayName, "Ada Lovelace")
	}

	if err := repo.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, u.ID); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want user.ErrNotFound", err)
	}
}

func TestUserRepository_CreateDuplicateEmail(t *testing.T) {
	tx := withTx(t)
	repo := NewUserRepository(tx)
	ctx := context.Background()

	u1 := &user.User{Email: "dobbel@example.no", DisplayName: "Person A", PasswordHash: "hash1"}
	if err := repo.Create(ctx, u1); err != nil {
		t.Fatalf("Create first user: %v", err)
	}

	u2 := &user.User{Email: "dobbel@example.no", DisplayName: "Person B", PasswordHash: "hash2"}
	if err := repo.Create(ctx, u2); !errors.Is(err, user.ErrDuplicateEmail) {
		t.Fatalf("Create duplicate email = %v, want user.ErrDuplicateEmail", err)
	}
}

var _ user.Repository = (*UserRepository)(nil)
