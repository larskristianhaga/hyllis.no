package book

import (
	"context"
	"fmt"
	"sort"
	"strings"
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

// Create assigns b.ID/b.CreatedAt when unset, mirroring how the real
// Postgres-backed repository generates these via `RETURNING id, created_at`.
// The ID is set equal to the ISBN, matching SeedBooks' convention so a
// scanned EAN-13 barcode can be used directly as a lookup key.
func (r *MemoryRepository) Create(_ context.Context, b *Book) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if b.ID == "" {
		b.ID = b.ISBN
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now()
	}

	if _, exists := r.books[b.ID]; exists {
		return ErrDuplicateISBN
	}
	for _, existing := range r.books {
		if existing.ISBN == b.ISBN {
			return ErrDuplicateISBN
		}
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
		return nil, fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	copied := *b
	return &copied, nil
}

func (r *MemoryRepository) GetByISBN(_ context.Context, isbn string) (*Book, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, b := range r.books {
		if b.ISBN == isbn {
			copied := *b
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("%w: isbn %q", ErrNotFound, isbn)
}

func (r *MemoryRepository) List(_ context.Context) ([]*Book, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Book, 0, len(r.books))
	for _, b := range r.books {
		copied := *b
		out = append(out, &copied)
	}
	sortBooksNewestFirst(out)
	return out, nil
}

// Search returns books whose title or author contains query, matched
// case-insensitively. It's a simple substring stand-in for the trigram
// similarity search the Postgres-backed repository performs.
func (r *MemoryRepository) Search(_ context.Context, query string) ([]*Book, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	needle := strings.ToLower(query)
	out := make([]*Book, 0, len(r.books))
	for _, b := range r.books {
		if strings.Contains(strings.ToLower(b.Title), needle) || strings.Contains(strings.ToLower(b.Author), needle) {
			copied := *b
			out = append(out, &copied)
		}
	}
	sortBooksNewestFirst(out)
	return out, nil
}

// sortBooksNewestFirst orders books by CreatedAt descending, breaking ties
// on ISBN. Iterating r.books (a map) has randomized order in Go, so without
// this every List/Search call would return books in a different order —
// this matches the deterministic "created_at DESC" ordering the
// Postgres-backed repository uses.
func sortBooksNewestFirst(books []*Book) {
	sort.Slice(books, func(i, j int) bool {
		if !books[i].CreatedAt.Equal(books[j].CreatedAt) {
			return books[i].CreatedAt.After(books[j].CreatedAt)
		}
		return books[i].ISBN < books[j].ISBN
	})
}

func (r *MemoryRepository) Update(_ context.Context, b *Book) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.books[b.ID]; !exists {
		return fmt.Errorf("%w: id %q", ErrNotFound, b.ID)
	}
	copied := *b
	r.books[b.ID] = &copied
	return nil
}

func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.books[id]; !exists {
		return fmt.Errorf("%w: id %q", ErrNotFound, id)
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
		{ISBN: "9788203293176", Title: "Sult", Author: "Knut Hamsun", Publisher: "Gyldendal", Year: 1890, Language: "no", Pages: 224, Source: "manual", CreatedAt: day(2025, time.January, 12)},
		{ISBN: "9788203365117", Title: "Fuglane", Author: "Tarjei Vesaas", Publisher: "Gyldendal", Year: 1957, Language: "no", Pages: 224, Source: "manual", CreatedAt: day(2025, time.February, 3)},
		{ISBN: "9780143127550", Title: "The Hobbit", Author: "J.R.R. Tolkien", Publisher: "Penguin Classics", Year: 1937, Language: "en", Pages: 310, Source: "manual", CreatedAt: day(2025, time.March, 21)},
		{ISBN: "9780451524935", Title: "1984", Author: "George Orwell", Publisher: "Signet Classics", Year: 1949, Language: "en", Pages: 328, Source: "manual", CreatedAt: day(2025, time.April, 9)},
		{ISBN: "9780061120084", Title: "To Kill a Mockingbird", Author: "Harper Lee", Publisher: "Harper Perennial", Year: 1960, Language: "en", Pages: 336, Source: "manual", CreatedAt: day(2025, time.May, 17)},
		{ISBN: "9780544003415", Title: "The Lord of the Rings", Author: "J.R.R. Tolkien", Publisher: "Houghton Mifflin", Year: 1954, Language: "en", Pages: 1178, Source: "manual", CreatedAt: day(2025, time.June, 30)},
		{ISBN: "9788205442104", Title: "Bikubesong", Author: "Frode Grytten", Publisher: "Cappelen Damm", Year: 1999, Language: "no", Pages: 256, Source: "manual", CreatedAt: day(2025, time.July, 22)},
		{ISBN: "9780316769488", Title: "The Catcher in the Rye", Author: "J.D. Salinger", Publisher: "Little, Brown and Company", Year: 1951, Language: "en", Pages: 224, Source: "manual", CreatedAt: day(2025, time.August, 14)},
	}

	for _, b := range books {
		b.ID = b.ISBN
	}
	return books
}
