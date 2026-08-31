package lookup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNBProvider_Lookup_Hit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "isbn:9788203293176" {
			t.Errorf("query q = %q, want isbn:9788203293176", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"_embedded": {
				"items": [{
					"metadata": {
						"title": "Sult",
						"creators": ["Hamsun, Knut"],
						"originInfo": {"issued": "1890"}
					}
				}]
			}
		}`))
	}))
	withBaseURL(t, &nbBaseURL, srv)

	p := NewNBProvider()
	b, err := p.Lookup(context.Background(), "9788203293176")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if b.Title != "Sult" || b.Author != "Hamsun, Knut" || b.Year != 1890 {
		t.Fatalf("Lookup returned %+v, unexpected fields", b)
	}
}

func TestNBProvider_Lookup_Miss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_embedded": {}}`))
	}))
	withBaseURL(t, &nbBaseURL, srv)

	p := NewNBProvider()
	_, err := p.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup = %v, want ErrNotFound", err)
	}
}
