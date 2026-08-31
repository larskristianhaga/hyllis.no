// Package server sets up the HTTP transport layer for the application.
package server

import (
	"net/http"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// New builds an *http.Server listening on addr with all routes registered.
func New(addr string, render *web.Renderer, books book.Repository) *http.Server {
	h := newHandlers(render, books)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(render.StaticFS())))

	mux.HandleFunc("GET /", h.home)
	mux.HandleFunc("GET /scan", h.scanPage)
	mux.HandleFunc("GET /library", h.libraryPage)
	mux.HandleFunc("GET /books/search", h.librarySearch)
	mux.HandleFunc("GET /books/{id}", h.bookDetail)
	mux.HandleFunc("POST /books/scan", h.scanSubmit)
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
