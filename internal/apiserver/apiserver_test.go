package apiserver_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/larskristianhaga/hyllis.no/internal/api"
	"github.com/larskristianhaga/hyllis.no/internal/apiserver"
	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/user"
)

// fakeVerifier accepts "token-<user>" as a valid token for <user>.
type fakeVerifier struct{}

func (fakeVerifier) Verify(token string) (auth.Claims, error) {
	id, ok := strings.CutPrefix(token, "token-")
	if !ok {
		return auth.Claims{}, errors.New("bad token")
	}
	return auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: id}, Email: id + "@example.com"}, nil
}

// fakeResolver knows only the books in its map.
type fakeResolver map[string]*book.Book

func (f fakeResolver) Resolve(_ context.Context, isbn string) (*book.Book, error) {
	if b, ok := f[isbn]; ok {
		c := *b
		return &c, nil
	}
	return nil, lookup.ErrNotFound
}

const known = "9788202000001"

func newTestAPI(t *testing.T) *api.Client {
	t.Helper()
	srv := httptest.NewServer(apiserver.Handler(apiserver.Deps{
		Books:    book.NewMemoryRepository(nil),
		Library:  library.NewMemoryRepository(),
		Users:    user.NewMemoryRepository(),
		Lookup:   fakeResolver{known: {ISBN: known, Title: "Sult", Author: "Knut Hamsun", Source: "nb"}},
		Verifier: fakeVerifier{},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL)
}

func TestRequiresBearerToken(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()
	for _, tok := range []string{"", "garbage"} {
		_, err := c.List(ctx, tok, api.ListOptions{})
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
			t.Errorf("token %q: err = %v, want 401", tok, err)
		}
	}
}

func TestLookup(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()

	b, err := c.Lookup(ctx, "token-alice", known)
	if err != nil || b.Title != "Sult" {
		t.Fatalf("Lookup(known) = %+v, %v", b, err)
	}
	if _, err := c.Lookup(ctx, "token-alice", "9780000000000"); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("Lookup(unknown) err = %v, want not_found", err)
	}
	if _, err := c.Lookup(ctx, "token-alice", "123"); !api.IsCode(err, api.CodeInvalidISBN) {
		t.Errorf("Lookup(bad) err = %v, want invalid_isbn", err)
	}
}

func TestLibraryLifecycleAndOwnership(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()
	alice, bob := "token-alice", "token-bob"
	sult := book.Book{ISBN: known, Title: "Sult", Author: "Knut Hamsun", Source: "nb"}

	res, err := c.Add(ctx, alice, api.AddRequest{Book: sult})
	if err != nil || res.Existed || res.Item.ID == "" || res.Item.Status != library.StatusUnread {
		t.Fatalf("first Add = %+v, %v", res, err)
	}
	id := res.Item.ID

	// Double-scan returns the existing entry instead of a duplicate.
	again, err := c.Add(ctx, alice, api.AddRequest{Book: sult})
	if err != nil || !again.Existed || again.Item.ID != id {
		t.Fatalf("second Add = %+v, %v", again, err)
	}

	// Another user shares the book row but gets their own entry.
	bobs, err := c.Add(ctx, bob, api.AddRequest{Book: sult})
	if err != nil || bobs.Existed || bobs.Item.ID == id {
		t.Fatalf("bob's Add = %+v, %v", bobs, err)
	}

	// Bob cannot see, edit or delete Alice's entry.
	if _, err := c.Get(ctx, bob, id); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("bob Get(alice's) err = %v, want not_found", err)
	}
	if _, err := c.Update(ctx, bob, id, api.EntryUpdate{Status: library.StatusRead}); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("bob Update(alice's) err = %v, want not_found", err)
	}
	if err := c.Delete(ctx, bob, id); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("bob Delete(alice's) err = %v, want not_found", err)
	}

	owned, err := c.GetByISBN(ctx, alice, known)
	if err != nil || owned.ID != id {
		t.Errorf("GetByISBN = %+v, %v", owned, err)
	}

	upd, err := c.Update(ctx, alice, id, api.EntryUpdate{Status: library.StatusReading, Notes: " god ", Location: "Stue"})
	if err != nil || upd.Status != library.StatusReading || upd.Notes != "god" || upd.Location != "Stue" {
		t.Errorf("Update = %+v, %v", upd, err)
	}
	if _, err := c.Update(ctx, alice, id, api.EntryUpdate{Status: "bogus"}); !api.IsCode(err, api.CodeInvalidStatus) {
		t.Errorf("Update(bogus status) err = %v", err)
	}
	if _, err := c.Update(ctx, alice, id, api.EntryUpdate{Status: library.StatusRead, Notes: strings.Repeat("x", api.MaxNotesLen+1)}); !api.IsCode(err, api.CodeNotesTooLong) {
		t.Errorf("Update(long notes) err = %v", err)
	}

	if err := c.Delete(ctx, alice, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.Get(ctx, alice, id); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("Get after delete err = %v", err)
	}
	if _, err := c.Get(ctx, bob, bobs.Item.ID); err != nil {
		t.Errorf("bob's entry should survive alice's delete: %v", err)
	}
}

func TestAddValidation(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()
	cases := map[string]api.AddRequest{
		api.CodeInvalidISBN: {Book: book.Book{ISBN: "12", Title: "T", Author: "A"}},
		api.CodeInvalidBook: {Book: book.Book{ISBN: known, Title: "", Author: "A"}},
	}
	for code, req := range cases {
		if _, err := c.Add(ctx, "token-alice", req); !api.IsCode(err, code) {
			t.Errorf("Add(%+v) err = %v, want %s", req.Book, err, code)
		}
	}
	bad := api.AddRequest{Book: book.Book{ISBN: known, Title: "T", Author: "A", Source: "evil"}}
	if _, err := c.Add(ctx, "token-alice", bad); !api.IsCode(err, api.CodeInvalidBook) {
		t.Errorf("Add(bad source) err = %v", err)
	}
}

func TestListSearchSortAndPaging(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()
	tok := "token-alice"

	titles := []string{"Bøker", "Anna", "Cirkus", "Dans", "Elv"}
	for i, title := range titles {
		isbn := "978000000000" + string(rune('0'+i))
		if _, err := c.Add(ctx, tok, api.AddRequest{Book: book.Book{ISBN: isbn, Title: title, Author: "Forfatter " + title}}); err != nil {
			t.Fatal(err)
		}
	}

	p, err := c.List(ctx, tok, api.ListOptions{Sort: api.SortTitle, PerPage: 2})
	if err != nil || p.Total != 5 || p.Matches != 5 || len(p.Items) != 2 || p.NextPage != 2 || p.Items[0].Book.Title != "Anna" {
		t.Fatalf("page 1 = %+v, %v", p, err)
	}
	p, err = c.List(ctx, tok, api.ListOptions{Sort: api.SortTitle, PerPage: 2, Page: 3})
	if err != nil || len(p.Items) != 1 || p.NextPage != 0 || p.Items[0].Book.Title != "Elv" {
		t.Fatalf("page 3 = %+v, %v", p, err)
	}

	// Fuzzy, typo-tolerant search; Total stays the unfiltered size.
	p, err = c.List(ctx, tok, api.ListOptions{Q: "cirkuss"})
	if err != nil || p.Total != 5 || p.Matches != 1 || p.Items[0].Book.Title != "Cirkus" {
		t.Fatalf("search = %+v, %v", p, err)
	}

	p, err = c.List(ctx, tok, api.ListOptions{PerPage: -1})
	if err != nil || len(p.Items) != 5 {
		t.Fatalf("all = %d items, %v", len(p.Items), err)
	}

	// Another user's library is empty.
	p, err = c.List(ctx, "token-bob", api.ListOptions{})
	if err != nil || p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("bob's list = %+v, %v", p, err)
	}
}

func TestImport(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()
	tok := "token-alice"

	if _, err := c.Add(ctx, tok, api.AddRequest{Book: book.Book{ISBN: known, Title: "Sult", Author: "Knut Hamsun"}}); err != nil {
		t.Fatal(err)
	}
	res, err := c.Import(ctx, tok, api.ImportRequest{Items: []api.AddRequest{
		{Book: book.Book{ISBN: known, Title: "Sult", Author: "Knut Hamsun"}},
		{Book: book.Book{ISBN: "9788202000002", Title: "Ny", Author: "A"}, Status: library.StatusRead, Notes: "n"},
		{Book: book.Book{ISBN: "bad", Title: "Ny", Author: "A"}},
	}})
	if err != nil || res.Added != 1 || res.Existing != 1 || res.Skipped != 1 {
		t.Fatalf("Import = %+v, %v", res, err)
	}
	it, err := c.GetByISBN(ctx, tok, "9788202000002")
	if err != nil || it.Status != library.StatusRead || it.Notes != "n" {
		t.Errorf("imported entry = %+v, %v", it, err)
	}
}

func TestProfile(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()

	if _, err := c.Me(ctx, "token-alice"); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("Me before sync err = %v", err)
	}
	if err := c.SyncProfile(ctx, "token-alice", api.ProfileUpdate{DisplayName: "Alice"}); err != nil {
		t.Fatal(err)
	}
	p, err := c.Me(ctx, "token-alice")
	if err != nil || p.ID != "alice" || p.Email != "alice@example.com" || p.DisplayName != "Alice" {
		t.Errorf("Me = %+v, %v", p, err)
	}
}
