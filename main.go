// Command hyllis is the entrypoint for both of hyllis.no's services, which
// ship as one binary and are picked by the first argument:
//
//	hyllis api   JSON API (internal/apiserver): owns Postgres, Redis and the
//	             external ISBN lookups. Listens on PORT (default 8081).
//	hyllis web   HTMX web app (internal/webapp): pages, static assets and the
//	             Supabase login flow; calls the API at API_URL for all data.
//	             Listens on PORT (default 8080).
//	hyllis       Both in one process (local development / plain docker run):
//	             the API on 127.0.0.1:API_PORT (default 8081) and the web app
//	             on PORT (default 8080), wired to each other over loopback.
//
// In production they run as separate Fly apps (fly.toml for the web app,
// fly.api.toml for the API), so the web app never holds database, Redis or
// Google Books credentials.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"

	"github.com/larskristianhaga/hyllis.no/internal/api"
	"github.com/larskristianhaga/hyllis.no/internal/apiserver"
	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/db"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/user"
	"github.com/larskristianhaga/hyllis.no/internal/web"
	"github.com/larskristianhaga/hyllis.no/internal/webapp"
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

	mode := "all"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var (
		servers []*http.Server
		cleanup func()
		err     error
	)
	switch mode {
	case "api":
		servers, cleanup, err = apiServers(ctx, logger, ":"+envOr("PORT", "8081"))
	case "web":
		servers, err = webServers(ctx, logger, ":"+envOr("PORT", "8080"), os.Getenv("API_URL"))
	case "all":
		apiAddr := "127.0.0.1:" + envOr("API_PORT", "8081")
		servers, cleanup, err = apiServers(ctx, logger, apiAddr)
		if err == nil {
			var web []*http.Server
			web, err = webServers(ctx, logger, ":"+envOr("PORT", "8080"), envOr("API_URL", "http://"+apiAddr))
			servers = append(servers, web...)
		}
	default:
		err = fmt.Errorf("unknown mode %q (want api, web or nothing)", mode)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		logger.Error("startup failed", "mode", mode, "error", err)
		os.Exit(1)
	}

	for _, srv := range servers {
		go func() {
			logger.Info("starting server", "mode", mode, "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("server error", "addr", srv.Addr, "error", err)
				stop()
			}
		}()
	}

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	failed := false
	// Shut down in reverse start order: the web app before the API it calls.
	for i := len(servers) - 1; i >= 0; i-- {
		if err := servers[i].Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "addr", servers[i].Addr, "error", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	logger.Info("stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// apiServers builds the JSON API server and everything it owns: the
// database pool (with migrations), the repositories, the ISBN cache and
// lookup providers, and a Supabase JWKS verifier for its Bearer tokens.
func apiServers(ctx context.Context, logger *slog.Logger, addr string) ([]*http.Server, func(), error) {
	supabaseURL := os.Getenv("SUPABASE_URL")
	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("build Supabase JWKS verifier: %w", err)
	}

	pool, closeDB, err := newDB(ctx, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to database: %w", err)
	}

	books, users, libraryRepo := newRepositories(pool)

	cache, err := newISBNCache(ctx, logger)
	if err != nil {
		closeDB()
		return nil, nil, fmt.Errorf("connect to redis: %w", err)
	}

	lookupSvc := lookup.NewService(cache, []lookup.Provider{
		lookup.NewGoogleBooksProvider(os.Getenv("GOOGLE_BOOKS_API_KEY")),
		lookup.NewOpenLibraryProvider(),
		lookup.NewNBProvider(),
		lookup.NewBibliotekenesProvider(),
	}, logger)

	srv := apiserver.New(addr, apiserver.Deps{
		Books:    books,
		Library:  libraryRepo,
		Users:    users,
		Lookup:   lookupSvc,
		Verifier: verifier,
		Log:      logger.With("service", "api"),
	})
	return []*http.Server{srv}, closeDB, nil
}

// webServers builds the HTMX web app server. It needs only Supabase Auth
// (to log users in and verify their session cookies) and the API's URL.
func webServers(ctx context.Context, logger *slog.Logger, addr, apiURL string) ([]*http.Server, error) {
	if apiURL == "" {
		return nil, errors.New("API_URL must be set to the hyllis API's base URL")
	}

	render, err := web.New()
	if err != nil {
		return nil, fmt.Errorf("build template renderer: %w", err)
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	authClient := auth.NewClient(supabaseURL, os.Getenv("SUPABASE_ANON_KEY"))
	verifier, err := auth.NewVerifier(ctx, supabaseURL)
	if err != nil {
		return nil, fmt.Errorf("build Supabase JWKS verifier: %w", err)
	}

	srv := webapp.New(addr, render, api.NewClient(apiURL), verifier, authClient, logger.With("service", "web"))
	return []*http.Server{srv}, nil
}

// newDB builds the shared Bun connection pool from DATABASE_URL. If it's
// unset (e.g. local development without Postgres configured), it logs a
// warning and returns a nil pool — mirroring newISBNCache's REDIS_URL
// fallback below — so newRepositories falls back to in-memory repositories
// and the app still serves, just without persistence. If DATABASE_URL is
// set but unreachable, that's a misconfiguration and startup fails fast
// (db.NewPool pings on connect). The returned close func releases the
// underlying connection pool and is a no-op for the in-memory fallback.
func newDB(ctx context.Context, logger *slog.Logger) (*bun.DB, func(), error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Warn("DATABASE_URL not set, using in-memory repositories (data will not persist)")
		return nil, func() {}, nil
	}

	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}

	if err := db.RunMigrations(ctx, pool, logger); err != nil {
		return nil, nil, err
	}

	closePool := func() {
		if err := db.Close(pool); err != nil {
			logger.Error("failed to close database pool", "error", err)
		}
	}
	return pool, closePool, nil
}

// newRepositories builds the book/user/library repositories from pool, or
// their in-memory equivalents when pool is nil (see newDB).
func newRepositories(pool *bun.DB) (book.Repository, user.Repository, library.Repository) {
	if pool == nil {
		return book.NewMemoryRepository(book.SeedBooks()), user.NewMemoryRepository(), library.NewMemoryRepository()
	}
	return db.NewBookRepository(pool), db.NewUserRepository(pool), db.NewLibraryRepository(pool)
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
