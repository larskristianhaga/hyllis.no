// Package user contains the shared domain types and interfaces for users.
// It has no dependency on any concrete storage or transport implementation.
package user

import "time"

// User is the shared domain representation of a registered user.
type User struct {
	ID           string
	Email        string
	DisplayName  string
	PasswordHash string
	CreatedAt    time.Time
}
