// Package book contains the shared domain types and interfaces for books.
// It has no dependency on any concrete storage or transport implementation.
package book

import "time"

// Book is the shared domain representation of a book.
type Book struct {
	ID        string
	ISBN      string
	Title     string
	Author    string
	Publisher string
	Year      int
	CoverURL  string
	Language  string
	Pages     int
	CreatedAt time.Time
}
