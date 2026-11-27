package webapp

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/larskristianhaga/hyllis.no/internal/api"
)

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy"} {
		if rec.Header().Get(name) == "" {
			t.Errorf("missing header %s", name)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent over plain HTTP")
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS missing for X-Forwarded-Proto: https")
	}
}

func TestCSRFProtectionRejectsCrossSitePost(t *testing.T) {
	h := csrfProtection(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("POST", "/books/scan", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST status = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest("POST", "/books/scan", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("same-origin POST status = %d, want 204", rec.Code)
	}
}

func TestGzipResponses(t *testing.T) {
	const body = "<html>hei</html>"
	h := gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, body)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", rec.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, _ := io.ReadAll(zr)
	if string(got) != body {
		t.Errorf("decoded body = %q, want %q", got, body)
	}

	// Without Accept-Encoding the body is passed through untouched.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != body {
		t.Errorf("unexpected encoding for a client without gzip: %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestParseLibraryQuery(t *testing.T) {
	r := httptest.NewRequest("GET", "/books/search?q=+sult+&sort=bogus&page=3", nil)
	q := parseLibraryQuery(r)
	if q.Q != "sult" || q.Field != defaultSearchField || q.Sort != api.SortNewest || q.Page != 3 {
		t.Errorf("parseLibraryQuery = %+v", q)
	}
	if next := q.pageURL(4); !strings.Contains(next, "page=4") || !strings.Contains(next, "q=sult") {
		t.Errorf("pageURL = %q", next)
	}
}
