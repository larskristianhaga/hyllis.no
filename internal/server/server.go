// Package server sets up the HTTP transport layer for the application.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// New builds an *http.Server listening on addr with all routes registered.
func New(addr string, render *web.Renderer, books book.Repository, lookupSvc *lookup.Service, log *slog.Logger) *http.Server {
	h := newHandlers(render, books, lookupSvc, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(render.StaticFS())))

	mux.HandleFunc("GET /", h.home)
	mux.HandleFunc("GET /scan", h.scanPage)
	mux.HandleFunc("GET /library", h.libraryPage)
	// GET /books is CLAUDE.md's documented "søk/filtrer i eget bibliotek"
	// route — an alias of /library's handler rather than a separate JSON
	// API, matching this app's server-rendered-HTML architecture.
	mux.HandleFunc("GET /books", h.libraryPage)
	mux.HandleFunc("GET /books/search", h.librarySearch)
	mux.HandleFunc("GET /books/{id}", h.bookDetail)
	mux.HandleFunc("POST /books/scan", h.scanSubmit)
	mux.HandleFunc("POST /books/manual", h.manualSubmit)
	mux.HandleFunc("POST /books/confirm", h.confirmSubmit)
	mux.HandleFunc("DELETE /books/{id}", h.bookDelete)
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("GET /register", h.registerPage)
	mux.HandleFunc("POST /register", h.registerSubmit)

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
