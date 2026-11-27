// Package apiserver is hyllis' JSON API service. It is the only part of the
// system that talks to Postgres, Redis and the external ISBN providers; the
// web app (internal/webapp) reaches all of that through this API, using the
// wire types and client in internal/api.
//
// Every /v1 route requires "Authorization: Bearer <Supabase access token>"
// and is scoped to the user in that token. The token is verified here,
// independently of whichever client sent it.
package apiserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/api"
	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/user"
)

// TokenVerifier validates a Supabase access token. *auth.Verifier
// implements it.
type TokenVerifier interface {
	Verify(token string) (auth.Claims, error)
}

// Resolver resolves an ISBN to book metadata. *lookup.Service implements it.
type Resolver interface {
	Resolve(ctx context.Context, isbn string) (*book.Book, error)
}

// Deps are the API's backing services.
type Deps struct {
	Books    book.Repository
	Library  library.Repository
	Users    user.Repository
	Lookup   Resolver
	Verifier TokenVerifier
	Log      *slog.Logger
}

// maxBodyBytes bounds JSON request bodies. Import is the largest: up to
// api.MaxImportItems books with notes.
const maxBodyBytes = 16 << 20

// Handler returns the API's routes.
func Handler(d Deps) http.Handler {
	s := &service{books: d.Books, library: d.Library, users: d.Users, lookup: d.Lookup, log: d.Log}
	authed := requireBearer(d.Verifier)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	route := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, authed(h)) }
	route("GET /v1/me", s.getMe)
	route("PUT /v1/me", s.putMe)
	route("GET /v1/lookup/{isbn}", s.lookupISBN)
	route("GET /v1/library", s.list)
	route("POST /v1/library", s.add)
	route("POST /v1/library/import", s.importItems)
	route("GET /v1/library/isbn/{isbn}", s.getByISBN)
	route("GET /v1/library/{id}", s.get)
	route("PATCH /v1/library/{id}", s.update)
	route("DELETE /v1/library/{id}", s.delete)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, api.CodeNotFound, "no such endpoint")
	})

	return jsonHeaders(mux)
}

// New builds an *http.Server for the API listening on addr.
func New(addr string, d Deps) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           Handler(d),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

type contextKey int

const claimsKey contextKey = 0

func claimsFrom(r *http.Request) auth.Claims {
	c, _ := r.Context().Value(claimsKey).(auth.Claims)
	return c
}

// requireBearer verifies the Bearer access token on every request it wraps
// and stashes its claims in the request context.
func requireBearer(v TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				writeError(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing bearer token")
				return
			}
			claims, err := v.Verify(token)
			if err != nil || claims.UserID() == "" {
				writeError(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid or expired token")
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func jsonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, api.Error{Code: code, Message: msg})
}

func decodeJSON(r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}
