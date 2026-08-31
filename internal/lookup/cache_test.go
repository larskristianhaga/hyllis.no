package lookup

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// newTestRedisCache starts an in-process fake Redis server (no real network
// or Docker needed) and returns a RedisCache backed by it.
func newTestRedisCache(t *testing.T) *RedisCache {
	t.Helper()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisCache(client)
}

func TestRedisCache_SetThenGet(t *testing.T) {
	cache := newTestRedisCache(t)
	ctx := context.Background()

	want := &book.Book{ISBN: "9780143127550", Title: "The Hobbit", Author: "J.R.R. Tolkien", Source: "google_books"}
	if err := cache.Set(ctx, want.ISBN, want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := cache.Get(ctx, want.ISBN)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != want.Title || got.Author != want.Author || got.Source != want.Source {
		t.Fatalf("Get returned %+v, want %+v", got, want)
	}
}

func TestRedisCache_Get_Miss(t *testing.T) {
	cache := newTestRedisCache(t)

	_, err := cache.Get(context.Background(), "0000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestNoopCache(t *testing.T) {
	var cache NoopCache
	ctx := context.Background()

	if err := cache.Set(ctx, "123", &book.Book{Title: "x"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, err := cache.Get(ctx, "123")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}
