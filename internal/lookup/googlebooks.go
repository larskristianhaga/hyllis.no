package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// googleBooksBaseURL is a var (not a const) so tests can point it at an
// httptest.Server.
var googleBooksBaseURL = "https://www.googleapis.com/books/v1/volumes"

// GoogleBooksProvider resolves ISBNs against the Google Books API. It works
// unauthenticated at low volume — apiKey may be empty.
type GoogleBooksProvider struct {
	apiKey string
	client *http.Client
}

// NewGoogleBooksProvider builds a GoogleBooksProvider. apiKey may be empty.
func NewGoogleBooksProvider(apiKey string) *GoogleBooksProvider {
	return &GoogleBooksProvider{apiKey: apiKey, client: &http.Client{Timeout: 5 * time.Second}}
}

func (p *GoogleBooksProvider) Name() string { return "google_books" }

type googleVolumeInfo struct {
	Title               string   `json:"title"`
	Authors             []string `json:"authors"`
	Publisher           string   `json:"publisher"`
	PublishedDate       string   `json:"publishedDate"`
	PageCount           int      `json:"pageCount"`
	Language            string   `json:"language"`
	IndustryIdentifiers []struct {
		Identifier string `json:"identifier"`
	} `json:"industryIdentifiers"`
	ImageLinks struct {
		Thumbnail string `json:"thumbnail"`
	} `json:"imageLinks"`
}

// hasISBN reports whether isbn appears among this volume's industry
// identifiers (ISBN_10/ISBN_13/etc).
func (v googleVolumeInfo) hasISBN(isbn string) bool {
	for _, id := range v.IndustryIdentifiers {
		if id.Identifier == isbn {
			return true
		}
	}
	return false
}

type googleBooksResponse struct {
	Items []struct {
		VolumeInfo googleVolumeInfo `json:"volumeInfo"`
	} `json:"items"`
}

func (p *GoogleBooksProvider) Lookup(ctx context.Context, isbn string) (*book.Book, error) {
	q := url.Values{"q": {"isbn:" + isbn}}
	if p.apiKey != "" {
		q.Set("key", p.apiKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleBooksBaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("lookup: build google books request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup: google books request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lookup: google books returned status %d", resp.StatusCode)
	}

	var parsed googleBooksResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("lookup: decode google books response: %w", err)
	}

	// "q=isbn:<isbn>" is a best-effort search, not an exact filter — absent
	// an exact match, Google Books can still return unrelated items, so
	// only trust an item whose own industryIdentifiers actually contains
	// the ISBN we asked for.
	var info *googleVolumeInfo
	for i := range parsed.Items {
		if parsed.Items[i].VolumeInfo.hasISBN(isbn) {
			info = &parsed.Items[i].VolumeInfo
			break
		}
	}
	if info == nil || info.Title == "" {
		return nil, ErrNotFound
	}

	return &book.Book{
		ISBN:      isbn,
		Title:     info.Title,
		Author:    strings.Join(info.Authors, ", "),
		Publisher: info.Publisher,
		Year:      extractYear(info.PublishedDate),
		CoverURL:  info.ImageLinks.Thumbnail,
		Language:  info.Language,
		Pages:     info.PageCount,
	}, nil
}

var _ Provider = (*GoogleBooksProvider)(nil)
