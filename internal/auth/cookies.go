package auth

import (
	"net/http"
	"time"
)

const (
	accessCookieName  = "sb-access-token"
	refreshCookieName = "sb-refresh-token"
)

// refreshCookieMaxAge bounds how long a signed-out browser can sit before
// its refresh token cookie expires client-side. Supabase's own refresh
// tokens are typically valid far longer than this; this is just how long we
// ask the browser to hold onto the cookie.
const refreshCookieMaxAge = 30 * 24 * time.Hour

// SetSession writes tokens as HttpOnly, Secure, SameSite=Lax cookies. The
// access token cookie carries no explicit MaxAge — it's a session cookie,
// re-validated (and silently refreshed on expiry, see Middleware) on every
// protected request.
func SetSession(w http.ResponseWriter, tokens Tokens) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookieName,
		Value:    tokens.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    tokens.RefreshToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(refreshCookieMaxAge.Seconds()),
	})
}

// ClearSession expires both session cookies, logging the browser out
// locally regardless of whether a server-side SignOut call succeeds.
func ClearSession(w http.ResponseWriter) {
	for _, name := range []string{accessCookieName, refreshCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}

// ReadTokens reads whatever session cookies are present on r. Either or
// both may be empty if not set.
func ReadTokens(r *http.Request) Tokens {
	var t Tokens
	if c, err := r.Cookie(accessCookieName); err == nil {
		t.AccessToken = c.Value
	}
	if c, err := r.Cookie(refreshCookieName); err == nil {
		t.RefreshToken = c.Value
	}
	return t
}
