package book

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryRepository is an in-memory Repository implementation. It exists for
// local development and template/handler work before a real database is
// wired up, and is safe for concurrent use.
type MemoryRepository struct {
	mu    sync.RWMutex
	books map[string]*Book
}

// NewMemoryRepository builds a MemoryRepository seeded with the given books.
// Each seed book is copied so later mutation of the caller's slice can't
// affect the repository's state.
func NewMemoryRepository(seed []*Book) *MemoryRepository {
	books := make(map[string]*Book, len(seed))
	for _, b := range seed {
		copied := *b
		books[b.ID] = &copied
	}
	return &MemoryRepository{books: books}
}

func (r *MemoryRepository) Create(_ context.Context, b *Book) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.books[b.ID]; exists {
		return fmt.Errorf("book with id %q already exists", b.ID)
	}
	copied := *b
	r.books[b.ID] = &copied
	return nil
}

func (r *MemoryRepository) GetByID(_ context.Context, id string) (*Book, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	b, ok := r.books[id]
	if !ok {
		return nil, fmt.Errorf("book with id %q not found", id)
	}
	copied := *b
	return &copied, nil
}

func (r *MemoryRepository) List(_ context.Context) ([]*Book, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Book, 0, len(r.books))
	for _, b := range r.books {
		copied := *b
		out = append(out, &copied)
	}
	return out, nil
}

func (r *MemoryRepository) Update(_ context.Context, b *Book) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.books[b.ID]; !exists {
		return fmt.Errorf("book with id %q not found", b.ID)
	}
	copied := *b
	r.books[b.ID] = &copied
	return nil
}

func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.books[id]; !exists {
		return fmt.Errorf("book with id %q not found", id)
	}
	delete(r.books, id)
	return nil
}

// SeedBooks returns a fixed set of mock books for local development and
// template rendering. IDs are set equal to the ISBN so a scanned EAN-13
// barcode can be used directly as a lookup key without widening the
// Repository contract.
func SeedBooks() []*Book {
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	books := []*Book{
		{ISBN: "9788203293176", Title: "Sult", Author: "Knut Hamsun", CreatedAt: day(2025, time.January, 12)},
		{ISBN: "9788203365117", Title: "Fuglane", Author: "Tarjei Vesaas", CreatedAt: day(2025, time.February, 3)},
		{ISBN: "9780143127550", Title: "The Hobbit", Author: "J.R.R. Tolkien", CreatedAt: day(2025, time.March, 21)},
		{ISBN: "9780451524935", Title: "1984", Author: "George Orwell", CreatedAt: day(2025, time.April, 9)},
		{ISBN: "9780061120084", Title: "To Kill a Mockingbird", Author: "Harper Lee", CreatedAt: day(2025, time.May, 17)},
		{ISBN: "9780544003415", Title: "The Lord of the Rings", Author: "J.R.R. Tolkien", CreatedAt: day(2025, time.June, 30)},
		{ISBN: "9788205442104", Title: "Bikubesong", Author: "Frode Grytten", CreatedAt: day(2025, time.July, 22)},
		{ISBN: "9780316769488", Title: "The Catcher in the Rye", Author: "J.D. Salinger", CreatedAt: day(2025, time.August, 14)},
	}

	for _, b := range books {
		b.ID = b.ISBN
		b.UpdatedAt = b.CreatedAt
	}
	return books
}
