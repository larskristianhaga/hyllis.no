package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/fuzzy"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/lookup"
	"github.com/larskristianhaga/hyllis.no/internal/user"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// handlers wires the HTTP routes to the template renderer, the book/library
// repositories, the ISBN lookup service, and the Supabase Auth client.
type handlers struct {
	render  *web.Renderer
	books   book.Repository
	library library.Repository
	users   user.Repository
	auth    *auth.Client
	lookup  *lookup.Service
	log     *slog.Logger
}

// homeRecentLimit caps how many books the home page's "recently added"
// section shows.
const homeRecentLimit = 4

type homeData struct {
	Books         []*book.Book
	Authenticated bool
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

func newHandlers(render *web.Renderer, books book.Repository, libraryRepo library.Repository, users user.Repository, authClient *auth.Client, lookupSvc *lookup.Service, log *slog.Logger) *handlers {
	return &handlers{
		render:  render,
		books:   books,
		library: libraryRepo,
		users:   users,
		auth:    authClient,
		lookup:  lookupSvc,
		log:     log,
	}
}

// --- full pages ------------------------------------------------------

// home renders the public landing page. When the visitor is logged in it
// also shows their own most-recently-added books; logged-out visitors see a
// generic landing view with no personal data (per-user library ownership
// means there's nothing to show for an unauthenticated request).
func (h *handlers) home(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.render.Page(w, r, "home", homeData{})
		return
	}

	books, err := h.userBooks(r, claims.UserID())
	if err != nil {
		h.render.Page(w, r, "home", homeData{Authenticated: true})
		return
	}
	if len(books) > homeRecentLimit {
		books = books[:homeRecentLimit]
	}
	h.render.Page(w, r, "home", homeData{Books: books, Authenticated: true})
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

// bookDetail and bookDelete take a library entry id (not a books.id — the
// books table is shared across users, so addressing "this user's copy" for
// display/deletion has to go through their library.Entry instead).
func (h *handlers) bookDetail(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.UserFromContext(r.Context())
	id := r.PathValue("id")

	b, err := h.ownedBookView(r, claims.UserID(), id)
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

// loginSubmit exchanges email/password for a Supabase session via
// auth.Client, sets the session cookies, and refreshes this user's local
// profile mirror (internal/user) before redirecting to /library. Supabase's
// own error message is shown back to the user on failure (bad credentials,
// unconfirmed email, etc.) rather than a generic one, since it's already
// meant to be user-facing.
func (h *handlers) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Page(w, r, "login", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	session, err := h.auth.SignInWithPassword(r.Context(), email, password)
	if err != nil {
		h.log.Info("login failed", "email", email, "error", err)
		h.render.Page(w, r, "login", errorMessageData{Message: "Feil e-post eller passord."})
		return
	}

	if err := h.syncUserProfile(r, session.User); err != nil {
		h.log.Error("failed to sync user profile after login", "error", err)
	}

	auth.SetSession(w, session.Tokens)
	http.Redirect(w, r, "/library", http.StatusSeeOther)
}

// registerSubmit signs up a new Supabase user. If the project requires
// email confirmation, Supabase issues no session yet — the user is told to
// check their email instead of being logged in immediately.
func (h *handlers) registerSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Page(w, r, "register", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	if password != passwordConfirm {
		h.render.Page(w, r, "register", errorMessageData{Message: "Passordene er ikke like."})
		return
	}

	session, err := h.auth.SignUp(r.Context(), email, password, name)
	if err != nil {
		h.log.Info("registration failed", "email", email, "error", err)
		h.render.Page(w, r, "register", errorMessageData{Message: "Kunne ikke registrere kontoen. Prøv igjen."})
		return
	}

	if session.AccessToken == "" {
		h.render.Page(w, r, "login", errorMessageData{Message: "Sjekk e-posten din for å bekrefte kontoen før du logger inn."})
		return
	}

	if err := h.syncUserProfile(r, session.User); err != nil {
		h.log.Error("failed to sync user profile after registration", "error", err)
	}

	auth.SetSession(w, session.Tokens)
	http.Redirect(w, r, "/library", http.StatusSeeOther)
}

// logoutSubmit clears the session cookies and best-effort revokes the
// session server-side. Revocation failure doesn't block logging the
// browser out locally.
func (h *handlers) logoutSubmit(w http.ResponseWriter, r *http.Request) {
	tokens := auth.ReadTokens(r)
	if tokens.AccessToken != "" {
		if err := h.auth.SignOut(r.Context(), tokens.AccessToken); err != nil {
			h.log.Info("sign out call to Supabase failed", "error", err)
		}
	}
	auth.ClearSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// syncUserProfile upserts the local profile mirror (internal/user) for a
// just-authenticated Supabase user, keyed by Supabase's own user id — see
// the user package's doc comment for why this exists.
func (h *handlers) syncUserProfile(r *http.Request, su auth.User) error {
	return h.users.Upsert(r.Context(), &user.User{
		ID:          su.ID,
		Email:       su.Email,
		DisplayName: su.DisplayName(),
	})
}

// --- HTMX partial endpoints -------------------------------------------

// scanSubmit backs both the camera scanner (via htmx.ajax) and the manual
// ISBN fallback form. It resolves the ISBN via the cache→Google
// Books→Open Library→Nasjonalbiblioteket chain (internal/lookup). By
// default it does NOT persist the result — it renders a scan-preview
// fragment with an explicit "legg til i biblioteket" button (see
// confirmSubmit) so scanning a barcode never silently adds a book. If the
// "auto" form value is "true" (the /scan page's "Legg til automatisk"
// toggle, for scanning many books back-to-back without confirming each
// one), it saves immediately instead and renders scan-result. Falls back
// to manual-entry-form when nothing resolves the ISBN, or error-message on
// invalid input.
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

	if r.FormValue("auto") == "true" {
		saved, err := h.saveBook(r, b)
		if err != nil {
			h.render.Partial(w, "error-message", errorMessageData{
				Message: "Fant boken, men kunne ikke lagre den. Prøv igjen.",
			})
			return
		}
		h.render.Partial(w, "scan-result", scanResultData{Book: saved})
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

// saveBook persists a resolved-but-not-yet-saved book and links it into the
// current user's library. If the ISBN already exists (another user
// scanned/entered it before — the books table is shared between users), it
// reuses the existing row instead of creating a duplicate, per the
// project's duplicate-ISBN rule. If this user already has that book in
// their library (double-scan/double-submit), it returns their existing
// entry instead of erroring. The returned *book.Book is a display copy
// whose ID/CreatedAt are the library entry's — see ownedBookView's doc
// comment for why.
func (h *handlers) saveBook(r *http.Request, b *book.Book) (*book.Book, error) {
	claims, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil, errors.New("saveBook: no authenticated user in context")
	}

	if err := h.books.Create(r.Context(), b); err != nil {
		if !errors.Is(err, book.ErrDuplicateISBN) {
			return nil, err
		}
		existing, err := h.books.GetByISBN(r.Context(), b.ISBN)
		if err != nil {
			return nil, err
		}
		b = existing
	}

	entry := &library.Entry{UserID: claims.UserID(), BookID: b.ID}
	if err := h.library.Create(r.Context(), entry); err != nil {
		if !errors.Is(err, library.ErrDuplicateEntry) {
			return nil, err
		}
		existing, err := h.library.GetByUserAndBook(r.Context(), claims.UserID(), b.ID)
		if err != nil {
			return nil, err
		}
		entry = existing
	}

	return entryBookView(b, entry), nil
}

// entryBookView returns a display copy of b whose ID/CreatedAt are entry's,
// so that book-card/book-detail templates' "/books/{{.ID}}" links and
// "Lagt til" date address this user's library entry rather than the
// (shared, cross-user) book row.
func entryBookView(b *book.Book, e *library.Entry) *book.Book {
	view := *b
	view.ID = e.ID
	view.CreatedAt = e.AddedAt
	return &view
}

// ownedBookView resolves a library entry id into its display copy (see
// entryBookView), 404ing via library.ErrNotFound if the entry doesn't exist
// or doesn't belong to userID.
func (h *handlers) ownedBookView(r *http.Request, userID, entryID string) (*book.Book, error) {
	entry, err := h.library.GetByID(r.Context(), entryID)
	if err != nil {
		return nil, err
	}
	if entry.UserID != userID {
		return nil, library.ErrNotFound
	}
	b, err := h.books.GetByID(r.Context(), entry.BookID)
	if err != nil {
		return nil, err
	}
	return entryBookView(b, entry), nil
}

// userBooks lists userID's library in newest-added-first order (see
// library.Repository.ListByUser), resolved into display copies (see
// entryBookView).
func (h *handlers) userBooks(r *http.Request, userID string) ([]*book.Book, error) {
	entries, err := h.library.ListByUser(r.Context(), userID)
	if err != nil {
		return nil, err
	}

	out := make([]*book.Book, 0, len(entries))
	for _, e := range entries {
		b, err := h.books.GetByID(r.Context(), e.BookID)
		if err != nil {
			h.log.Error("failed to resolve library entry's book", "entry_id", e.ID, "book_id", e.BookID, "error", err)
			continue
		}
		out = append(out, entryBookView(b, e))
	}
	return out, nil
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
// identically — search here is always local (Postgres or the in-memory
// fallback, via h.userBooks), never an external call, per CLAUDE.md's rule
// that only the scan endpoint talks to external services.
func (h *handlers) filterBooks(r *http.Request, query, field string) ([]*book.Book, error) {
	claims, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil, errors.New("filterBooks: no authenticated user in context")
	}

	all, err := h.userBooks(r, claims.UserID())
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

// bookDelete backs DELETE /books/{id}, where id is a library entry id (see
// ownedBookView's doc comment). It's a plain REST action (no HTML fragment
// to render): 204 on success, 404 if the entry doesn't exist or doesn't
// belong to the caller, 500 on any other repository error. If the request
// came from htmx (i.e. a delete button on the book detail page), an
// HX-Redirect back to /library is set so the client navigates away from the
// now-deleted book's page.
func (h *handlers) bookDelete(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.UserFromContext(r.Context())
	id := r.PathValue("id")

	entry, err := h.library.GetByID(r.Context(), id)
	if err != nil || entry.UserID != claims.UserID() {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if err := h.library.Delete(r.Context(), entry.ID); err != nil {
		if errors.Is(err, library.ErrNotFound) {
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
