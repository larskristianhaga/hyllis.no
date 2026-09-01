package library

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryRepository is an in-memory Repository implementation. It exists for
// local development without a database configured, mirroring
// book.MemoryRepository's fallback role.
type MemoryRepository struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// NewMemoryRepository builds an empty MemoryRepository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{entries: make(map[string]*Entry)}
}

func (r *MemoryRepository) Create(_ context.Context, e *Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.entries {
		if existing.UserID == e.UserID && existing.BookID == e.BookID {
			return ErrDuplicateEntry
		}
	}

	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.AddedAt.IsZero() {
		e.AddedAt = time.Now()
	}
	copied := *e
	r.entries[e.ID] = &copied
	return nil
}

func (r *MemoryRepository) GetByID(_ context.Context, id string) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.entries[id]
	if !ok {
		return nil, fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	copied := *e
	return &copied, nil
}

func (r *MemoryRepository) GetByUserAndBook(_ context.Context, userID, bookID string) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.entries {
		if e.UserID == userID && e.BookID == bookID {
			copied := *e
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("%w: user %q book %q", ErrNotFound, userID, bookID)
}

func (r *MemoryRepository) ListByUser(_ context.Context, userID string) ([]*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		if e.UserID == userID {
			copied := *e
			out = append(out, &copied)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].AddedAt.Equal(out[j].AddedAt) {
			return out[i].AddedAt.After(out[j].AddedAt)
		}
		return out[i].BookID < out[j].BookID
	})
	return out, nil
}

func (r *MemoryRepository) Update(_ context.Context, e *Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[e.ID]; !exists {
		return fmt.Errorf("%w: id %q", ErrNotFound, e.ID)
	}
	copied := *e
	r.entries[e.ID] = &copied
	return nil
}

func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[id]; !exists {
		return fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	delete(r.entries, id)
	return nil
}

var _ Repository = (*MemoryRepository)(nil)
