package lookup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

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

func TestService_Resolve_FallsThroughToNextProvider(t *testing.T) {
	cache := fakeCache{}
	var calledOrder []string

	first := fakeProvider{name: "google_books", lookup: func(context.Context, string) (*book.Book, error) {
		calledOrder = append(calledOrder, "google_books")
		return nil, ErrNotFound
	}}
	second := fakeProvider{name: "open_library", lookup: func(context.Context, string) (*book.Book, error) {
		calledOrder = append(calledOrder, "open_library")
		return nil, errors.New("boom") // non-ErrNotFound error should still fall through
	}}
	third := fakeProvider{name: "nb", lookup: func(context.Context, string) (*book.Book, error) {
		calledOrder = append(calledOrder, "nb")
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
	if want := []string{"google_books", "open_library", "nb"}; !equalStrings(calledOrder, want) {
		t.Fatalf("provider call order = %v, want %v", calledOrder, want)
	}
	if cached == nil || cached.Source != "nb" {
		t.Fatalf("cache.Set was not called with the resolved book (got %+v)", cached)
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

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var _ Cache = fakeCache{}
var _ Provider = fakeProvider{}
