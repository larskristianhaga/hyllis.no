// Command server is the HTTP entrypoint for hyllis.no.
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

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/server"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

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

	srv := server.New(":"+port, render, books)

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
