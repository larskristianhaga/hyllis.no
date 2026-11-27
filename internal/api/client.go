package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
)

// Client calls the hyllis JSON API. Every method takes the end user's
// Supabase access token and forwards it as a Bearer token. Errors returned
// by the API come back as *Error.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a Client for the API at baseURL (e.g.
// "http://127.0.0.1:8081").
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// Lookups fan out to four external providers on a cache miss, so
		// leave generous headroom over their own timeouts.
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Me returns the caller's profile.
func (c *Client) Me(ctx context.Context, token string) (*Profile, error) {
	var p Profile
	return &p, c.do(ctx, token, http.MethodGet, "/v1/me", nil, &p)
}

// SyncProfile creates or refreshes the caller's profile mirror; call it
// after every successful login/signup.
func (c *Client) SyncProfile(ctx context.Context, token string, u ProfileUpdate) error {
	return c.do(ctx, token, http.MethodPut, "/v1/me", u, nil)
}

// Lookup resolves an ISBN via the API's cache → external providers chain.
// It returns an *Error with CodeNotFound when every provider missed.
func (c *Client) Lookup(ctx context.Context, token, isbn string) (*book.Book, error) {
	var b book.Book
	return &b, c.do(ctx, token, http.MethodGet, "/v1/lookup/"+url.PathEscape(isbn), nil, &b)
}

// List returns one page of the caller's library.
func (c *Client) List(ctx context.Context, token string, opts ListOptions) (*LibraryPage, error) {
	q := url.Values{}
	if opts.Q != "" {
		q.Set("q", opts.Q)
	}
	if opts.Field != "" {
		q.Set("field", opts.Field)
	}
	if opts.Sort != "" {
		q.Set("sort", opts.Sort)
	}
	if opts.Page > 1 {
		q.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PerPage != 0 {
		q.Set("per_page", strconv.Itoa(opts.PerPage))
	}
	path := "/v1/library"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var page LibraryPage
	return &page, c.do(ctx, token, http.MethodGet, path, nil, &page)
}

// Get returns one library entry by its entry id.
func (c *Client) Get(ctx context.Context, token, id string) (*Item, error) {
	var it Item
	return &it, c.do(ctx, token, http.MethodGet, "/v1/library/"+url.PathEscape(id), nil, &it)
}

// GetByISBN returns the caller's entry for the book with this ISBN, or an
// *Error with CodeNotFound if they don't own it.
func (c *Client) GetByISBN(ctx context.Context, token, isbn string) (*Item, error) {
	var it Item
	return &it, c.do(ctx, token, http.MethodGet, "/v1/library/isbn/"+url.PathEscape(isbn), nil, &it)
}

// Add adds a book to the caller's library (or returns their existing entry).
func (c *Client) Add(ctx context.Context, token string, req AddRequest) (*AddResult, error) {
	var res AddResult
	return &res, c.do(ctx, token, http.MethodPost, "/v1/library", req, &res)
}

// Import adds many books at once, using only the data given (no lookups).
func (c *Client) Import(ctx context.Context, token string, req ImportRequest) (*ImportResult, error) {
	var res ImportResult
	return &res, c.do(ctx, token, http.MethodPost, "/v1/library/import", req, &res)
}

// Update edits an entry's reading status, notes and location.
func (c *Client) Update(ctx context.Context, token, id string, u EntryUpdate) (*Item, error) {
	var it Item
	return &it, c.do(ctx, token, http.MethodPatch, "/v1/library/"+url.PathEscape(id), u, &it)
}

// Delete removes an entry from the caller's library.
func (c *Client) Delete(ctx context.Context, token, id string) error {
	return c.do(ctx, token, http.MethodDelete, "/v1/library/"+url.PathEscape(id), nil, nil)
}

func (c *Client) do(ctx context.Context, token, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("api: encode request: %w", err)
		}
		rd = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return fmt.Errorf("api: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		apiErr := &Error{Status: resp.StatusCode}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(apiErr); err != nil || apiErr.Code == "" {
			apiErr.Code = CodeInternal
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return apiErr
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("api: decode %s %s: %w", method, path, err)
	}
	return nil
}
