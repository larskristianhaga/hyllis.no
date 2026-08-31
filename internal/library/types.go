// Package library contains the shared domain types and interfaces for a
// user's library entries (the association between a user and a book they
// own/have read). It has no dependency on any concrete storage or transport
// implementation.
package library

import "time"

// Entry is the shared domain representation of a book in a user's library.
type Entry struct {
	ID       string
	UserID   string
	BookID   string
	AddedAt  time.Time
	Notes    string
	Location string
}
