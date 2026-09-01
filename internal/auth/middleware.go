package auth

import (
	"context"
	"net/http"
)

type contextKey int

const claimsContextKey contextKey = 0

// UserFromContext returns the authenticated user's claims stashed by
// Middleware, if any.
func UserFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(Claims)
	return claims, ok
}

// Middleware requires a valid Supabase session on every request it wraps.
// It verifies the access-token cookie; on expiry it transparently exchanges
// the refresh-token cookie for a new session (re-issuing both cookies)
// before retrying verification once. On any other failure it redirects to
// /login — as an HX-Redirect header for HTMX requests (a bare 3xx response
// to an htmx.ajax/hx-get call doesn't navigate the browser) or a normal
// redirect otherwise.
func Middleware(verifier *Verifier, client *Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokens := ReadTokens(r)

			claims, err := verifyOrRefresh(r.Context(), verifier, client, w, tokens)
			if err != nil {
				redirectToLogin(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func verifyOrRefresh(ctx context.Context, verifier *Verifier, client *Client, w http.ResponseWriter, tokens Tokens) (Claims, error) {
	if tokens.AccessToken != "" {
		if claims, err := verifier.Verify(tokens.AccessToken); err == nil {
			return claims, nil
		}
	}

	if tokens.RefreshToken == "" {
		return Claims{}, http.ErrNoCookie
	}

	session, err := client.Refresh(ctx, tokens.RefreshToken)
	if err != nil {
		return Claims{}, err
	}

	claims, err := verifier.Verify(session.AccessToken)
	if err != nil {
		return Claims{}, err
	}

	SetSession(w, session.Tokens)
	return claims, nil
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	ClearSession(w)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
