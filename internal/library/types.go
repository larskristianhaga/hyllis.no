// Package library contains the shared domain types and interfaces for a
// user's library entries (the association between a user and a book they
// own/have read). It has no dependency on any concrete storage or transport
// implementation.
package library

import "time"

// Reading statuses an Entry can be in. These mirror the check constraint on
// library_entries.status (migration 000008).
const (
	StatusUnread  = "unread"
	StatusReading = "reading"
	StatusRead    = "read"
)

// ValidStatus reports whether s is one of the known reading statuses.
func ValidStatus(s string) bool {
	switch s {
	case StatusUnread, StatusReading, StatusRead:
		return true
	}
	return false
}

// Entry is the shared domain representation of a book in a user's library.
// An empty Status is treated as StatusUnread by the repositories.
type Entry struct {
	ID       string
	UserID   string
	BookID   string
	AddedAt  time.Time
	Status   string
	Notes    string
	Location string
}
