// Package lookup resolves ISBNs to book metadata following the priority
// mandated by the project: a cache first, then a set of external providers
// (Google Books, Open Library, Nasjonalbiblioteket). The cache is always
// checked first and awaited before anything else; on a miss, all providers
// are queried concurrently (to cut latency — waiting on each one in turn is
// slow), but the *result* still respects the documented priority order: if
// several providers hit, the earliest one in the configured list wins,
// exactly as a sequential try-in-order chain would have picked. It has no
// dependency on any concrete storage or transport implementation beyond the
// Cache/Provider interfaces below — callers are responsible for persisting
// the resolved book.
package lookup

import (
	"context"
	"errors"
	"log/slog"
	"sync"

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

// Resolve looks up isbn in the cache first (always awaited before anything
// else), then queries every provider concurrently — sequential round-trips
// to three external APIs is the slow path this avoids. Despite running in
// parallel, the winning result still respects the configured provider
// order: if several providers hit for the same ISBN, the earliest one in
// the list wins, matching what a sequential try-in-order chain would have
// picked. A cache error is treated as a miss (the providers are still
// tried) rather than failing the request. Any hit is best-effort written
// back to the cache before it's returned. If nothing resolves the ISBN,
// Resolve returns ErrNotFound.
func (s *Service) Resolve(ctx context.Context, isbn string) (*book.Book, error) {
	if b, err := s.cache.Get(ctx, isbn); err == nil {
		s.log.Info("lookup: resolved", "source", "cache", "isbn", isbn)
		return b, nil
	} else if !errors.Is(err, ErrNotFound) {
		s.log.Warn("lookup: cache get failed, falling through to providers", "isbn", isbn, "error", err)
	}

	type outcome struct {
		book *book.Book
		err  error
	}

	outcomes := make([]outcome, len(s.providers))
	var wg sync.WaitGroup
	for i, p := range s.providers {
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			s.log.Info("lookup: trying provider", "provider", p.Name(), "isbn", isbn)
			b, err := p.Lookup(ctx, isbn)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					s.log.Info("lookup: provider response", "provider", p.Name(), "isbn", isbn, "result", "not_found")
				} else {
					s.log.Warn("lookup: provider response", "provider", p.Name(), "isbn", isbn, "result", "error", "error", err)
				}
				outcomes[i] = outcome{err: err}
				return
			}
			b.Source = p.Name()
			s.log.Info("lookup: provider response", "provider", p.Name(), "isbn", isbn, "result", "hit", "title", b.Title)
			outcomes[i] = outcome{book: b}
		}(i, p)
	}
	wg.Wait()

	for _, o := range outcomes {
		if o.book == nil {
			continue
		}
		s.log.Info("lookup: resolved", "source", o.book.Source, "isbn", isbn, "title", o.book.Title)
		if err := s.cache.Set(ctx, isbn, o.book); err != nil {
			s.log.Warn("lookup: cache set failed", "isbn", isbn, "error", err)
		}
		return o.book, nil
	}

	s.log.Info("lookup: no provider resolved isbn", "isbn", isbn)
	return nil, ErrNotFound
}
