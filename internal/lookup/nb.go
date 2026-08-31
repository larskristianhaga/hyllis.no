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

// nbBaseURL is a var (not a const) so tests can point it at an
// httptest.Server.
//
// CLAUDE.md names the legacy "nb.no/services/search/v2" endpoint, but that
// host no longer answers (confirmed at plan time). api.nb.no/catalog/v1 is
// Nasjonalbiblioteket's current, live catalog search API and is used here
// instead — same role (Norwegian-title fallback), same "miss just means no
// hit" contract as the other providers.
var nbBaseURL = "https://api.nb.no/catalog/v1/items"

// NBProvider resolves ISBNs against Nasjonalbiblioteket's catalog, as a
// last-resort fallback for Norwegian titles the other two providers don't
// have.
type NBProvider struct {
	client *http.Client
}

// NewNBProvider builds an NBProvider.
func NewNBProvider() *NBProvider {
	return &NBProvider{client: &http.Client{Timeout: 5 * time.Second}}
}

func (p *NBProvider) Name() string { return "nb" }

type nbResponse struct {
	Embedded struct {
		Items []struct {
			Metadata struct {
				Title      string   `json:"title"`
				Creators   []string `json:"creators"`
				OriginInfo struct {
					Issued string `json:"issued"`
				} `json:"originInfo"`
			} `json:"metadata"`
		} `json:"items"`
	} `json:"_embedded"`
}

func (p *NBProvider) Lookup(ctx context.Context, isbn string) (*book.Book, error) {
	q := url.Values{
		"q":    {"isbn:" + isbn},
		"size": {"1"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nbBaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("lookup: build nb request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup: nb request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lookup: nb returned status %d", resp.StatusCode)
	}

	var parsed nbResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("lookup: decode nb response: %w", err)
	}
	if len(parsed.Embedded.Items) == 0 {
		return nil, ErrNotFound
	}

	meta := parsed.Embedded.Items[0].Metadata
	if meta.Title == "" {
		return nil, ErrNotFound
	}

	return &book.Book{
		ISBN:   isbn,
		Title:  meta.Title,
		Author: strings.Join(meta.Creators, ", "),
		Year:   extractYear(meta.OriginInfo.Issued),
	}, nil
}

var _ Provider = (*NBProvider)(nil)
