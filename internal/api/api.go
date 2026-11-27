// Package api is the wire contract between hyllis' two services: the JSON
// API (internal/apiserver, which owns Postgres, Redis and the external ISBN
// lookups) and the HTMX web app (internal/webapp, which owns pages, static
// assets and the Supabase login flow). It holds the request/response types
// and a Client for calling the API. It deliberately depends only on the
// plain domain packages (book, library) — never on storage or lookup code —
// so the web app can be built and run without database or Redis access.
//
// Every /v1 endpoint is called on behalf of an end user and authenticated
// with that user's Supabase access token as "Authorization: Bearer <jwt>";
// the API verifies it itself against Supabase's JWKS, so it never has to
// trust the web app (or whichever other client) about who the user is.
package api

import (
	"fmt"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// Item is one book in a user's library: the user's library entry (id,
// added-at, reading status, notes, location) plus the shared book metadata
// it points at. ID is the library entry id — the id every /v1/library/{id}
// endpoint takes — never the shared books.id.
type Item struct {
	ID       string    `json:"id"`
	AddedAt  time.Time `json:"added_at"`
	Status   string    `json:"status"`
	Notes    string    `json:"notes,omitempty"`
	Location string    `json:"location,omitempty"`
	Book     book.Book `json:"book"`
}

// Profile is the authenticated user's local profile mirror.
type Profile struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// ProfileUpdate is the body of PUT /v1/me. The user id and email always
// come from the verified access token, never from the body.
type ProfileUpdate struct {
	DisplayName string `json:"display_name"`
}

// Library sort orders (ListOptions.Sort). SortNewest is the default.
const (
	SortNewest = "new"
	SortOldest = "old"
	SortTitle  = "title"
	SortAuthor = "author"
)

// Field filters (ListOptions.Field). FieldAll is the default.
const (
	FieldAll       = "all"
	FieldTitle     = "title"
	FieldAuthor    = "author"
	FieldPublisher = "publisher"
)

// DefaultPerPage is the page size GET /v1/library uses when per_page is
// absent.
const DefaultPerPage = 24

// ListOptions are the query parameters of GET /v1/library.
//
// Q fuzzily matches title, author and publisher together (internal/fuzzy).
// Field is a separate, display-only filter that hides books with nothing
// set in that field — it never changes what Q matches against. PerPage 0
// means DefaultPerPage; a negative PerPage returns every match in one page
// (used by CSV export).
type ListOptions struct {
	Q       string
	Field   string
	Sort    string
	Page    int
	PerPage int
}

// LibraryPage is the response of GET /v1/library.
type LibraryPage struct {
	Items []Item `json:"items"`
	// Total is the user's whole, unfiltered library size.
	Total int `json:"total"`
	// Matches is how many books matched Q/Field across all pages.
	Matches int `json:"matches"`
	// NextPage is the following page number, or 0 on the last page.
	NextPage int `json:"next_page,omitempty"`
}

// AddRequest is the body of POST /v1/library: a book (from a lookup, the
// manual-entry form, or an import) to add to the caller's library. Status,
// Notes and Location apply only if the entry is newly created.
type AddRequest struct {
	Book     book.Book `json:"book"`
	Status   string    `json:"status,omitempty"`
	Notes    string    `json:"notes,omitempty"`
	Location string    `json:"location,omitempty"`
}

// AddResult is the response of POST /v1/library. Existed is true when the
// user already had the book, in which case Item is their existing entry
// (the API answers 200 instead of 201).
type AddResult struct {
	Item    Item `json:"item"`
	Existed bool `json:"existed"`
}

// ImportRequest is the body of POST /v1/library/import.
type ImportRequest struct {
	Items []AddRequest `json:"items"`
}

// ImportResult is the response of POST /v1/library/import.
type ImportResult struct {
	Added    int `json:"added"`
	Existing int `json:"existing"`
	Skipped  int `json:"skipped"`
}

// EntryUpdate is the body of PATCH /v1/library/{id}.
type EntryUpdate struct {
	Status   string `json:"status"`
	Notes    string `json:"notes"`
	Location string `json:"location"`
}

// Limits on the free-text entry fields, in runes.
const (
	MaxNotesLen    = 2000
	MaxLocationLen = 200
)

// MaxImportItems caps how many items one POST /v1/library/import takes.
const MaxImportItems = 5000

// Error codes returned in Error.Code.
const (
	CodeBadRequest      = "bad_request"
	CodeUnauthorized    = "unauthorized"
	CodeNotFound        = "not_found"
	CodeInvalidISBN     = "invalid_isbn"
	CodeInvalidBook     = "invalid_book"
	CodeInvalidStatus   = "invalid_status"
	CodeNotesTooLong    = "notes_too_long"
	CodeLocationTooLong = "location_too_long"
	CodeTooManyItems    = "too_many_items"
	CodeInternal        = "internal"
)

// Error is the body of every non-2xx API response, and what Client methods
// return for one.
type Error struct {
	// Status is the HTTP status code; it isn't part of the JSON body.
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("api: %d %s: %s", e.Status, e.Code, e.Message)
}

// IsCode reports whether err is an *Error with the given code.
func IsCode(err error, code string) bool {
	e, ok := err.(*Error)
	return ok && e.Code == code
}

// ValidEAN13 reports whether s looks like an EAN-13 barcode: exactly 13
// ASCII digits. It doesn't verify the checksum digit — mock data and manual
// entry don't need that level of strictness.
func ValidEAN13(s string) bool {
	if len(s) != 13 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
