package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

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
	Books []*book.Book
	// Name is the visitor's display name, set only when they actually chose
	// one at signup — user.User.DisplayName falls back to their email when
	// they didn't, and greeting someone by their own email isn't a greeting.
	Name string
}

type searchResultsData struct {
	// Books is the current page of matches (see libraryPageSize).
	Books []*book.Book
	Query string
	Field string
	Sort  string
	// Total is the library's full, unfiltered size — shown alongside the
	// (possibly filtered) match count so "how many books do I have" always
	// reads correctly even while a search query narrows what's displayed.
	Total int
	// Matches is how many books matched the query/field filter across all
	// pages (== Total when nothing is filtered).
	Matches int
	// NextURL is the /books/search URL for the following page, or "" on the
	// last page. The book-cards partial renders an hx-trigger="revealed"
	// sentinel from it for infinite scroll.
	NextURL string
}

type scanResultData struct {
	Book *book.Book
	// Duplicate is true when the user already had this book, so the
	// template says so instead of claiming it was just added.
	Duplicate bool
}

// entryDetailsData backs the entry-details partial: the per-user fields
// (reading status, notes, location) of one library entry.
type entryDetailsData struct {
	ID       string
	Status   string
	Notes    string
	Location string
	Saved    bool
	Error    string
}

type scanPreviewData struct {
	Book *book.Book
}

type bookDetailData struct {
	Book    *book.Book
	Entry   *entryDetailsData
	Message string
}

// PageTitle gives the layout a per-page <title> (see web.Renderer).
func (d bookDetailData) PageTitle() string {
	if d.Book != nil {
		return d.Book.Title
	}
	return "Bok ikke funnet"
}

type importResultData struct {
	Added    int
	Existing int
	Skipped  int
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

	name := h.displayName(r, claims)

	books, err := h.userBooks(r, claims.UserID())
	if err != nil {
		h.render.Page(w, r, "home", homeData{Name: name})
		return
	}
	if len(books) > homeRecentLimit {
		books = books[:homeRecentLimit]
	}
	h.render.Page(w, r, "home", homeData{Books: books, Name: name})
}

// displayName looks up the visitor's chosen name from the local profile
// mirror, returning "" both on lookup failure and when they never set one
// (see homeData.Name's doc comment for why the email fallback is excluded).
func (h *handlers) displayName(r *http.Request, claims auth.Claims) string {
	u, err := h.users.GetByID(r.Context(), claims.UserID())
	if err != nil {
		return ""
	}
	if u.DisplayName == u.Email {
		return ""
	}
	return u.DisplayName
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
	q := parseLibraryQuery(r)
	data, err := h.searchData(r, q)
	if err != nil {
		h.render.Page(w, r, "library", searchResultsData{Field: q.Field, Sort: q.Sort})
		return
	}
	h.render.Page(w, r, "library", data)
}

// bookDetail and bookDelete take a library entry id (not a books.id — the
// books table is shared across users, so addressing "this user's copy" for
// display/deletion has to go through their library.Entry instead).
func (h *handlers) bookDetail(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.UserFromContext(r.Context())
	id := r.PathValue("id")

	row, err := h.ownedRow(r, claims.UserID(), id)
	if err != nil {
		h.render.PageWithStatus(w, r, "book_detail", http.StatusNotFound, bookDetailData{Message: "Boken ble ikke funnet."})
		return
	}
	h.render.Page(w, r, "book_detail", bookDetailData{Book: row.view(), Entry: newEntryDetails(row.Entry)})
}

// newEntryDetails maps a library entry to its editable-fields view model.
func newEntryDetails(e *library.Entry) *entryDetailsData {
	status := e.Status
	if !library.ValidStatus(status) {
		status = library.StatusUnread
	}
	return &entryDetailsData{ID: e.ID, Status: status, Notes: e.Notes, Location: e.Location}
}

// Limits on the free-text entry fields, in runes.
const (
	maxNotesLen    = 2000
	maxLocationLen = 200
)

// bookUpdate backs PATCH /books/{id} (id is a library entry id): it saves
// the reading status, notes, and location the user edited on the detail
// page and re-renders the entry-details form with a confirmation.
func (h *handlers) bookUpdate(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.UserFromContext(r.Context())

	entry, err := h.library.GetByID(r.Context(), r.PathValue("id"))
	if err != nil || entry.UserID != claims.UserID() {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	data := newEntryDetails(entry)
	if err := r.ParseForm(); err != nil {
		data.Error = "Ugyldig forespørsel."
		h.render.Partial(w, "entry-details", data)
		return
	}

	status := r.FormValue("status")
	notes := strings.TrimSpace(r.FormValue("notes"))
	location := strings.TrimSpace(r.FormValue("location"))
	data.Status, data.Notes, data.Location = status, notes, location

	switch {
	case !library.ValidStatus(status):
		data.Status = entry.Status
		data.Error = "Ugyldig lesestatus."
	case utf8.RuneCountInString(notes) > maxNotesLen:
		data.Error = "Notatet er for langt (maks 2000 tegn)."
	case utf8.RuneCountInString(location) > maxLocationLen:
		data.Error = "Plasseringen er for lang (maks 200 tegn)."
	}
	if data.Error != "" {
		h.render.Partial(w, "entry-details", data)
		return
	}

	entry.Status, entry.Notes, entry.Location = status, notes, location
	if err := h.library.Update(r.Context(), entry); err != nil {
		h.log.Error("failed to update library entry", "entry_id", entry.ID, "error", err)
		data.Error = "Kunne ikke lagre endringene. Prøv igjen."
		h.render.Partial(w, "entry-details", data)
		return
	}

	data.Saved = true
	h.render.Partial(w, "entry-details", data)
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

	// Already in this user's library: say so right away, without spending
	// a lookup (cache or external APIs) on a book we already have.
	if owned, ok := h.ownedByISBN(r, isbn); ok {
		h.render.Partial(w, "scan-result", scanResultData{Book: owned, Duplicate: true})
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
		saved, existed, err := h.saveBook(r, b, nil)
		if err != nil {
			h.render.Partial(w, "error-message", errorMessageData{
				Message: "Fant boken, men kunne ikke lagre den. Prøv igjen.",
			})
			return
		}
		h.render.Partial(w, "scan-result", scanResultData{Book: saved, Duplicate: existed})
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

	saved, existed, err := h.saveBook(r, b, nil)
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Fant boken, men kunne ikke lagre den. Prøv igjen.",
		})
		return
	}

	h.render.Partial(w, "scan-result", scanResultData{Book: saved, Duplicate: existed})
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
	saved, existed, err := h.saveBook(r, b, nil)
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Kunne ikke lagre boken. Prøv igjen."})
		return
	}

	h.render.Partial(w, "scan-result", scanResultData{Book: saved, Duplicate: existed})
}

// libraryRow pairs a library entry with the (shared) book it points at.
type libraryRow struct {
	Book  *book.Book
	Entry *library.Entry
}

// view returns the display copy of the book whose ID/CreatedAt are the
// entry's — see entryBookView for why.
func (row libraryRow) view() *book.Book {
	return entryBookView(row.Book, row.Entry)
}

// saveBook persists a resolved-but-not-yet-saved book and links it into the
// current user's library. If the ISBN already exists (another user
// scanned/entered it before — the books table is shared between users), it
// reuses the existing row instead of creating a duplicate, per the
// project's duplicate-ISBN rule. If this user already has that book in
// their library (double-scan/double-submit), it returns their existing
// entry instead of erroring, and existed is true so callers can tell the
// user they already had it. details, if non-nil, supplies the status, notes
// and location for a newly created entry (used by CSV import); it is
// ignored when the entry already existed. The returned *book.Book is a
// display copy whose ID/CreatedAt are the library entry's — see
// entryBookView's doc comment for why.
func (h *handlers) saveBook(r *http.Request, b *book.Book, details *library.Entry) (view *book.Book, existed bool, err error) {
	claims, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil, false, errors.New("saveBook: no authenticated user in context")
	}

	if err := h.books.Create(r.Context(), b); err != nil {
		if !errors.Is(err, book.ErrDuplicateISBN) {
			return nil, false, err
		}
		existing, err := h.books.GetByISBN(r.Context(), b.ISBN)
		if err != nil {
			return nil, false, err
		}
		b = existing
	}

	entry := &library.Entry{UserID: claims.UserID(), BookID: b.ID}
	if details != nil {
		entry.Status = details.Status
		entry.Notes = details.Notes
		entry.Location = details.Location
	}
	if err := h.library.Create(r.Context(), entry); err != nil {
		if !errors.Is(err, library.ErrDuplicateEntry) {
			return nil, false, err
		}
		existing, err := h.library.GetByUserAndBook(r.Context(), claims.UserID(), b.ID)
		if err != nil {
			return nil, false, err
		}
		entry = existing
		existed = true
	}

	return entryBookView(b, entry), existed, nil
}

// ownedByISBN reports whether the current user already has the book with
// this ISBN in their library, returning its display copy if so. Any lookup
// failure counts as "not owned" — the caller then simply proceeds with a
// normal lookup/save, which handles duplicates on its own.
func (h *handlers) ownedByISBN(r *http.Request, isbn string) (*book.Book, bool) {
	claims, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil, false
	}
	b, err := h.books.GetByISBN(r.Context(), isbn)
	if err != nil {
		return nil, false
	}
	entry, err := h.library.GetByUserAndBook(r.Context(), claims.UserID(), b.ID)
	if err != nil {
		return nil, false
	}
	return entryBookView(b, entry), true
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

// ownedRow resolves a library entry id into its entry + book, 404ing via
// library.ErrNotFound if the entry doesn't exist or doesn't belong to
// userID.
func (h *handlers) ownedRow(r *http.Request, userID, entryID string) (libraryRow, error) {
	entry, err := h.library.GetByID(r.Context(), entryID)
	if err != nil {
		return libraryRow{}, err
	}
	if entry.UserID != userID {
		return libraryRow{}, library.ErrNotFound
	}
	b, err := h.books.GetByID(r.Context(), entry.BookID)
	if err != nil {
		return libraryRow{}, err
	}
	return libraryRow{Book: b, Entry: entry}, nil
}

// userRows lists userID's library in newest-added-first order (see
// library.Repository.ListByUser), with each entry's book resolved.
func (h *handlers) userRows(r *http.Request, userID string) ([]libraryRow, error) {
	entries, err := h.library.ListByUser(r.Context(), userID)
	if err != nil {
		return nil, err
	}

	out := make([]libraryRow, 0, len(entries))
	for _, e := range entries {
		b, err := h.books.GetByID(r.Context(), e.BookID)
		if err != nil {
			h.log.Error("failed to resolve library entry's book", "entry_id", e.ID, "book_id", e.BookID, "error", err)
			continue
		}
		out = append(out, libraryRow{Book: b, Entry: e})
	}
	return out, nil
}

// userBooks is userRows reduced to display copies (see entryBookView).
func (h *handlers) userBooks(r *http.Request, userID string) ([]*book.Book, error) {
	rows, err := h.userRows(r, userID)
	if err != nil {
		return nil, err
	}
	out := make([]*book.Book, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.view())
	}
	return out, nil
}

// defaultSearchField is used when the "field" query param is missing or
// unrecognized. It shows every book that matched the search query — see
// passesFieldFilter.
const defaultSearchField = "all"

// Library sort orders ("sort" query param). sortNewest is the repository's
// natural order, so it needs no re-sorting.
const (
	sortNewest = "new"
	sortOldest = "old"
	sortTitle  = "title"
	sortAuthor = "author"

	defaultSort = sortNewest
)

// libraryPageSize is how many books one page of the library grid holds;
// further pages are fetched by htmx as the user scrolls (see the
// book-cards partial).
const libraryPageSize = 24

// libraryQuery is the parsed, normalized "q"/"field"/"sort"/"page" query
// of /library, /books and /books/search.
type libraryQuery struct {
	Q     string
	Field string
	Sort  string
	Page  int
}

func parseLibraryQuery(r *http.Request) libraryQuery {
	v := r.URL.Query()
	q := libraryQuery{
		Q:     strings.TrimSpace(v.Get("q")),
		Field: v.Get("field"),
		Sort:  v.Get("sort"),
		Page:  1,
	}
	if q.Field == "" {
		q.Field = defaultSearchField
	}
	switch q.Sort {
	case sortNewest, sortOldest, sortTitle, sortAuthor:
	default:
		q.Sort = defaultSort
	}
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n > 1 {
		q.Page = min(n, 10000)
	}
	return q
}

// nextURL returns the /books/search URL for the page after q.Page.
func (q libraryQuery) nextURL() string {
	v := url.Values{}
	v.Set("q", q.Q)
	v.Set("field", q.Field)
	v.Set("sort", q.Sort)
	v.Set("page", strconv.Itoa(q.Page+1))
	return "/books/search?" + v.Encode()
}

// librarySearch backs the search-as-you-type input on the library page. "q"
// fuzzily matches across title, author, and publisher together; the
// "field" select is unrelated to that match — it's a separate, purely
// cosmetic display filter, see filterBooks. Page 1 renders the stats line
// plus the first grid page; later pages (infinite scroll) render only the
// next batch of cards.
func (h *handlers) librarySearch(w http.ResponseWriter, r *http.Request) {
	q := parseLibraryQuery(r)

	data, err := h.searchData(r, q)
	if err != nil {
		h.render.Partial(w, "search-results", searchResultsData{Query: q.Q, Field: q.Field, Sort: q.Sort})
		return
	}

	if q.Page > 1 {
		h.render.Partial(w, "book-cards", data)
		return
	}
	h.render.Partial(w, "search-results", data)
}

// searchData filters, sorts and paginates the caller's library per q.
func (h *handlers) searchData(r *http.Request, q libraryQuery) (searchResultsData, error) {
	matches, total, err := h.filterBooks(r, q.Q, q.Field)
	if err != nil {
		return searchResultsData{}, err
	}
	sortBooks(matches, q.Sort)

	data := searchResultsData{
		Query:   q.Q,
		Field:   q.Field,
		Sort:    q.Sort,
		Total:   total,
		Matches: len(matches),
	}

	start := (q.Page - 1) * libraryPageSize
	if start < len(matches) {
		end := min(start+libraryPageSize, len(matches))
		data.Books = matches[start:end]
		if end < len(matches) {
			data.NextURL = q.nextURL()
		}
	}
	return data, nil
}

// sortBooks orders books in place. The input is newest-added-first
// (library.Repository.ListByUser), and the sort is stable, so ties keep
// that deterministic order.
func sortBooks(books []*book.Book, order string) {
	switch order {
	case sortOldest:
		slices.Reverse(books)
	case sortTitle:
		slices.SortStableFunc(books, func(a, b *book.Book) int {
			return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		})
	case sortAuthor:
		slices.SortStableFunc(books, func(a, b *book.Book) int {
			return strings.Compare(strings.ToLower(a.Author), strings.ToLower(b.Author))
		})
	}
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
// that only the scan endpoint talks to external services. The returned total
// is the caller's full, unfiltered library size, always ignoring "q"/"field"
// so the "N bøker i hyllen din" stat stays accurate even mid-search.
func (h *handlers) filterBooks(r *http.Request, query, field string) ([]*book.Book, int, error) {
	claims, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil, 0, errors.New("filterBooks: no authenticated user in context")
	}

	all, err := h.userBooks(r, claims.UserID())
	if err != nil {
		return nil, 0, err
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
	return matches, len(all), nil
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
// entryBookView's doc comment). It's a plain REST action (no HTML fragment
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
