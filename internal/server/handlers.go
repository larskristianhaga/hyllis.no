package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// handlers wires the HTTP routes to the template renderer and the book
// repository (an in-memory mock until a real store is wired up).
type handlers struct {
	render *web.Renderer
	books  book.Repository
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
}

type scanResultData struct {
	Book *book.Book
}

type bookDetailData struct {
	Book    *book.Book
	Message string
}

type errorMessageData struct {
	Message string
}

func newHandlers(render *web.Renderer, books book.Repository) *handlers {
	return &handlers{render: render, books: books}
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

func (h *handlers) libraryPage(w http.ResponseWriter, r *http.Request) {
	books, _ := h.books.List(r.Context())
	h.render.Page(w, r, "library", searchResultsData{Books: books})
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
// ISBN fallback form. It always returns a fragment: a scan-result partial
// on success, or an error-message partial on invalid input / no match.
func (h *handlers) scanSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	isbn := strings.TrimSpace(r.FormValue("isbn"))
	if !isValidEAN13(isbn) {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Ugyldig ISBN/EAN-13-kode. Koden må bestå av 13 siffer.",
		})
		return
	}

	b, err := h.books.GetByID(r.Context(), isbn)
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Fant ingen bok med ISBN " + isbn + ".",
		})
		return
	}

	h.render.Partial(w, "scan-result", scanResultData{Book: b})
}

// librarySearch backs the search-as-you-type input on the library page.
func (h *handlers) librarySearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	all, err := h.books.List(r.Context())
	if err != nil {
		h.render.Partial(w, "search-results", searchResultsData{Query: query})
		return
	}

	if query == "" {
		h.render.Partial(w, "search-results", searchResultsData{Books: all, Query: query})
		return
	}

	needle := strings.ToLower(query)
	matches := make([]*book.Book, 0, len(all))
	for _, b := range all {
		if strings.Contains(strings.ToLower(b.Title), needle) || strings.Contains(strings.ToLower(b.Author), needle) {
			matches = append(matches, b)
		}
	}

	h.render.Partial(w, "search-results", searchResultsData{Books: matches, Query: query})
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
