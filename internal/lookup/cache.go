package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// cacheKeyPrefix namespaces every ISBN cache entry, per the project's
// convention of keying resolved metadata as "isbn:<isbn13>".
const cacheKeyPrefix = "isbn:"

func cacheKey(isbn string) string {
	return cacheKeyPrefix + isbn
}

// RedisCache is a Cache backed by Redis. It stores each resolved Book as
// JSON with no expiry — resolved ISBN metadata is shared across every user
// and practically immutable, so there's nothing to invalidate.
type RedisCache struct {
	client redis.UniversalClient
}

// NewRedisCache builds a RedisCache around an already-connected client.
func NewRedisCache(client redis.UniversalClient) *RedisCache {
	return &RedisCache{client: client}
}

func (c *RedisCache) Get(ctx context.Context, isbn string) (*book.Book, error) {
	raw, err := c.client.Get(ctx, cacheKey(isbn)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lookup: redis get: %w", err)
	}

	var b book.Book
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("lookup: unmarshal cached book: %w", err)
	}
	return &b, nil
}

func (c *RedisCache) Set(ctx context.Context, isbn string, b *book.Book) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("lookup: marshal book for cache: %w", err)
	}
	// 0 means no expiry — see the immutability rationale on RedisCache.
	if err := c.client.Set(ctx, cacheKey(isbn), raw, 0).Err(); err != nil {
		return fmt.Errorf("lookup: redis set: %w", err)
	}
	return nil
}

// NoopCache never caches anything. It's used when REDIS_URL isn't
// configured (e.g. local development) so lookups still work end to end via
// the providers, just without the caching layer.
type NoopCache struct{}

func (NoopCache) Get(context.Context, string) (*book.Book, error) { return nil, ErrNotFound }
func (NoopCache) Set(context.Context, string, *book.Book) error   { return nil }

var (
	_ Cache = (*RedisCache)(nil)
	_ Cache = NoopCache{}
)
