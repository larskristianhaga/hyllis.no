package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// bibliotekenesBaseURL is a var (not a const) so tests can point it at an
// httptest.Server.
var bibliotekenesBaseURL = "https://www.bibliotekenes.no/api/e-commerce/search/bibsent-suggestions"

// BibliotekenesProvider resolves ISBNs against bibliotekenes.no's product
// search API (Biblioteksentralen's e-commerce catalog). It's tried last, per
// CLAUDE.md's provider priority order — a fallback for whatever Google
// Books, Open Library, and Nasjonalbiblioteket all miss.
type BibliotekenesProvider struct {
	client *http.Client
}

// NewBibliotekenesProvider builds a BibliotekenesProvider.
func NewBibliotekenesProvider() *BibliotekenesProvider {
	return &BibliotekenesProvider{client: &http.Client{Timeout: 5 * time.Second}}
}

func (p *BibliotekenesProvider) Name() string { return "bibliotekenes" }

type bibliotekenesProduct struct {
	ISBN13  string `json:"isbn13"`
	Title   string `json:"title"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Imprints        []string `json:"imprints"`
	PublicationYear string   `json:"publication_year"`
	NumberOfPages   int      `json:"number_of_pages"`
	Languages       []struct {
		Name string `json:"name"`
	} `json:"languages"`
	Image struct {
		URL string `json:"url"`
	} `json:"image"`
}

type bibliotekenesResponse struct {
	Products []bibliotekenesProduct `json:"products"`
}

func (p *BibliotekenesProvider) Lookup(ctx context.Context, isbn string) (*book.Book, error) {
	q := url.Values{
		"query": {isbn},
		"limit": {"10"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bibliotekenesBaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("lookup: build bibliotekenes request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup: bibliotekenes request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lookup: bibliotekenes returned status %d", resp.StatusCode)
	}

	var parsed bibliotekenesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("lookup: decode bibliotekenes response: %w", err)
	}

	// "query=<isbn>" is a search, not an exact filter — only trust a product
	// whose own isbn13 actually matches what we asked for.
	var product *bibliotekenesProduct
	for i := range parsed.Products {
		if parsed.Products[i].ISBN13 == isbn {
			product = &parsed.Products[i]
			break
		}
	}
	if product == nil || product.Title == "" {
		return nil, ErrNotFound
	}

	authors := make([]string, 0, len(product.Authors))
	for _, a := range product.Authors {
		authors = append(authors, a.Name)
	}

	var publisher string
	if len(product.Imprints) > 0 {
		publisher = product.Imprints[0]
	}

	var language string
	if len(product.Languages) > 0 {
		language = product.Languages[0].Name
	}

	return &book.Book{
		ISBN:      isbn,
		Title:     product.Title,
		Author:    strings.Join(authors, ", "),
		Publisher: publisher,
		Year:      yearFromString(product.PublicationYear),
		CoverURL:  product.Image.URL,
		Language:  language,
		Pages:     product.NumberOfPages,
	}, nil
}

// yearFromString parses a plain 4-digit year string (bibliotekenes returns
// "publication_year" as e.g. "2026", not a full date), falling back to 0 on
// anything unparseable.
func yearFromString(s string) int {
	year, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return year
}

var _ Provider = (*BibliotekenesProvider)(nil)
