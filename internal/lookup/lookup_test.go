package lookup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// fakeCache and fakeProvider let Service's orchestration logic be tested
// without any real cache or network dependency.

type fakeCache struct {
	get func(ctx context.Context, isbn string) (*book.Book, error)
	set func(ctx context.Context, isbn string, b *book.Book) error
}

func (f fakeCache) Get(ctx context.Context, isbn string) (*book.Book, error) {
	if f.get == nil {
		return nil, ErrNotFound
	}
	return f.get(ctx, isbn)
}

func (f fakeCache) Set(ctx context.Context, isbn string, b *book.Book) error {
	if f.set == nil {
		return nil
	}
	return f.set(ctx, isbn, b)
}

type fakeProvider struct {
	name   string
	lookup func(ctx context.Context, isbn string) (*book.Book, error)
}

func (f fakeProvider) Name() string { return f.name }

func (f fakeProvider) Lookup(ctx context.Context, isbn string) (*book.Book, error) {
	return f.lookup(ctx, isbn)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestService_Resolve_CacheHit(t *testing.T) {
	cache := fakeCache{get: func(context.Context, string) (*book.Book, error) {
		return &book.Book{ISBN: "123", Title: "Cached"}, nil
	}}
	providerCalled := false
	provider := fakeProvider{name: "google_books", lookup: func(context.Context, string) (*book.Book, error) {
		providerCalled = true
		return nil, ErrNotFound
	}}

	svc := NewService(cache, []Provider{provider}, testLogger())
	b, err := svc.Resolve(context.Background(), "123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if b.Title != "Cached" {
		t.Fatalf("Resolve returned %+v, want cached book", b)
	}
	if providerCalled {
		t.Fatal("Resolve called a provider despite a cache hit")
	}
}

// TestService_Resolve_AllProvidersQueriedAndPriorityWins covers the
// parallel-query behaviour: every provider is called (concurrently, not
// one-at-a-time), and when multiple could resolve the ISBN, the earliest
// one in the configured provider list wins regardless of which goroutine
// happens to finish first — matching what a sequential try-in-order chain
// would have picked.
func TestService_Resolve_AllProvidersQueriedAndPriorityWins(t *testing.T) {
	cache := fakeCache{}
	var mu sync.Mutex
	called := map[string]bool{}
	markCalled := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		called[name] = true
	}

	first := fakeProvider{name: "google_books", lookup: func(context.Context, string) (*book.Book, error) {
		markCalled("google_books")
		return nil, ErrNotFound
	}}
	second := fakeProvider{name: "open_library", lookup: func(context.Context, string) (*book.Book, error) {
		markCalled("open_library")
		return nil, errors.New("boom") // non-ErrNotFound error should still fall through
	}}
	third := fakeProvider{name: "nb", lookup: func(context.Context, string) (*book.Book, error) {
		markCalled("nb")
		return &book.Book{ISBN: "123", Title: "Funnet på NB"}, nil
	}}

	var cached *book.Book
	cache.set = func(_ context.Context, isbn string, b *book.Book) error {
		cached = b
		return nil
	}

	svc := NewService(cache, []Provider{first, second, third}, testLogger())
	b, err := svc.Resolve(context.Background(), "123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if b.Title != "Funnet på NB" || b.Source != "nb" {
		t.Fatalf("Resolve returned %+v, want NB hit with Source set", b)
	}
	for _, name := range []string{"google_books", "open_library", "nb"} {
		if !called[name] {
			t.Fatalf("provider %q was not called", name)
		}
	}
	if cached == nil || cached.Source != "nb" {
		t.Fatalf("cache.Set was not called with the resolved book (got %+v)", cached)
	}
}

// TestService_Resolve_ProvidersRunConcurrently proves providers are queried
// in parallel rather than one at a time: it blocks each provider on a
// shared gate and requires all of them to have started before any is
// released, which would deadlock (and fail via the timeout) under the old
// sequential implementation.
func TestService_Resolve_ProvidersRunConcurrently(t *testing.T) {
	const n = 3
	started := make(chan struct{}, n)
	release := make(chan struct{})

	mk := func(name string) fakeProvider {
		return fakeProvider{name: name, lookup: func(ctx context.Context, _ string) (*book.Book, error) {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return nil, ErrNotFound
		}}
	}

	svc := NewService(fakeCache{}, []Provider{mk("google_books"), mk("open_library"), mk("nb")}, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = svc.Resolve(ctx, "123")
		close(done)
	}()

	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d/%d providers had started concurrently before timeout", i, n)
		}
	}
	close(release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Resolve did not return after providers were released")
	}
}

func TestService_Resolve_AllMiss(t *testing.T) {
	cache := fakeCache{}
	miss := fakeProvider{name: "google_books", lookup: func(context.Context, string) (*book.Book, error) {
		return nil, ErrNotFound
	}}

	svc := NewService(cache, []Provider{miss, miss}, testLogger())
	_, err := svc.Resolve(context.Background(), "123")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %v, want ErrNotFound", err)
	}
}

var _ Cache = fakeCache{}
var _ Provider = fakeProvider{}
