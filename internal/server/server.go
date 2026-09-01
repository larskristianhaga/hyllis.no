// Package server sets up the HTTP transport layer for the application.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/user"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// New builds an *http.Server listening on addr with all routes registered.
// verifier/authClient drive the Supabase Auth session: verifier validates
// access tokens (see auth.Middleware), authClient issues/refreshes/revokes
// them and backs the login/register/logout handlers.
func New(addr string, render *web.Renderer, books book.Repository, libraryRepo library.Repository, users user.Repository, verifier *auth.Verifier, authClient *auth.Client, lookupSvc *lookup.Service, log *slog.Logger) *http.Server {
	h := newHandlers(render, books, libraryRepo, users, authClient, lookupSvc, log)
	requireAuth := auth.Middleware(verifier, authClient)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(render.StaticFS())))

	// / stays public: per-user library ownership means an unauthenticated
	// visitor just sees a generic landing view instead of a personal
	// "recently added" list (home checks auth.UserFromContext itself).
	mux.HandleFunc("GET /", h.home)
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("GET /register", h.registerPage)
	mux.HandleFunc("POST /register", h.registerSubmit)
	mux.HandleFunc("POST /logout", h.logoutSubmit)

	// Everything that reads/writes a user's own library requires a valid
	// session.
	mux.Handle("GET /scan", requireAuth(http.HandlerFunc(h.scanPage)))
	mux.Handle("GET /library", requireAuth(http.HandlerFunc(h.libraryPage)))
	// GET /books is CLAUDE.md's documented "søk/filtrer i eget bibliotek"
	// route — an alias of /library's handler rather than a separate JSON
	// API, matching this app's server-rendered-HTML architecture.
	mux.Handle("GET /books", requireAuth(http.HandlerFunc(h.libraryPage)))
	mux.Handle("GET /books/search", requireAuth(http.HandlerFunc(h.librarySearch)))
	mux.Handle("GET /books/{id}", requireAuth(http.HandlerFunc(h.bookDetail)))
	mux.Handle("POST /books/scan", requireAuth(http.HandlerFunc(h.scanSubmit)))
	mux.Handle("POST /books/manual", requireAuth(http.HandlerFunc(h.manualSubmit)))
	mux.Handle("POST /books/confirm", requireAuth(http.HandlerFunc(h.confirmSubmit)))
	mux.Handle("DELETE /books/{id}", requireAuth(http.HandlerFunc(h.bookDelete)))

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
