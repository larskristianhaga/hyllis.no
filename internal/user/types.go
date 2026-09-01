// Package user contains the shared domain types and interfaces for users.
// It has no dependency on any concrete storage or transport implementation.
package user

import "time"

// User is the shared domain representation of a registered user. Credentials
// are owned entirely by Supabase Auth — this is a local mirror of the
// profile fields Go needs (e.g. to satisfy library_entries.user_id's
// foreign key and to render a display name), keyed by Supabase's own user
// id (the JWT "sub" claim).
type User struct {
	ID          string
	Email       string
	DisplayName string
	CreatedAt   time.Time
}
