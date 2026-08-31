// Package book contains the shared domain types and interfaces for books.
// It has no dependency on any concrete storage or transport implementation.
package book

import "time"

// Book is the shared domain representation of a book.
type Book struct {
	ID        string
	Title     string
	Author    string
	ISBN      string
	CreatedAt time.Time
	UpdatedAt time.Time
}
