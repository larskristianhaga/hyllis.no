package webapp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/larskristianhaga/hyllis.no/internal/api"
	"github.com/larskristianhaga/hyllis.no/internal/apiserver"
	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/user"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

type stubVerifier struct{}

func (stubVerifier) Verify(token string) (auth.Claims, error) {
	if token != "tok" {
		return auth.Claims{}, errors.New("bad token")
	}
	return auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u1"}, Email: "u1@example.com"}, nil
}

type stubResolver struct{}

func (stubResolver) Resolve(_ context.Context, isbn string) (*book.Book, error) {
	if isbn == "9788202000001" {
		return &book.Book{ISBN: isbn, Title: "Sult", Author: "Knut Hamsun", Source: "nb"}, nil
	}
	return nil, lookup.ErrNotFound
}

// newStack runs a real API server (in-memory repos) and returns web
// handlers talking to it over HTTP — the same wiring as production, minus
// Supabase.
func newStack(t *testing.T) *handlers {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(apiserver.Handler(apiserver.Deps{
		Books:    book.NewMemoryRepository(nil),
		Library:  library.NewMemoryRepository(),
		Users:    user.NewMemoryRepository(),
		Lookup:   stubResolver{},
		Verifier: stubVerifier{},
		Log:      discard,
	}))
	t.Cleanup(srv.Close)

	render, err := web.New()
	if err != nil {
		t.Fatal(err)
	}
	return newHandlers(render, api.NewClient(srv.URL), nil, discard)
}

// call runs handler h for an authenticated request.
func call(h http.HandlerFunc, method, target string, form url.Values, pathID string) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	claims, _ := stubVerifier{}.Verify("tok")
	r = r.WithContext(auth.ContextWithSession(r.Context(), claims, "tok"))
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func TestWebAppOverAPI(t *testing.T) {
	h := newStack(t)

	// Unknown ISBN → manual-entry form.
	rec := call(h.scanSubmit, "POST", "/books/scan", url.Values{"isbn": {"9780000000000"}}, "")
	if !strings.Contains(rec.Body.String(), "books/manual") {
		t.Fatalf("unknown isbn should render manual form, got: %s", rec.Body.String())
	}

	// Known ISBN → preview, not saved yet.
	rec = call(h.scanSubmit, "POST", "/books/scan", url.Values{"isbn": {"9788202000001"}}, "")
	if !strings.Contains(rec.Body.String(), "Sult") || !strings.Contains(rec.Body.String(), "books/confirm") {
		t.Fatalf("known isbn should render preview, got: %s", rec.Body.String())
	}
	if rec := call(h.librarySearch, "GET", "/books/search", nil, ""); strings.Contains(rec.Body.String(), "Sult") {
		t.Fatal("preview must not save the book")
	}

	// Confirm → saved.
	rec = call(h.confirmSubmit, "POST", "/books/confirm", url.Values{
		"isbn": {"9788202000001"}, "title": {"Sult"}, "author": {"Knut Hamsun"}, "source": {"nb"},
	}, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sult") {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}

	// Scanning it again says it's already there.
	rec = call(h.scanSubmit, "POST", "/books/scan", url.Values{"isbn": {"9788202000001"}}, "")
	if strings.Contains(rec.Body.String(), "books/confirm") {
		t.Fatalf("owned book should not render a preview again: %s", rec.Body.String())
	}

	// Manual entry.
	rec = call(h.manualSubmit, "POST", "/books/manual", url.Values{
		"isbn": {"9780000000000"}, "title": {"Egen bok"}, "author": {"Meg"},
	}, "")
	if !strings.Contains(rec.Body.String(), "Egen bok") {
		t.Fatalf("manual: %s", rec.Body.String())
	}

	// Library page lists both; search narrows.
	rec = call(h.libraryPage, "GET", "/library", nil, "")
	if body := rec.Body.String(); !strings.Contains(body, "Sult") || !strings.Contains(body, "Egen bok") {
		t.Fatalf("library page missing books: %s", body)
	}
	rec = call(h.librarySearch, "GET", "/books/search?q=hamsun", nil, "")
	if body := rec.Body.String(); !strings.Contains(body, "Sult") || strings.Contains(body, "Egen bok") {
		t.Fatalf("search should match only Sult: %s", body)
	}

	// Find the entry id via the API-backed owned lookup.
	it, err := h.api.GetByISBN(context.Background(), "tok", "9788202000001")
	if err != nil {
		t.Fatal(err)
	}

	rec = call(h.bookDetail, "GET", "/books/"+it.ID, nil, it.ID)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sult") {
		t.Fatalf("detail: %d", rec.Code)
	}
	if rec := call(h.bookDetail, "GET", "/books/nope", nil, "nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing detail: %d", rec.Code)
	}

	rec = call(h.bookUpdate, "PATCH", "/books/"+it.ID, url.Values{"status": {"read"}, "notes": {"Fin"}}, it.ID)
	if !strings.Contains(rec.Body.String(), "Fin") {
		t.Fatalf("update: %s", rec.Body.String())
	}
	rec = call(h.bookUpdate, "PATCH", "/books/"+it.ID, url.Values{"status": {"bogus"}}, it.ID)
	if !strings.Contains(rec.Body.String(), "Ugyldig lesestatus") {
		t.Fatalf("invalid update should show error: %s", rec.Body.String())
	}

	// Export contains both books.
	rec = call(h.libraryExport, "GET", "/library/export.csv", nil, "")
	csvData := rec.Body.String()
	if !strings.Contains(csvData, "9788202000001") || !strings.Contains(csvData, "Fin") {
		t.Fatalf("export: %s", csvData)
	}

	// Delete.
	r := httptest.NewRequest("DELETE", "/books/"+it.ID, nil)
	r.Header.Set("HX-Request", "true")
	r.SetPathValue("id", it.ID)
	claims, _ := stubVerifier{}.Verify("tok")
	r = r.WithContext(auth.ContextWithSession(r.Context(), claims, "tok"))
	del := httptest.NewRecorder()
	h.bookDelete(del, r)
	if del.Code != http.StatusNoContent || del.Header().Get("HX-Redirect") != "/library" {
		t.Fatalf("delete: %d %v", del.Code, del.Header())
	}

	// Re-import the export: Sult comes back, Egen bok already exists.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.csv")
	io.WriteString(fw, strings.TrimPrefix(csvData, "\ufeff"))
	mw.Close()
	r = httptest.NewRequest("POST", "/library/import", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r = r.WithContext(auth.ContextWithSession(r.Context(), claims, "tok"))
	imp := httptest.NewRecorder()
	h.libraryImport(imp, r)
	res, err := h.api.List(context.Background(), "tok", api.ListOptions{PerPage: -1})
	if err != nil || res.Total != 2 {
		t.Fatalf("after import total = %+v, %v; body: %s", res, err, imp.Body.String())
	}
	back, err := h.api.GetByISBN(context.Background(), "tok", "9788202000001")
	if err != nil || back.Status != library.StatusRead || back.Notes != "Fin" {
		t.Fatalf("re-imported entry = %+v, %v", back, err)
	}
}
