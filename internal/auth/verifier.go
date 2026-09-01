package auth

import (
	"context"
	"fmt"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Claims is the subset of a Supabase access token's JWT claims this app
// needs. Sub is the Supabase user id — the same value stored as
// users.id/library_entries.user_id.
type Claims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
}

// UserID returns the Supabase user id (the JWT "sub" claim).
func (c Claims) UserID() string {
	return c.Subject
}

// Verifier validates Supabase-issued access tokens against Supabase's own
// published JWKS. Supabase signs tokens with ES256 (asymmetric ECC P-256),
// so verification needs no shared secret — just the public key set, fetched
// and auto-refreshed from {SUPABASE_URL}/auth/v1/.well-known/jwks.json.
type Verifier struct {
	kf keyfunc.Keyfunc
}

// NewVerifier builds a Verifier for supabaseURL, launching a background
// refresh of the JWKS as Supabase rotates its signing keys. ctx controls
// that background refresh's lifetime, not this call.
func NewVerifier(ctx context.Context, supabaseURL string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{supabaseURL + "/auth/v1/.well-known/jwks.json"})
	if err != nil {
		return nil, fmt.Errorf("auth: build JWKS verifier: %w", err)
	}
	return &Verifier{kf: kf}, nil
}

// Verify parses and validates tokenString, returning its claims if the
// signature, expiry, and algorithm all check out.
func (v *Verifier) Verify(tokenString string) (Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(tokenString, &claims, v.kf.Keyfunc, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		return Claims{}, fmt.Errorf("auth: verify token: %w", err)
	}
	return claims, nil
}
