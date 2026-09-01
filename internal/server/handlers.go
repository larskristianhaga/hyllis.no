package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/fuzzy"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// handlers wires the HTTP routes to the template renderer, the book
// repository (an in-memory mock until a real store is wired up), and the
// ISBN lookup service.
type handlers struct {
	render *web.Renderer
	books  book.Repository
	lookup *lookup.Service
	log    *slog.Logger
}

// homeRecentLimit caps how many books the home page's "recently added"
// section shows.
const homeRecentLimit = 4

type homeData struct {
	Books []*book.Book
}

type searchResultsData struct {
	Books []*book.Book
	Query string
	Field string
}

type scanResultData struct {
	Book *book.Book
}

type scanPreviewData struct {
	Book *book.Book
}

type bookDetailData struct {
	Book    *book.Book
	Message string
}

type errorMessageData struct {
	Message string
}

type manualEntryFormData struct {
	ISBN string
}

func newHandlers(render *web.Renderer, books book.Repository, lookupSvc *lookup.Service, log *slog.Logger) *handlers {
	return &handlers{render: render, books: books, lookup: lookupSvc, log: log}
}

// --- full pages ------------------------------------------------------

func (h *handlers) home(w http.ResponseWriter, r *http.Request) {
	books, err := h.books.List(r.Context())
	if err != nil {
		h.render.Page(w, r, "home", homeData{})
		return
	}
	if len(books) > homeRecentLimit {
		books = books[:homeRecentLimit]
	}
	h.render.Page(w, r, "home", homeData{Books: books})
}

func (h *handlers) scanPage(w http.ResponseWriter, r *http.Request) {
	h.render.Page(w, r, "scan", nil)
}

// libraryPage backs both GET /library (the HTMX page) and GET /books (the
// documented REST route for "søk/filtrer i eget bibliotek" — kept as an
// alias of the same handler rather than a separate JSON API, matching this
// app's server-rendered-HTML architecture). Optional "q"/"field" query
// params filter the initial page load the same way /books/search does.
func (h *handlers) libraryPage(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	field := r.URL.Query().Get("field")
	if field == "" {
		field = defaultSearchField
	}

	books, err := h.filterBooks(r, query, field)
	if err != nil {
		h.render.Page(w, r, "library", searchResultsData{Field: field})
		return
	}
	h.render.Page(w, r, "library", searchResultsData{Books: books, Query: query, Field: field})
}

func (h *handlers) bookDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := h.books.GetByID(r.Context(), id)
	if err != nil {
		h.render.PageWithStatus(w, r, "book_detail", http.StatusNotFound, bookDetailData{Message: "Boken ble ikke funnet."})
		return
	}
	h.render.Page(w, r, "book_detail", bookDetailData{Book: b})
}

func (h *handlers) loginPage(w http.ResponseWriter, r *http.Request) {
	h.render.Page(w, r, "login", errorMessageData{})
}

func (h *handlers) registerPage(w http.ResponseWriter, r *http.Request) {
	h.render.Page(w, r, "register", errorMessageData{})
}

// loginSubmit and registerSubmit have nothing to authenticate against yet
// (no auth/DB stream wired up). They re-render the form with a notice
// instead of 404ing, so the pages stay testable end-to-end by hand.
func (h *handlers) loginSubmit(w http.ResponseWriter, r *http.Request) {
	h.render.Page(w, r, "login", errorMessageData{Message: "Innlogging er ikke tilgjengelig ennå."})
}

func (h *handlers) registerSubmit(w http.ResponseWriter, r *http.Request) {
	h.render.Page(w, r, "register", errorMessageData{Message: "Registrering er ikke tilgjengelig ennå."})
}

// --- HTMX partial endpoints -------------------------------------------

// scanSubmit backs both the camera scanner (via htmx.ajax) and the manual
// ISBN fallback form. It resolves the ISBN via the cache→Google
// Books→Open Library→Nasjonalbiblioteket chain (internal/lookup) but does
// NOT persist the result — it renders a scan-preview fragment with an
// explicit "legg til i biblioteket" button (see confirmSubmit) so scanning
// a barcode never silently adds a book. Falls back to manual-entry-form
// when nothing resolves the ISBN, or error-message on invalid input.
func (h *handlers) scanSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	isbn := strings.TrimSpace(r.FormValue("isbn"))

	source := r.FormValue("source")
	if source == "" {
		source = "manual"
	}
	switch source {
	case "camera":
		h.log.Info("scan: isbn detected by camera", "isbn", isbn)
	default:
		h.log.Info("scan: isbn typed manually and submitted", "isbn", isbn)
	}

	if !isValidEAN13(isbn) {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Ugyldig ISBN/EAN-13-kode. Koden må bestå av 13 siffer.",
		})
		return
	}

	b, err := h.lookup.Resolve(r.Context(), isbn)
	if errors.Is(err, lookup.ErrNotFound) {
		h.render.Partial(w, "manual-entry-form", manualEntryFormData{ISBN: isbn})
		return
	}
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Noe gikk feil under oppslag av ISBN " + isbn + ". Prøv igjen.",
		})
		return
	}

	h.render.Partial(w, "scan-preview", scanPreviewData{Book: b})
}

// confirmSubmit backs the "Legg til i biblioteket" button shown on the
// scan-preview fragment. It re-reads the resolved book's fields from the
// form (round-tripped as hidden inputs, since the book was never persisted
// after scanSubmit's lookup) and only now calls saveBook.
func (h *handlers) confirmSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	isbn := strings.TrimSpace(r.FormValue("isbn"))
	title := strings.TrimSpace(r.FormValue("title"))
	author := strings.TrimSpace(r.FormValue("author"))

	if !isValidEAN13(isbn) || title == "" || author == "" {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig bokdata. Prøv å skanne igjen."})
		return
	}

	source := r.FormValue("source")
	if source == "" {
		source = "manual"
	}

	year, _ := strconv.Atoi(r.FormValue("year"))
	pages, _ := strconv.Atoi(r.FormValue("pages"))

	b := &book.Book{
		ISBN:      isbn,
		Title:     title,
		Author:    author,
		Publisher: strings.TrimSpace(r.FormValue("publisher")),
		Year:      year,
		CoverURL:  strings.TrimSpace(r.FormValue("cover_url")),
		Language:  strings.TrimSpace(r.FormValue("language")),
		Pages:     pages,
		Source:    source,
	}

	saved, err := h.saveBook(r, b)
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Fant boken, men kunne ikke lagre den. Prøv igjen.",
		})
		return
	}

	h.render.Partial(w, "scan-result", scanResultData{Book: saved})
}

// manualSubmit backs the manual-entry-form shown when scanSubmit's lookup
// chain misses on every source. The book is persisted with kilde "manual"
// per the project's fallback rule.
func (h *handlers) manualSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	isbn := strings.TrimSpace(r.FormValue("isbn"))
	title := strings.TrimSpace(r.FormValue("title"))
	author := strings.TrimSpace(r.FormValue("author"))
	publisher := strings.TrimSpace(r.FormValue("publisher"))

	if !isValidEAN13(isbn) || title == "" || author == "" {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Utfyll ISBN, tittel og forfatter for å legge til boken manuelt.",
		})
		return
	}

	b := &book.Book{ISBN: isbn, Title: title, Author: author, Publisher: publisher, Source: "manual"}
	saved, err := h.saveBook(r, b)
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Kunne ikke lagre boken. Prøv igjen."})
		return
	}

	h.render.Partial(w, "scan-result", scanResultData{Book: saved})
}

// saveBook persists a resolved-but-not-yet-saved book. If the ISBN already
// exists (another user scanned/entered it before — the books table is
// shared across all users), it returns the existing row instead of
// creating a duplicate, per the project's duplicate-ISBN rule.
func (h *handlers) saveBook(r *http.Request, b *book.Book) (*book.Book, error) {
	if err := h.books.Create(r.Context(), b); err != nil {
		if errors.Is(err, book.ErrDuplicateISBN) {
			return h.books.GetByISBN(r.Context(), b.ISBN)
		}
		return nil, err
	}
	return b, nil
}

// defaultSearchField is used when the "field" query param is missing or
// unrecognized. It shows every book that matched the search query — see
// passesFieldFilter.
const defaultSearchField = "all"

// librarySearch backs the search-as-you-type input on the library page. "q"
// fuzzily matches across title, author, and publisher together; the
// "field" select is unrelated to that match — it's a separate, purely
// cosmetic display filter, see filterBooks.
func (h *handlers) librarySearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	field := r.URL.Query().Get("field")
	if field == "" {
		field = defaultSearchField
	}

	books, err := h.filterBooks(r, query, field)
	if err != nil {
		h.render.Partial(w, "search-results", searchResultsData{Query: query, Field: field})
		return
	}

	h.render.Partial(w, "search-results", searchResultsData{Books: books, Query: query, Field: field})
}

// filterBooks lists the caller's library and, if query is non-empty, keeps
// only books that fuzzily match it (typo-tolerant, matched across title,
// author, and publisher together — see internal/fuzzy). field is then
// applied as a wholly separate, display-only visibility filter via
// passesFieldFilter: it narrows which of the already-matched books are
// shown, but never affects what the query itself matches against. Shared
// by libraryPage/GET-books and librarySearch so both honor "q"/"field"
// identically — search here is always local against h.books (Postgres,
// once wired up), never an external call, per CLAUDE.md's rule that only
// the scan endpoint talks to external services.
func (h *handlers) filterBooks(r *http.Request, query, field string) ([]*book.Book, error) {
	all, err := h.books.List(r.Context())
	if err != nil {
		return nil, err
	}

	matches := make([]*book.Book, 0, len(all))
	for _, b := range all {
		if query != "" && !fuzzy.Match(b.Title+" "+b.Author+" "+b.Publisher, query) {
			continue
		}
		if !passesFieldFilter(b, field) {
			continue
		}
		matches = append(matches, b)
	}
	return matches, nil
}

// passesFieldFilter reports whether b should be visible under the "field"
// filter chosen on the library page. This is purely a display concern: it
// decides which already-matched books are shown (those with something set
// in the chosen field) and has nothing to do with the "q" search above —
// the two are intentionally independent, per this app's filter-vs-search
// separation.
func passesFieldFilter(b *book.Book, field string) bool {
	switch field {
	case "title":
		return b.Title != ""
	case "author":
		return b.Author != ""
	case "publisher":
		return b.Publisher != ""
	default:
		return true
	}
}

// bookDelete backs DELETE /books/{id}. It's a plain REST action (no HTML
// fragment to render): 204 on success, 404 if the book doesn't exist, 500
// on any other repository error. If the request came from htmx (i.e. a
// delete button on the book detail page), an HX-Redirect back to /library
// is set so the client navigates away from the now-deleted book's page.
func (h *handlers) bookDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.books.Delete(r.Context(), id); err != nil {
		if errors.Is(err, book.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/library")
	}
	w.WriteHeader(http.StatusNoContent)
}

// isValidEAN13 reports whether s looks like an EAN-13 barcode: exactly 13
// ASCII digits. It doesn't verify the checksum digit — mock data and manual
// entry don't need that level of strictness.
func isValidEAN13(s string) bool {
	if len(s) != 13 {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
