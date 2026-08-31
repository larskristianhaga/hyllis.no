// Command hyllis is the HTTP entrypoint for hyllis.no.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/server"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// .env is for local development only (gitignored, never present in the
	// Docker image or on Fly, which inject real env vars/secrets directly).
	// A missing file is expected in those environments, so the error is
	// intentionally ignored rather than failing startup.
	if err := godotenv.Load(); err == nil {
		logger.Info("loaded .env")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	render, err := web.New()
	if err != nil {
		logger.Error("failed to build template renderer", "error", err)
		os.Exit(1)
	}

	books := book.NewMemoryRepository(book.SeedBooks())

	cache, err := newISBNCache(context.Background(), logger)
	if err != nil {
		logger.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}

	lookupSvc := lookup.NewService(cache, []lookup.Provider{
		lookup.NewGoogleBooksProvider(os.Getenv("GOOGLE_BOOKS_API_KEY")),
		lookup.NewOpenLibraryProvider(),
		lookup.NewNBProvider(),
	}, logger)

	srv := server.New(":"+port, render, books, lookupSvc)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("starting server", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()

	logger.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

// newISBNCache builds the ISBN lookup cache from REDIS_URL. If it's unset
// (e.g. local development without a Redis instance), it logs a warning and
// falls back to lookup.NoopCache so the app still serves — lookups just
// always go straight to the external providers. If REDIS_URL is set but
// unreachable, that's a misconfiguration and startup fails fast, mirroring
// db.NewPool's ping-on-connect.
func newISBNCache(ctx context.Context, logger *slog.Logger) (lookup.Cache, error) {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		logger.Warn("REDIS_URL not set, ISBN lookups will not be cached")
		return lookup.NoopCache{}, nil
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return lookup.NewRedisCache(client), nil
}
