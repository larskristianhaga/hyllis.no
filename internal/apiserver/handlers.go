package apiserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/larskristianhaga/hyllis.no/internal/api"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/fuzzy"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/user"
)

type service struct {
	books   book.Repository
	library library.Repository
	users   user.Repository
	lookup  Resolver
	log     *slog.Logger
}

// --- profile ---------------------------------------------------------

func (s *service) getMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	u, err := s.users.GetByID(r.Context(), claims.UserID())
	if errors.Is(err, user.ErrNotFound) {
		writeError(w, http.StatusNotFound, api.CodeNotFound, "no profile for this user yet")
		return
	}
	if err != nil {
		s.internalError(w, "get profile", err)
		return
	}
	writeJSON(w, http.StatusOK, api.Profile{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName})
}

// putMe upserts the local profile mirror (internal/user) for the caller.
// The id and email come from the verified token; only the display name is
// taken from the body (falling back to the email, like auth.User does).
func (s *service) putMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	var body api.ProfileUpdate
	if !decodeJSON(r, &body) {
		writeError(w, http.StatusBadRequest, api.CodeBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if name == "" {
		name = claims.Email
	}
	u := &user.User{ID: claims.UserID(), Email: claims.Email, DisplayName: name}
	if err := s.users.Upsert(r.Context(), u); err != nil {
		s.internalError(w, "upsert profile", err)
		return
	}
	writeJSON(w, http.StatusOK, api.Profile{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName})
}

// --- lookup ----------------------------------------------------------

// lookupISBN is the only endpoint that talks to external services: it
// resolves an ISBN via the Redis cache → Google Books / Open Library / NB /
// bibliotekenes.no chain (internal/lookup) without saving anything.
func (s *service) lookupISBN(w http.ResponseWriter, r *http.Request) {
	isbn := r.PathValue("isbn")
	if !api.ValidEAN13(isbn) {
		writeError(w, http.StatusBadRequest, api.CodeInvalidISBN, "isbn must be 13 digits")
		return
	}
	b, err := s.lookup.Resolve(r.Context(), isbn)
	if errors.Is(err, lookup.ErrNotFound) {
		writeError(w, http.StatusNotFound, api.CodeNotFound, "no provider knows this isbn")
		return
	}
	if err != nil {
		s.internalError(w, "lookup isbn", err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// --- library ---------------------------------------------------------

func (s *service) list(w http.ResponseWriter, r *http.Request) {
	opts := parseListOptions(r)
	rows, err := s.userRows(r.Context(), claimsFrom(r).UserID())
	if err != nil {
		s.internalError(w, "list library", err)
		return
	}
	writeJSON(w, http.StatusOK, pageOf(rows, opts))
}

func (s *service) get(w http.ResponseWriter, r *http.Request) {
	row, err := s.ownedRow(r.Context(), claimsFrom(r).UserID(), r.PathValue("id"))
	if err != nil {
		s.notFoundOr500(w, "get entry", err)
		return
	}
	writeJSON(w, http.StatusOK, row.item())
}

func (s *service) getByISBN(w http.ResponseWriter, r *http.Request) {
	isbn := r.PathValue("isbn")
	b, err := s.books.GetByISBN(r.Context(), isbn)
	if err != nil {
		s.notFoundOr500(w, "get book by isbn", err)
		return
	}
	e, err := s.library.GetByUserAndBook(r.Context(), claimsFrom(r).UserID(), b.ID)
	if err != nil {
		s.notFoundOr500(w, "get entry by book", err)
		return
	}
	writeJSON(w, http.StatusOK, libraryRow{Book: b, Entry: e}.item())
}

func (s *service) add(w http.ResponseWriter, r *http.Request) {
	var req api.AddRequest
	if !decodeJSON(r, &req) {
		writeError(w, http.StatusBadRequest, api.CodeBadRequest, "invalid JSON body")
		return
	}
	if code, msg := validateAdd(&req); code != "" {
		writeError(w, http.StatusUnprocessableEntity, code, msg)
		return
	}

	row, existed, err := s.save(r.Context(), claimsFrom(r).UserID(), req)
	if err != nil {
		s.internalError(w, "save book", err)
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	writeJSON(w, status, api.AddResult{Item: row.item(), Existed: existed})
}

// importItems adds many books at once using only the data given — no
// external lookups, per the rule that only lookup talks to external
// services. Invalid or failing items are counted as skipped.
func (s *service) importItems(w http.ResponseWriter, r *http.Request) {
	var req api.ImportRequest
	if !decodeJSON(r, &req) {
		writeError(w, http.StatusBadRequest, api.CodeBadRequest, "invalid JSON body")
		return
	}
	if len(req.Items) > api.MaxImportItems {
		writeError(w, http.StatusUnprocessableEntity, api.CodeTooManyItems, "too many items")
		return
	}

	userID := claimsFrom(r).UserID()
	var res api.ImportResult
	for _, item := range req.Items {
		if code, _ := validateAdd(&item); code != "" {
			res.Skipped++
			continue
		}
		_, existed, err := s.save(r.Context(), userID, item)
		switch {
		case err != nil:
			s.log.Error("import: failed to save book", "isbn", item.Book.ISBN, "error", err)
			res.Skipped++
		case existed:
			res.Existing++
		default:
			res.Added++
		}
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *service) update(w http.ResponseWriter, r *http.Request) {
	userID := claimsFrom(r).UserID()
	row, err := s.ownedRow(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		s.notFoundOr500(w, "get entry", err)
		return
	}

	var u api.EntryUpdate
	if !decodeJSON(r, &u) {
		writeError(w, http.StatusBadRequest, api.CodeBadRequest, "invalid JSON body")
		return
	}
	u.Notes, u.Location = strings.TrimSpace(u.Notes), strings.TrimSpace(u.Location)
	switch {
	case !library.ValidStatus(u.Status):
		writeError(w, http.StatusUnprocessableEntity, api.CodeInvalidStatus, "status must be unread, reading or read")
		return
	case utf8.RuneCountInString(u.Notes) > api.MaxNotesLen:
		writeError(w, http.StatusUnprocessableEntity, api.CodeNotesTooLong, "notes too long")
		return
	case utf8.RuneCountInString(u.Location) > api.MaxLocationLen:
		writeError(w, http.StatusUnprocessableEntity, api.CodeLocationTooLong, "location too long")
		return
	}

	row.Entry.Status, row.Entry.Notes, row.Entry.Location = u.Status, u.Notes, u.Location
	if err := s.library.Update(r.Context(), row.Entry); err != nil {
		s.internalError(w, "update entry", err)
		return
	}
	writeJSON(w, http.StatusOK, row.item())
}

func (s *service) delete(w http.ResponseWriter, r *http.Request) {
	row, err := s.ownedRow(r.Context(), claimsFrom(r).UserID(), r.PathValue("id"))
	if err != nil {
		s.notFoundOr500(w, "get entry", err)
		return
	}
	if err := s.library.Delete(r.Context(), row.Entry.ID); err != nil {
		s.notFoundOr500(w, "delete entry", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- domain logic ------------------------------------------------------

// libraryRow pairs a library entry with the (shared) book it points at.
type libraryRow struct {
	Book  *book.Book
	Entry *library.Entry
}

func (row libraryRow) item() api.Item {
	status := row.Entry.Status
	if !library.ValidStatus(status) {
		status = library.StatusUnread
	}
	return api.Item{
		ID:       row.Entry.ID,
		AddedAt:  row.Entry.AddedAt,
		Status:   status,
		Notes:    row.Entry.Notes,
		Location: row.Entry.Location,
		Book:     *row.Book,
	}
}

// validateAdd normalizes req in place and reports the first problem as an
// api error code + message ("" when valid).
func validateAdd(req *api.AddRequest) (code, msg string) {
	b := &req.Book
	b.ID = ""
	b.ISBN = strings.TrimSpace(b.ISBN)
	b.Title = strings.TrimSpace(b.Title)
	b.Author = strings.TrimSpace(b.Author)
	b.Publisher = strings.TrimSpace(b.Publisher)
	b.CoverURL = strings.TrimSpace(b.CoverURL)
	b.Language = strings.TrimSpace(b.Language)
	if b.Source == "" {
		b.Source = "manual"
	}
	if req.Status == "" {
		req.Status = library.StatusUnread
	}
	req.Notes, req.Location = strings.TrimSpace(req.Notes), strings.TrimSpace(req.Location)

	switch {
	case !api.ValidEAN13(b.ISBN):
		return api.CodeInvalidISBN, "isbn must be 13 digits"
	case b.Title == "" || b.Author == "":
		return api.CodeInvalidBook, "title and author are required"
	case !validSource(b.Source):
		return api.CodeInvalidBook, "unknown source"
	case !library.ValidStatus(req.Status):
		return api.CodeInvalidStatus, "status must be unread, reading or read"
	case utf8.RuneCountInString(req.Notes) > api.MaxNotesLen:
		return api.CodeNotesTooLong, "notes too long"
	case utf8.RuneCountInString(req.Location) > api.MaxLocationLen:
		return api.CodeLocationTooLong, "location too long"
	}
	return "", ""
}

// validSource mirrors the check constraint on books.source (migrations
// 000005/000006).
func validSource(s string) bool {
	switch s {
	case "google_books", "open_library", "nb", "bibliotekenes", "manual":
		return true
	}
	return false
}

// save persists a resolved-but-not-yet-saved book and links it into
// userID's library. If the ISBN already exists (the books table is shared
// between users), it reuses that row instead of creating a duplicate. If
// the user already has the book (double-scan/double-submit), it returns
// their existing entry with existed=true instead of erroring. req's
// status/notes/location apply only to a newly created entry.
func (s *service) save(ctx context.Context, userID string, req api.AddRequest) (libraryRow, bool, error) {
	b := req.Book
	if err := s.books.Create(ctx, &b); err != nil {
		if !errors.Is(err, book.ErrDuplicateISBN) {
			return libraryRow{}, false, err
		}
		existing, err := s.books.GetByISBN(ctx, b.ISBN)
		if err != nil {
			return libraryRow{}, false, err
		}
		b = *existing
	}

	entry := &library.Entry{UserID: userID, BookID: b.ID, Status: req.Status, Notes: req.Notes, Location: req.Location}
	existed := false
	if err := s.library.Create(ctx, entry); err != nil {
		if !errors.Is(err, library.ErrDuplicateEntry) {
			return libraryRow{}, false, err
		}
		existing, err := s.library.GetByUserAndBook(ctx, userID, b.ID)
		if err != nil {
			return libraryRow{}, false, err
		}
		entry, existed = existing, true
	}
	return libraryRow{Book: &b, Entry: entry}, existed, nil
}

// ownedRow resolves a library entry id into its entry + book, returning
// library.ErrNotFound if it doesn't exist or doesn't belong to userID.
func (s *service) ownedRow(ctx context.Context, userID, entryID string) (libraryRow, error) {
	entry, err := s.library.GetByID(ctx, entryID)
	if err != nil {
		return libraryRow{}, err
	}
	if entry.UserID != userID {
		return libraryRow{}, library.ErrNotFound
	}
	b, err := s.books.GetByID(ctx, entry.BookID)
	if err != nil {
		return libraryRow{}, err
	}
	return libraryRow{Book: b, Entry: entry}, nil
}

// userRows lists userID's library newest-added-first (see
// library.Repository.ListByUser), with each entry's book resolved.
func (s *service) userRows(ctx context.Context, userID string) ([]libraryRow, error) {
	entries, err := s.library.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]libraryRow, 0, len(entries))
	for _, e := range entries {
		b, err := s.books.GetByID(ctx, e.BookID)
		if err != nil {
			s.log.Error("failed to resolve library entry's book", "entry_id", e.ID, "book_id", e.BookID, "error", err)
			continue
		}
		out = append(out, libraryRow{Book: b, Entry: e})
	}
	return out, nil
}

func parseListOptions(r *http.Request) api.ListOptions {
	v := r.URL.Query()
	o := api.ListOptions{
		Q:     strings.TrimSpace(v.Get("q")),
		Field: v.Get("field"),
		Sort:  v.Get("sort"),
		Page:  1,
	}
	switch o.Field {
	case api.FieldTitle, api.FieldAuthor, api.FieldPublisher:
	default:
		o.Field = api.FieldAll
	}
	switch o.Sort {
	case api.SortNewest, api.SortOldest, api.SortTitle, api.SortAuthor:
	default:
		o.Sort = api.SortNewest
	}
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n > 1 {
		o.Page = min(n, 10000)
	}
	o.PerPage = api.DefaultPerPage
	if n, err := strconv.Atoi(v.Get("per_page")); err == nil && n != 0 {
		o.PerPage = min(n, 1000)
	}
	return o
}

// pageOf filters, sorts and paginates rows (newest-added-first) per opts.
// Search is always local — never an external call. Q fuzzily matches
// title, author and publisher together; Field is a wholly separate,
// display-only filter (see api.ListOptions).
func pageOf(rows []libraryRow, opts api.ListOptions) api.LibraryPage {
	matches := make([]api.Item, 0, len(rows))
	for _, row := range rows {
		b := row.Book
		if opts.Q != "" && !fuzzy.Match(b.Title+" "+b.Author+" "+b.Publisher, opts.Q) {
			continue
		}
		if !passesFieldFilter(b, opts.Field) {
			continue
		}
		matches = append(matches, row.item())
	}
	sortItems(matches, opts.Sort)

	page := api.LibraryPage{Items: []api.Item{}, Total: len(rows), Matches: len(matches)}
	if opts.PerPage < 0 {
		page.Items = matches
		return page
	}
	start := (opts.Page - 1) * opts.PerPage
	if start < len(matches) {
		end := min(start+opts.PerPage, len(matches))
		page.Items = matches[start:end]
		if end < len(matches) {
			page.NextPage = opts.Page + 1
		}
	}
	return page
}

// sortItems orders items in place. The input is newest-added-first and the
// sort is stable, so ties keep that deterministic order.
func sortItems(items []api.Item, order string) {
	switch order {
	case api.SortOldest:
		slices.Reverse(items)
	case api.SortTitle:
		slices.SortStableFunc(items, func(a, b api.Item) int {
			return strings.Compare(strings.ToLower(a.Book.Title), strings.ToLower(b.Book.Title))
		})
	case api.SortAuthor:
		slices.SortStableFunc(items, func(a, b api.Item) int {
			return strings.Compare(strings.ToLower(a.Book.Author), strings.ToLower(b.Book.Author))
		})
	}
}

func passesFieldFilter(b *book.Book, field string) bool {
	switch field {
	case api.FieldTitle:
		return b.Title != ""
	case api.FieldAuthor:
		return b.Author != ""
	case api.FieldPublisher:
		return b.Publisher != ""
	default:
		return true
	}
}

// --- errors ----------------------------------------------------------

func (s *service) notFoundOr500(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, library.ErrNotFound) || errors.Is(err, book.ErrNotFound) {
		writeError(w, http.StatusNotFound, api.CodeNotFound, "not found")
		return
	}
	s.internalError(w, op, err)
}

func (s *service) internalError(w http.ResponseWriter, op string, err error) {
	s.log.Error("api: "+op+" failed", "error", err)
	writeError(w, http.StatusInternalServerError, api.CodeInternal, "internal error")
}
