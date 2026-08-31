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

// openLibraryBaseURL is a var (not a const) so tests can point it at an
// httptest.Server.
var openLibraryBaseURL = "https://openlibrary.org/api/books"

// OpenLibraryProvider resolves ISBNs against Open Library's books API.
type OpenLibraryProvider struct {
	client *http.Client
}

// NewOpenLibraryProvider builds an OpenLibraryProvider.
func NewOpenLibraryProvider() *OpenLibraryProvider {
	return &OpenLibraryProvider{client: &http.Client{Timeout: 5 * time.Second}}
}

func (p *OpenLibraryProvider) Name() string { return "open_library" }

type openLibraryEntry struct {
	Title   string `json:"title"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Publishers []struct {
		Name string `json:"name"`
	} `json:"publishers"`
	PublishDate   string `json:"publish_date"`
	NumberOfPages int    `json:"number_of_pages"`
	Cover         struct {
		Medium string `json:"medium"`
	} `json:"cover"`
}

func (p *OpenLibraryProvider) Lookup(ctx context.Context, isbn string) (*book.Book, error) {
	bibkey := "ISBN:" + isbn
	q := url.Values{
		"bibkeys": {bibkey},
		"format":  {"json"},
		"jscmd":   {"data"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openLibraryBaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("lookup: build open library request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup: open library request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lookup: open library returned status %d", resp.StatusCode)
	}

	var parsed map[string]openLibraryEntry
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("lookup: decode open library response: %w", err)
	}

	entry, ok := parsed[bibkey]
	if !ok || entry.Title == "" {
		return nil, ErrNotFound
	}

	authors := make([]string, 0, len(entry.Authors))
	for _, a := range entry.Authors {
		authors = append(authors, a.Name)
	}

	var publisher string
	if len(entry.Publishers) > 0 {
		publisher = entry.Publishers[0].Name
	}

	return &book.Book{
		ISBN:      isbn,
		Title:     entry.Title,
		Author:    strings.Join(authors, ", "),
		Publisher: publisher,
		Year:      extractYear(entry.PublishDate),
		CoverURL:  entry.Cover.Medium,
		Pages:     entry.NumberOfPages,
	}, nil
}

var _ Provider = (*OpenLibraryProvider)(nil)
