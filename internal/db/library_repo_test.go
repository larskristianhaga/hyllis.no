package db

import (
	"context"
	"errors"
	"testing"

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/user"
)

func TestLibraryRepository_CreateGetUpdateDelete(t *testing.T) {
	tx := withTx(t)
	books := NewBookRepository(tx)
	users := NewUserRepository(tx)
	entries := NewLibraryRepository(tx)
	ctx := context.Background()

	u := &user.User{Email: "leser@example.no", DisplayName: "Leser", PasswordHash: "hash"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	b := &book.Book{ISBN: "9780000000020", Title: "Kristin Lavransdatter", Author: "Sigrid Undset", Source: "manual"}
	if err := books.Create(ctx, b); err != nil {
		t.Fatalf("create book: %v", err)
	}

	e := &library.Entry{UserID: u.ID, BookID: b.ID, Notes: "Julegave", Location: "Stue"}
	if err := entries.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.ID == "" {
		t.Fatal("Create did not populate ID")
	}

	got, err := entries.GetByUserAndBook(ctx, u.ID, b.ID)
	if err != nil {
		t.Fatalf("GetByUserAndBook: %v", err)
	}
	if got.ID != e.ID {
		t.Fatalf("GetByUserAndBook id = %q, want %q", got.ID, e.ID)
	}

	list, err := entries.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByUser returned %d entries, want 1", len(list))
	}

	e.Location = "Kontor"
	if err := entries.Update(ctx, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = entries.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Location != "Kontor" {
		t.Fatalf("GetByID after update location = %q, want %q", got.Location, "Kontor")
	}

	if err := entries.Delete(ctx, e.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := entries.GetByID(ctx, e.ID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want library.ErrNotFound", err)
	}
}

func TestLibraryRepository_DuplicateUserBookIsRejected(t *testing.T) {
	tx := withTx(t)
	books := NewBookRepository(tx)
	users := NewUserRepository(tx)
	entries := NewLibraryRepository(tx)
	ctx := context.Background()

	u := &user.User{Email: "duplikat@example.no", DisplayName: "Duplikat", PasswordHash: "hash"}
	if err := users.Create(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	b := &book.Book{ISBN: "9780000000021", Title: "Duplikatboken", Author: "En Forfatter", Source: "manual"}
	if err := books.Create(ctx, b); err != nil {
		t.Fatalf("create book: %v", err)
	}

	first := &library.Entry{UserID: u.ID, BookID: b.ID}
	if err := entries.Create(ctx, first); err != nil {
		t.Fatalf("Create first entry: %v", err)
	}

	second := &library.Entry{UserID: u.ID, BookID: b.ID}
	if err := entries.Create(ctx, second); !errors.Is(err, library.ErrDuplicateEntry) {
		t.Fatalf("Create duplicate (user_id, book_id) = %v, want library.ErrDuplicateEntry", err)
	}
}

var _ library.Repository = (*LibraryRepository)(nil)
