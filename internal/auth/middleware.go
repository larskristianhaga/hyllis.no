package auth

import (
	"context"
	"net/http"
)

type contextKey int

const (
	claimsContextKey contextKey = iota
	tokenContextKey
)

// UserFromContext returns the authenticated user's claims stashed by
// Middleware, if any.
func UserFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(Claims)
	return claims, ok
}

// AccessTokenFromContext returns the (possibly just refreshed) access token
// Middleware/Optional verified for this request, for forwarding to the API
// as a Bearer token.
func AccessTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(tokenContextKey).(string)
	return token
}

// ContextWithSession returns ctx carrying a verified session's claims and
// access token, as Middleware/Optional attach them. Exported for tests.
func ContextWithSession(ctx context.Context, claims Claims, token string) context.Context {
	ctx = context.WithValue(ctx, claimsContextKey, claims)
	return context.WithValue(ctx, tokenContextKey, token)
}

func withSession(r *http.Request, claims Claims, token string) *http.Request {
	return r.WithContext(ContextWithSession(r.Context(), claims, token))
}

// Optional is Middleware for public pages: it attaches the session to the
// request context when there is a valid one (refreshing it if needed), and
// otherwise just serves the request anonymously instead of redirecting.
func Optional(verifier *Verifier, client *Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokens := ReadTokens(r)
			if tokens.AccessToken == "" && tokens.RefreshToken == "" {
				next.ServeHTTP(w, r)
				return
			}
			if claims, token, err := verifyOrRefresh(r.Context(), verifier, client, w, tokens); err == nil {
				r = withSession(r, claims, token)
			}
			next.ServeHTTP(w, r)
		})
	}
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

			claims, token, err := verifyOrRefresh(r.Context(), verifier, client, w, tokens)
			if err != nil {
				redirectToLogin(w, r)
				return
			}

			next.ServeHTTP(w, withSession(r, claims, token))
		})
	}
}

func verifyOrRefresh(ctx context.Context, verifier *Verifier, client *Client, w http.ResponseWriter, tokens Tokens) (Claims, string, error) {
	if tokens.AccessToken != "" {
		if claims, err := verifier.Verify(tokens.AccessToken); err == nil {
			return claims, tokens.AccessToken, nil
		}
	}

	if tokens.RefreshToken == "" {
		return Claims{}, "", http.ErrNoCookie
	}

	session, err := client.Refresh(ctx, tokens.RefreshToken)
	if err != nil {
		return Claims{}, "", err
	}

	claims, err := verifier.Verify(session.AccessToken)
	if err != nil {
		return Claims{}, "", err
	}

	SetSession(w, session.Tokens)
	return claims, session.AccessToken, nil
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
