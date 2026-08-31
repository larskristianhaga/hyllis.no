// Package lookup resolves ISBNs to book metadata following the order
// mandated by the project: a cache, then a chain of external providers
// (Google Books, Open Library, Nasjonalbiblioteket), falling through to the
// next on any miss or error. It has no dependency on any concrete storage
// or transport implementation beyond the Cache/Provider interfaces below —
// callers are responsible for persisting the resolved book.
package lookup

import (
	"context"
	"errors"
	"log/slog"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// ErrNotFound is returned by Cache.Get and Provider.Lookup on a miss, and by
// Service.Resolve when the cache and every provider miss (or fail) for an
// ISBN — the caller should offer manual entry at that point.
var ErrNotFound = errors.New("lookup: not found")

// Provider resolves a single ISBN against one external source of book
// metadata. Implementations should return ErrNotFound (not a wrapped HTTP
// error) for a clean miss, so Service can fall through to the next
// provider without treating it as noteworthy.
type Provider interface {
	// Name identifies the provider; it's stored on the resolved Book's
	// Source field, so it must be one of the values the books.source
	// check constraint allows ("google_books", "open_library", "nb").
	Name() string
	Lookup(ctx context.Context, isbn string) (*book.Book, error)
}

// Cache is the ISBN metadata cache (Redis in production). Implementations
// don't need to expire entries — resolved book metadata is treated as
// practically immutable.
type Cache interface {
	Get(ctx context.Context, isbn string) (*book.Book, error)
	Set(ctx context.Context, isbn string, b *book.Book) error
}

// Service resolves ISBNs by checking the cache, then trying each provider
// in order.
type Service struct {
	cache     Cache
	providers []Provider
	log       *slog.Logger
}

// NewService builds a Service. providers are tried in the given order.
func NewService(cache Cache, providers []Provider, log *slog.Logger) *Service {
	return &Service{cache: cache, providers: providers, log: log}
}

// Resolve looks up isbn in the cache, then in each provider in order,
// returning the first hit. A cache error is treated as a miss (the
// providers are still tried) rather than failing the request. Any hit is
// best-effort written back to the cache before it's returned. If nothing
// resolves the ISBN, Resolve returns ErrNotFound.
func (s *Service) Resolve(ctx context.Context, isbn string) (*book.Book, error) {
	if b, err := s.cache.Get(ctx, isbn); err == nil {
		return b, nil
	} else if !errors.Is(err, ErrNotFound) {
		s.log.Warn("lookup: cache get failed, falling through to providers", "isbn", isbn, "error", err)
	}

	for _, p := range s.providers {
		b, err := p.Lookup(ctx, isbn)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				s.log.Warn("lookup: provider failed, trying next", "provider", p.Name(), "isbn", isbn, "error", err)
			}
			continue
		}

		b.Source = p.Name()
		if err := s.cache.Set(ctx, isbn, b); err != nil {
			s.log.Warn("lookup: cache set failed", "isbn", isbn, "error", err)
		}
		return b, nil
	}

	return nil, ErrNotFound
}
