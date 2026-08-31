// Package book contains the shared domain types and interfaces for books.
// It has no dependency on any concrete storage or transport implementation.
package book

import "time"

// Book is the shared domain representation of a book.
//
// ID and CreatedAt are database-assigned and meaningless before a Book has
// been persisted, so they're excluded from JSON — the internal/lookup
// package marshals a Book as-is when caching a resolved-but-not-yet-saved
// result.
type Book struct {
	ID        string    `json:"-"`
	ISBN      string    `json:"isbn"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	Publisher string    `json:"publisher,omitempty"`
	Year      int       `json:"year,omitempty"`
	CoverURL  string    `json:"cover_url,omitempty"`
	Language  string    `json:"language,omitempty"`
	Pages     int       `json:"pages,omitempty"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"-"`
}
