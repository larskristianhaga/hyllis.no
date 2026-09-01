package user

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryRepository is an in-memory Repository implementation. It exists for
// local development without a database configured, mirroring
// book.MemoryRepository's fallback role.
type MemoryRepository struct {
	mu    sync.RWMutex
	users map[string]*User
}

// NewMemoryRepository builds an empty MemoryRepository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{users: make(map[string]*User)}
}

func (r *MemoryRepository) Create(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.users[u.ID]; exists {
		return fmt.Errorf("%w: id %q already exists", ErrDuplicateEmail, u.ID)
	}
	for _, existing := range r.users {
		if existing.Email == u.Email {
			return ErrDuplicateEmail
		}
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	copied := *u
	r.users[u.ID] = &copied
	return nil
}

func (r *MemoryRepository) GetByID(_ context.Context, id string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	u, ok := r.users[id]
	if !ok {
		return nil, fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	copied := *u
	return &copied, nil
}

func (r *MemoryRepository) GetByEmail(_ context.Context, email string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, u := range r.users {
		if u.Email == email {
			copied := *u
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("%w: email %q", ErrNotFound, email)
}

func (r *MemoryRepository) List(_ context.Context) ([]*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*User, 0, len(r.users))
	for _, u := range r.users {
		copied := *u
		out = append(out, &copied)
	}
	return out, nil
}

func (r *MemoryRepository) Update(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.users[u.ID]; !exists {
		return fmt.Errorf("%w: id %q", ErrNotFound, u.ID)
	}
	copied := *u
	r.users[u.ID] = &copied
	return nil
}

func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.users[id]; !exists {
		return fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	delete(r.users, id)
	return nil
}

// Upsert creates u or, if u.ID already exists, refreshes its email/display
// name — see the Repository interface doc comment.
func (r *MemoryRepository) Upsert(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.users[u.ID]
	if !ok {
		copied := *u
		if copied.CreatedAt.IsZero() {
			copied.CreatedAt = time.Now()
		}
		r.users[u.ID] = &copied
		return nil
	}
	existing.Email = u.Email
	existing.DisplayName = u.DisplayName
	return nil
}

var _ Repository = (*MemoryRepository)(nil)
