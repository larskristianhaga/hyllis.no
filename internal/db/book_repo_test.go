package db

import (
	"context"
	"errors"
	"testing"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

func TestBookRepository_CreateGetListUpdateDelete(t *testing.T) {
	tx := withTx(t)
	repo := NewBookRepository(tx)
	ctx := context.Background()

	b := &book.Book{
		ISBN:     "9780000000001",
		Title:    "Testing i praksis",
		Author:   "Ada Testesen",
		Year:     2024,
		Language: "no",
		Pages:    200,
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if b.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}

	got, err := repo.GetByID(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Title != b.Title || got.ISBN != b.ISBN {
		t.Fatalf("GetByID returned %+v, want title/isbn matching %+v", got, b)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d books, want 1", len(list))
	}

	b.Title = "Testing i praksis, 2. utgave"
	if err := repo.Update(ctx, b); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.GetByID(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Title != b.Title {
		t.Fatalf("GetByID after update = %q, want %q", got.Title, b.Title)
	}

	if err := repo.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, b.ID); !errors.Is(err, book.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want book.ErrNotFound", err)
	}
}

func TestBookRepository_GetByISBN(t *testing.T) {
	tx := withTx(t)
	repo := NewBookRepository(tx)
	ctx := context.Background()

	b := &book.Book{ISBN: "9780000000002", Title: "Fjellet og fjorden", Author: "Ola Nordmann"}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByISBN(ctx, b.ISBN)
	if err != nil {
		t.Fatalf("GetByISBN: %v", err)
	}
	if got.ID != b.ID {
		t.Fatalf("GetByISBN returned id %q, want %q", got.ID, b.ID)
	}

	if _, err := repo.GetByISBN(ctx, "9999999999999"); !errors.Is(err, book.ErrNotFound) {
		t.Fatalf("GetByISBN for unknown isbn = %v, want book.ErrNotFound", err)
	}
}

func TestBookRepository_CreateDuplicateISBN(t *testing.T) {
	tx := withTx(t)
	repo := NewBookRepository(tx)
	ctx := context.Background()

	b1 := &book.Book{ISBN: "9780000000003", Title: "Første bok", Author: "Forfatter A"}
	if err := repo.Create(ctx, b1); err != nil {
		t.Fatalf("Create first book: %v", err)
	}

	b2 := &book.Book{ISBN: "9780000000003", Title: "Andre bok", Author: "Forfatter B"}
	if err := repo.Create(ctx, b2); !errors.Is(err, book.ErrDuplicateISBN) {
		t.Fatalf("Create duplicate isbn = %v, want book.ErrDuplicateISBN", err)
	}
}

func TestBookRepository_Search(t *testing.T) {
	tx := withTx(t)
	repo := NewBookRepository(tx)
	ctx := context.Background()

	seed := []*book.Book{
		{ISBN: "9780000000010", Title: "Harry Potter og de vises stein", Author: "J.K. Rowling"},
		{ISBN: "9780000000011", Title: "Harry Potter og kammeret hemmelighet", Author: "J.K. Rowling"},
		{ISBN: "9780000000012", Title: "Snømannen", Author: "Jo Nesbø"},
	}
	for _, b := range seed {
		if err := repo.Create(ctx, b); err != nil {
			t.Fatalf("seed Create: %v", err)
		}
	}

	// Deliberately misspelled (one dropped letter) to exercise trigram
	// fuzzy matching rather than an exact/substring match. pg_trgm's
	// similarity() compares full-string trigram sets, so a short query like
	// "Hary Poter" against a much longer title falls below the default
	// 0.3 threshold even though it's an obvious human typo — a near-full
	// typo'd title is the realistic/testable case for the % operator.
	const query = "Harry Poter og de vises stein"
	results, err := repo.Search(ctx, query)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search for a typo'd title returned no fuzzy matches")
	}
	for _, r := range results {
		if r.Author == "Jo Nesbø" {
			t.Errorf("Search for %q unexpectedly matched %q", query, r.Title)
		}
	}
}

var _ book.Repository = (*BookRepository)(nil)
