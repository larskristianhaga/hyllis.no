package lookup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenLibraryProvider_Lookup_Hit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("bibkeys"); got != "ISBN:9780451524935" {
			t.Errorf("query bibkeys = %q, want ISBN:9780451524935", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ISBN:9780451524935": {
				"title": "1984",
				"authors": [{"name": "George Orwell"}],
				"publishers": [{"name": "Signet Classics"}],
				"publish_date": "Jul 1, 1961",
				"number_of_pages": 328,
				"cover": {"medium": "https://example.com/cover.jpg"}
			}
		}`))
	}))
	withBaseURL(t, &openLibraryBaseURL, srv)

	p := NewOpenLibraryProvider()
	b, err := p.Lookup(context.Background(), "9780451524935")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if b.Title != "1984" || b.Author != "George Orwell" || b.Publisher != "Signet Classics" || b.Year != 1961 || b.Pages != 328 {
		t.Fatalf("Lookup returned %+v, unexpected fields", b)
	}
}

func TestOpenLibraryProvider_Lookup_Miss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	withBaseURL(t, &openLibraryBaseURL, srv)

	p := NewOpenLibraryProvider()
	_, err := p.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup = %v, want ErrNotFound", err)
	}
}
