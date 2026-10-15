package server

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// contentSecurityPolicy locks the app down to its own origin. Exceptions:
//   - script-src allows unpkg.com because htmx and html5-qrcode are still
//     loaded from there (see layout.html / scan.html); drop it once they are
//     self-hosted under /static/js.
//   - style-src allows 'unsafe-inline' because html5-qrcode and htmx inject
//     inline styles.
//   - img-src allows any https host because book covers come from the
//     external metadata sources (Google Books, Open Library, NB, ...).
//   - media-src/img-src allow blob:/data: for the camera preview.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://unpkg.com; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' https: data: blob:; " +
	"media-src 'self' blob:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders sets a conservative set of response headers on every
// response: CSP, MIME-sniffing and referrer hardening, a Permissions-Policy
// that only lets this origin use the camera (needed by the scanner), and
// HSTS when the request arrived over HTTPS (directly or via Fly's proxy).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=()")
		h.Set("X-Frame-Options", "DENY")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// csrfProtection rejects cross-origin state-changing requests (POST, PATCH,
// DELETE, ...) using the browser-supplied Sec-Fetch-Site / Origin headers
// (net/http's CrossOriginProtection). This sits on top of the auth cookies
// being SameSite=Lax. Same-origin requests, safe methods, and non-browser
// clients that send neither header are let through.
func csrfProtection(next http.Handler) http.Handler {
	return http.NewCrossOriginProtection().Handler(next)
}

// staticCache marks /static assets cacheable. Requests carrying the
// content-hash ?v= query that Renderer's asset func appends are immutable
// (the URL changes whenever the file does); others are revalidated hourly.
func staticCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		next.ServeHTTP(w, r)
	})
}

// gzipResponses compresses text-like responses (HTML, CSS, JS, SVG, JSON)
// for clients that advertise gzip support. Range requests and already
// encoded responses are passed through untouched.
func gzipResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead ||
			r.Header.Get("Range") != "" ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// gzipWriter lazily decides, on the first WriteHeader/Write, whether the
// response is compressible based on the Content-Type the handler set.
type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

func (w *gzipWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true

	h := w.Header()
	if h.Get("Content-Encoding") != "" || !compressible(h.Get("Content-Type")) {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	w.gz = gzip.NewWriter(w.ResponseWriter)
}

func (w *gzipWriter) WriteHeader(code int) {
	// 1xx, 204 and 304 carry no body, so there is nothing to encode.
	if code >= 200 && code != http.StatusNoContent && code != http.StatusNotModified {
		w.decide()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	w.decide()
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipWriter) close() {
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func compressible(contentType string) bool {
	ct := strings.ToLower(contentType)
	switch {
	case strings.HasPrefix(ct, "text/"),
		strings.HasPrefix(ct, "application/javascript"),
		strings.HasPrefix(ct, "application/json"),
		strings.HasPrefix(ct, "image/svg+xml"):
		return true
	}
	return false
}
