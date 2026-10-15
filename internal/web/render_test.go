package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPageTitle(t *testing.T) {
	if got := pageTitle("home", nil); !strings.HasPrefix(got, "hyllis") {
		t.Errorf("home title = %q, want it to start with %q", got, "hyllis")
	}
	if got, want := pageTitle("library", nil), "Bibliotek – hyllis"; got != want {
		t.Errorf("library title = %q, want %q", got, want)
	}
	if got, want := pageTitle("book_detail", titled("Sult")), "Sult – hyllis"; got != want {
		t.Errorf("Titled title = %q, want %q", got, want)
	}
}

type titled string

func (t titled) PageTitle() string { return string(t) }

func TestIsActive(t *testing.T) {
	tests := []struct {
		path     string
		prefixes []string
		want     bool
	}{
		{"/", []string{"/"}, true},
		{"/scan", []string{"/"}, false},
		{"/library", []string{"/library", "/books"}, true},
		{"/books/abc", []string{"/library", "/books"}, true},
		{"/library-x", []string{"/library"}, false},
	}
	for _, tt := range tests {
		if got := isActive(tt.path, tt.prefixes...); got != tt.want {
			t.Errorf("isActive(%q, %v) = %v, want %v", tt.path, tt.prefixes, got, tt.want)
		}
	}
}

func TestHTTPSURL(t *testing.T) {
	if got, want := httpsURL("http://books.google.com/x.jpg"), "https://books.google.com/x.jpg"; got != want {
		t.Errorf("httpsURL = %q, want %q", got, want)
	}
	if got, want := httpsURL("https://a/b"), "https://a/b"; got != want {
		t.Errorf("httpsURL = %q, want %q", got, want)
	}
}

// TestTemplatesParseAndRender catches template syntax/field mistakes, which
// would otherwise only surface at request time.
func TestTemplatesParseAndRender(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	search := map[string]any{
		"Books": nil, "Query": "", "Field": "all", "Sort": "new",
		"Total": 0, "Matches": 0, "NextURL": "",
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/library", nil)
	if err := rd.Page(rec, req, "library", search); err != nil {
		t.Fatalf("render library page: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{"<title>Bibliotek – hyllis</title>", "Hopp til innhold", "/static/css/styles.css?v="} {
		if !strings.Contains(body, want) {
			t.Errorf("library page missing %q", want)
		}
	}

	rec = httptest.NewRecorder()
	if err := rd.Partial(rec, "entry-details", map[string]any{
		"ID": "e1", "Status": "read", "Notes": "n", "Location": "l", "Saved": true, "Error": "",
	}); err != nil {
		t.Fatalf("render entry-details: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `<option value="read" selected>`) {
		t.Errorf("entry-details did not preselect the status: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	if err := rd.Partial(rec, "import-result", map[string]any{"Added": 2, "Existing": 1, "Skipped": 0}); err != nil {
		t.Fatalf("render import-result: %v", err)
	}
}
