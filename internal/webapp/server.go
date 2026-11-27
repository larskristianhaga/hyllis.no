// Package webapp is hyllis' web app service: server-rendered HTML + HTMX
// pages, static assets and the Supabase login/register/logout flow. It owns
// no data — every book/library operation goes to the JSON API
// (internal/apiserver) through LibraryAPI, authenticated with the logged-in
// user's own Supabase access token. It needs no database, Redis or ISBN
// provider credentials.
package webapp

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// New builds an *http.Server listening on addr with all routes registered.
// verifier/authClient drive the Supabase Auth session: verifier validates
// access tokens (see auth.Middleware), authClient issues/refreshes/revokes
// them and backs the login/register/logout handlers.
func New(addr string, render *web.Renderer, libraryAPI LibraryAPI, verifier *auth.Verifier, authClient *auth.Client, log *slog.Logger) *http.Server {
	h := newHandlers(render, libraryAPI, authClient, log)
	requireAuth := auth.Middleware(verifier, authClient)
	optionalAuth := auth.Optional(verifier, authClient)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	mux.Handle("GET /static/", staticCache(http.StripPrefix("/static/", http.FileServerFS(render.StaticFS()))))

	// Public pages. They still pick up a valid session when there is one,
	// so the layout shows the right nav and / can show the visitor's own
	// recently added books.
	mux.Handle("GET /", optionalAuth(http.HandlerFunc(h.home)))
	mux.Handle("GET /login", optionalAuth(http.HandlerFunc(h.loginPage)))
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.Handle("GET /register", optionalAuth(http.HandlerFunc(h.registerPage)))
	mux.HandleFunc("POST /register", h.registerSubmit)
	mux.HandleFunc("POST /logout", h.logoutSubmit)

	// Everything that reads/writes a user's own library requires a valid
	// session.
	mux.Handle("GET /scan", requireAuth(http.HandlerFunc(h.scanPage)))
	mux.Handle("GET /library", requireAuth(http.HandlerFunc(h.libraryPage)))
	// GET /books is an alias of /library's handler, matching this app's
	// server-rendered-HTML architecture (the JSON API lives in
	// internal/apiserver).
	mux.Handle("GET /books", requireAuth(http.HandlerFunc(h.libraryPage)))
	mux.Handle("GET /books/search", requireAuth(http.HandlerFunc(h.librarySearch)))
	mux.Handle("GET /books/{id}", requireAuth(http.HandlerFunc(h.bookDetail)))
	mux.Handle("POST /books/scan", requireAuth(http.HandlerFunc(h.scanSubmit)))
	mux.Handle("POST /books/manual", requireAuth(http.HandlerFunc(h.manualSubmit)))
	mux.Handle("POST /books/confirm", requireAuth(http.HandlerFunc(h.confirmSubmit)))
	mux.Handle("PATCH /books/{id}", requireAuth(http.HandlerFunc(h.bookUpdate)))
	mux.Handle("DELETE /books/{id}", requireAuth(http.HandlerFunc(h.bookDelete)))
	mux.Handle("GET /library/export.csv", requireAuth(http.HandlerFunc(h.libraryExport)))
	mux.Handle("POST /library/import", requireAuth(http.HandlerFunc(h.libraryImport)))

	return &http.Server{
		Addr:              addr,
		Handler:           securityHeaders(gzipResponses(csrfProtection(mux))),
		ReadHeaderTimeout: 5 * time.Second,
	}
}
