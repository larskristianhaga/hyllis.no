package lookup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// withBaseURL points target at srv for the duration of the test, restoring
// the original value afterwards.
func withBaseURL(t *testing.T, target *string, srv *httptest.Server) {
	t.Helper()
	original := *target
	*target = srv.URL
	t.Cleanup(func() {
		*target = original
		srv.Close()
	})
}

func TestGoogleBooksProvider_Lookup_Hit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "isbn:9780143127550" {
			t.Errorf("query q = %q, want isbn:9780143127550", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items": [{
				"volumeInfo": {
					"title": "The Hobbit",
					"authors": ["J.R.R. Tolkien"],
					"publisher": "Penguin Classics",
					"publishedDate": "1937-09-21",
					"pageCount": 310,
					"language": "en",
					"industryIdentifiers": [{"type": "ISBN_13", "identifier": "9780143127550"}],
					"imageLinks": {"thumbnail": "https://example.com/cover.jpg"}
				}
			}]
		}`))
	}))
	withBaseURL(t, &googleBooksBaseURL, srv)

	p := NewGoogleBooksProvider("")
	b, err := p.Lookup(context.Background(), "9780143127550")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if b.Title != "The Hobbit" || b.Author != "J.R.R. Tolkien" || b.Publisher != "Penguin Classics" || b.Year != 1937 || b.Pages != 310 {
		t.Fatalf("Lookup returned %+v, unexpected fields", b)
	}
}

func TestGoogleBooksProvider_Lookup_Miss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items": []}`))
	}))
	withBaseURL(t, &googleBooksBaseURL, srv)

	p := NewGoogleBooksProvider("")
	_, err := p.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup = %v, want ErrNotFound", err)
	}
}

// TestGoogleBooksProvider_Lookup_RejectsNonMatchingItem covers the case
// where "q=isbn:<isbn>" (a best-effort text search, not an exact filter)
// returns an unrelated item with no matching industryIdentifiers — that
// item must not be trusted, so the lookup should behave like a miss.
func TestGoogleBooksProvider_Lookup_RejectsNonMatchingItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items": [{
				"volumeInfo": {
					"title": "Some Unrelated Book",
					"authors": ["Someone Else"],
					"industryIdentifiers": [{"type": "ISBN_13", "identifier": "9780000000000"}]
				}
			}]
		}`))
	}))
	withBaseURL(t, &googleBooksBaseURL, srv)

	p := NewGoogleBooksProvider("")
	_, err := p.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup = %v, want ErrNotFound", err)
	}
}

func TestGoogleBooksProvider_Lookup_AppendsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "test-key" {
			t.Errorf("query key = %q, want test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items": []}`))
	}))
	withBaseURL(t, &googleBooksBaseURL, srv)

	p := NewGoogleBooksProvider("test-key")
	_, _ = p.Lookup(context.Background(), "0000000000000")
}
