package lookup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBibliotekenesProvider_Lookup_Hit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("query"); got != "9788202893415" {
			t.Errorf("query query = %q, want 9788202893415", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"products": [{
				"isbn13": "9788202893415",
				"title": "Med hjertet i hånden",
				"authors": [{"name": "Yvonne Andersen"}],
				"imprints": ["Cappelen Damm"],
				"publication_year": "2026",
				"number_of_pages": 253,
				"languages": [{"code": "nob", "name": "Norsk bokmål"}],
				"image": {"url": "https://example.com/cover.jpg"}
			}]
		}`))
	}))
	withBaseURL(t, &bibliotekenesBaseURL, srv)

	p := NewBibliotekenesProvider()
	b, err := p.Lookup(context.Background(), "9788202893415")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if b.Title != "Med hjertet i hånden" || b.Author != "Yvonne Andersen" || b.Publisher != "Cappelen Damm" ||
		b.Year != 2026 || b.Pages != 253 || b.Language != "Norsk bokmål" || b.CoverURL != "https://example.com/cover.jpg" {
		t.Fatalf("Lookup returned %+v, unexpected fields", b)
	}
}

func TestBibliotekenesProvider_Lookup_Miss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"products": []}`))
	}))
	withBaseURL(t, &bibliotekenesBaseURL, srv)

	p := NewBibliotekenesProvider()
	_, err := p.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup = %v, want ErrNotFound", err)
	}
}

// TestBibliotekenesProvider_Lookup_RejectsNonMatchingItem covers the case
// where "query=<isbn>" (a search, not an exact filter) returns an unrelated
// product with a different isbn13 — that product must not be trusted, so
// the lookup should behave like a miss.
func TestBibliotekenesProvider_Lookup_RejectsNonMatchingItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"products": [{
				"isbn13": "9780000000000",
				"title": "Some Unrelated Book",
				"authors": [{"name": "Someone Else"}]
			}]
		}`))
	}))
	withBaseURL(t, &bibliotekenesBaseURL, srv)

	p := NewBibliotekenesProvider()
	_, err := p.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup = %v, want ErrNotFound", err)
	}
}
