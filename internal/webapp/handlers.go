package webapp

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/larskristianhaga/hyllis.no/internal/api"
	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
	"github.com/larskristianhaga/hyllis.no/internal/web"
)

// LibraryAPI is the subset of the hyllis JSON API the web app uses.
// *api.Client implements it. The web app has no database, cache or lookup
// access of its own — everything about books and libraries goes through
// here, authenticated as the logged-in user.
type LibraryAPI interface {
	Me(ctx context.Context, token string) (*api.Profile, error)
	SyncProfile(ctx context.Context, token string, u api.ProfileUpdate) error
	Lookup(ctx context.Context, token, isbn string) (*book.Book, error)
	List(ctx context.Context, token string, opts api.ListOptions) (*api.LibraryPage, error)
	Get(ctx context.Context, token, id string) (*api.Item, error)
	GetByISBN(ctx context.Context, token, isbn string) (*api.Item, error)
	Add(ctx context.Context, token string, req api.AddRequest) (*api.AddResult, error)
	Import(ctx context.Context, token string, req api.ImportRequest) (*api.ImportResult, error)
	Update(ctx context.Context, token, id string, u api.EntryUpdate) (*api.Item, error)
	Delete(ctx context.Context, token, id string) error
}

// handlers wires the HTML routes to the template renderer, the hyllis API
// and the Supabase Auth client.
type handlers struct {
	render *web.Renderer
	api    LibraryAPI
	auth   *auth.Client
	log    *slog.Logger
}

// homeRecentLimit caps how many books the home page's "recently added"
// section shows.
const homeRecentLimit = 4

type homeData struct {
	Books []*book.Book
	// Name is the visitor's display name, set only when they actually chose
	// one at signup — the profile's display name falls back to their email
	// when they didn't, and greeting someone by their own email isn't a
	// greeting.
	Name string
}

type searchResultsData struct {
	// Books is the current page of matches.
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

func newHandlers(render *web.Renderer, libraryAPI LibraryAPI, authClient *auth.Client, log *slog.Logger) *handlers {
	return &handlers{render: render, api: libraryAPI, auth: authClient, log: log}
}

// token is the logged-in user's access token, forwarded to the API.
func token(r *http.Request) string {
	return auth.AccessTokenFromContext(r.Context())
}

// itemView returns a display copy of the item's book whose ID/CreatedAt are
// the library entry's, so book-card/book-detail templates' "/books/{{.ID}}"
// links and "Lagt til" date address this user's library entry rather than
// the (shared, cross-user) book row.
func itemView(it *api.Item) *book.Book {
	b := it.Book
	b.ID = it.ID
	b.CreatedAt = it.AddedAt
	return &b
}

func itemViews(items []api.Item) []*book.Book {
	out := make([]*book.Book, 0, len(items))
	for i := range items {
		out = append(out, itemView(&items[i]))
	}
	return out
}

// --- full pages ------------------------------------------------------

// home renders the public landing page. When the visitor is logged in it
// also shows their own most-recently-added books; logged-out visitors see a
// generic landing view with no personal data.
func (h *handlers) home(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserFromContext(r.Context()); !ok {
		h.render.Page(w, r, "home", homeData{})
		return
	}

	data := homeData{Name: h.displayName(r)}
	page, err := h.api.List(r.Context(), token(r), api.ListOptions{PerPage: homeRecentLimit})
	if err != nil {
		h.log.Error("home: list library failed", "error", err)
	} else {
		data.Books = itemViews(page.Items)
	}
	h.render.Page(w, r, "home", data)
}

// displayName returns the visitor's chosen name, or "" both on lookup
// failure and when they never set one (see homeData.Name).
func (h *handlers) displayName(r *http.Request) string {
	p, err := h.api.Me(r.Context(), token(r))
	if err != nil || p.DisplayName == p.Email {
		return ""
	}
	return p.DisplayName
}

func (h *handlers) scanPage(w http.ResponseWriter, r *http.Request) {
	h.render.Page(w, r, "scan", nil)
}

// libraryPage backs both GET /library and its alias GET /books. Optional
// "q"/"field"/"sort" query params filter the initial page load the same
// way /books/search does.
func (h *handlers) libraryPage(w http.ResponseWriter, r *http.Request) {
	q := parseLibraryQuery(r)
	data, err := h.searchData(r, q)
	if err != nil {
		h.log.Error("library: list failed", "error", err)
		h.render.Page(w, r, "library", searchResultsData{Field: q.Field, Sort: q.Sort})
		return
	}
	h.render.Page(w, r, "library", data)
}

// bookDetail takes a library entry id (not a books.id — the books table is
// shared across users, so "this user's copy" goes through their entry).
func (h *handlers) bookDetail(w http.ResponseWriter, r *http.Request) {
	it, err := h.api.Get(r.Context(), token(r), r.PathValue("id"))
	if err != nil {
		if !api.IsCode(err, api.CodeNotFound) {
			h.log.Error("book detail: get failed", "error", err)
		}
		h.render.PageWithStatus(w, r, "book_detail", http.StatusNotFound, bookDetailData{Message: "Boken ble ikke funnet."})
		return
	}
	h.render.Page(w, r, "book_detail", bookDetailData{Book: itemView(it), Entry: newEntryDetails(it)})
}

func newEntryDetails(it *api.Item) *entryDetailsData {
	status := it.Status
	if !library.ValidStatus(status) {
		status = library.StatusUnread
	}
	return &entryDetailsData{ID: it.ID, Status: status, Notes: it.Notes, Location: it.Location}
}

// entryUpdateErrors maps API validation codes to the messages shown on the
// entry-details form.
var entryUpdateErrors = map[string]string{
	api.CodeInvalidStatus:   "Ugyldig lesestatus.",
	api.CodeNotesTooLong:    "Notatet er for langt (maks 2000 tegn).",
	api.CodeLocationTooLong: "Plasseringen er for lang (maks 200 tegn).",
}

// bookUpdate backs PATCH /books/{id} (id is a library entry id): it saves
// the reading status, notes, and location the user edited on the detail
// page and re-renders the entry-details form with a confirmation.
func (h *handlers) bookUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "entry-details", &entryDetailsData{ID: id, Error: "Ugyldig forespørsel."})
		return
	}

	u := api.EntryUpdate{
		Status:   r.FormValue("status"),
		Notes:    strings.TrimSpace(r.FormValue("notes")),
		Location: strings.TrimSpace(r.FormValue("location")),
	}
	data := &entryDetailsData{ID: id, Status: u.Status, Notes: u.Notes, Location: u.Location}

	it, err := h.api.Update(r.Context(), token(r), id, u)
	if err != nil {
		apiErr, _ := err.(*api.Error)
		switch {
		case apiErr != nil && apiErr.Code == api.CodeNotFound:
			w.WriteHeader(http.StatusNotFound)
			return
		case apiErr != nil && entryUpdateErrors[apiErr.Code] != "":
			data.Error = entryUpdateErrors[apiErr.Code]
			if apiErr.Code == api.CodeInvalidStatus {
				data.Status = library.StatusUnread
				if cur, err := h.api.Get(r.Context(), token(r), id); err == nil {
					data.Status = newEntryDetails(cur).Status
				}
			}
		default:
			h.log.Error("failed to update library entry", "entry_id", id, "error", err)
			data.Error = "Kunne ikke lagre endringene. Prøv igjen."
		}
		h.render.Partial(w, "entry-details", data)
		return
	}

	data = newEntryDetails(it)
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
// auth.Client, sets the session cookies, and asks the API to refresh this
// user's local profile mirror before redirecting to /library.
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

	h.syncUserProfile(r, session)
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

	h.syncUserProfile(r, session)
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

// syncUserProfile asks the API to upsert the local profile mirror for a
// just-authenticated Supabase user (the API takes id/email from the token
// itself). Failure is logged, not fatal: the user can still log in.
func (h *handlers) syncUserProfile(r *http.Request, s auth.Session) {
	name := s.User.DisplayName()
	if err := h.api.SyncProfile(r.Context(), s.AccessToken, api.ProfileUpdate{DisplayName: name}); err != nil {
		h.log.Error("failed to sync user profile", "error", err)
	}
}

// --- HTMX partial endpoints -------------------------------------------

// scanSubmit backs both the camera scanner (via htmx.ajax) and the manual
// ISBN fallback form. It resolves the ISBN via the API's lookup endpoint
// (cache → external providers). By default it does NOT persist the result
// — it renders a scan-preview fragment with an explicit "legg til i
// biblioteket" button (see confirmSubmit). If the "auto" form value is
// "true" (the /scan page's "Legg til automatisk" toggle), it saves
// immediately instead and renders scan-result. Falls back to
// manual-entry-form when nothing resolves the ISBN, or error-message on
// invalid input.
func (h *handlers) scanSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	isbn := strings.TrimSpace(r.FormValue("isbn"))

	switch r.FormValue("source") {
	case "camera":
		h.log.Info("scan: isbn detected by camera", "isbn", isbn)
	default:
		h.log.Info("scan: isbn typed manually and submitted", "isbn", isbn)
	}

	if !api.ValidEAN13(isbn) {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Ugyldig ISBN/EAN-13-kode. Koden må bestå av 13 siffer.",
		})
		return
	}

	// Already in this user's library: say so right away, without spending
	// a lookup (cache or external APIs) on a book we already have. Any
	// error counts as "not owned" — the save path handles duplicates too.
	if owned, err := h.api.GetByISBN(r.Context(), token(r), isbn); err == nil {
		h.render.Partial(w, "scan-result", scanResultData{Book: itemView(owned), Duplicate: true})
		return
	}

	b, err := h.api.Lookup(r.Context(), token(r), isbn)
	if api.IsCode(err, api.CodeNotFound) {
		h.render.Partial(w, "manual-entry-form", manualEntryFormData{ISBN: isbn})
		return
	}
	if err != nil {
		h.log.Error("scan: lookup failed", "isbn", isbn, "error", err)
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Noe gikk feil under oppslag av ISBN " + isbn + ". Prøv igjen.",
		})
		return
	}

	if r.FormValue("auto") == "true" {
		h.addAndRender(w, r, api.AddRequest{Book: *b}, "Fant boken, men kunne ikke lagre den. Prøv igjen.")
		return
	}

	h.render.Partial(w, "scan-preview", scanPreviewData{Book: b})
}

// confirmSubmit backs the "Legg til i biblioteket" button shown on the
// scan-preview fragment. It re-reads the resolved book's fields from the
// form (round-tripped as hidden inputs, since the book was never persisted
// after scanSubmit's lookup) and only now adds it.
func (h *handlers) confirmSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	isbn := strings.TrimSpace(r.FormValue("isbn"))
	title := strings.TrimSpace(r.FormValue("title"))
	author := strings.TrimSpace(r.FormValue("author"))

	if !api.ValidEAN13(isbn) || title == "" || author == "" {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig bokdata. Prøv å skanne igjen."})
		return
	}

	source := r.FormValue("source")
	if source == "" {
		source = "manual"
	}

	year, _ := strconv.Atoi(r.FormValue("year"))
	pages, _ := strconv.Atoi(r.FormValue("pages"))

	b := book.Book{
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
	h.addAndRender(w, r, api.AddRequest{Book: b}, "Fant boken, men kunne ikke lagre den. Prøv igjen.")
}

// manualSubmit backs the manual-entry-form shown when every lookup source
// missed. The book is persisted with kilde "manual".
func (h *handlers) manualSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Ugyldig forespørsel."})
		return
	}

	isbn := strings.TrimSpace(r.FormValue("isbn"))
	title := strings.TrimSpace(r.FormValue("title"))
	author := strings.TrimSpace(r.FormValue("author"))
	publisher := strings.TrimSpace(r.FormValue("publisher"))

	if !api.ValidEAN13(isbn) || title == "" || author == "" {
		h.render.Partial(w, "error-message", errorMessageData{
			Message: "Utfyll ISBN, tittel og forfatter for å legge til boken manuelt.",
		})
		return
	}

	b := book.Book{ISBN: isbn, Title: title, Author: author, Publisher: publisher, Source: "manual"}
	h.addAndRender(w, r, api.AddRequest{Book: b}, "Kunne ikke lagre boken. Prøv igjen.")
}

// addAndRender adds req via the API and renders scan-result (or failMsg).
func (h *handlers) addAndRender(w http.ResponseWriter, r *http.Request, req api.AddRequest, failMsg string) {
	res, err := h.api.Add(r.Context(), token(r), req)
	if err != nil {
		h.log.Error("add book failed", "isbn", req.Book.ISBN, "error", err)
		h.render.Partial(w, "error-message", errorMessageData{Message: failMsg})
		return
	}
	h.render.Partial(w, "scan-result", scanResultData{Book: itemView(&res.Item), Duplicate: res.Existed})
}

// defaultSearchField is used when the "field" query param is missing or
// unrecognized. It shows every book that matched the search query.
const defaultSearchField = api.FieldAll

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
	case api.SortNewest, api.SortOldest, api.SortTitle, api.SortAuthor:
	default:
		q.Sort = api.SortNewest
	}
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n > 1 {
		q.Page = min(n, 10000)
	}
	return q
}

// pageURL returns the /books/search URL for page n of q.
func (q libraryQuery) pageURL(n int) string {
	v := url.Values{}
	v.Set("q", q.Q)
	v.Set("field", q.Field)
	v.Set("sort", q.Sort)
	v.Set("page", strconv.Itoa(n))
	return "/books/search?" + v.Encode()
}

// librarySearch backs the search-as-you-type input on the library page.
// Page 1 renders the stats line plus the first grid page; later pages
// (infinite scroll) render only the next batch of cards.
func (h *handlers) librarySearch(w http.ResponseWriter, r *http.Request) {
	q := parseLibraryQuery(r)

	data, err := h.searchData(r, q)
	if err != nil {
		h.log.Error("library search failed", "error", err)
		h.render.Partial(w, "search-results", searchResultsData{Query: q.Q, Field: q.Field, Sort: q.Sort})
		return
	}

	if q.Page > 1 {
		h.render.Partial(w, "book-cards", data)
		return
	}
	h.render.Partial(w, "search-results", data)
}

// searchData fetches one page of the caller's library from the API —
// search is local to the API's own database, never an external lookup.
func (h *handlers) searchData(r *http.Request, q libraryQuery) (searchResultsData, error) {
	page, err := h.api.List(r.Context(), token(r), api.ListOptions{
		Q: q.Q, Field: q.Field, Sort: q.Sort, Page: q.Page,
	})
	if err != nil {
		return searchResultsData{}, err
	}
	data := searchResultsData{
		Books:   itemViews(page.Items),
		Query:   q.Q,
		Field:   q.Field,
		Sort:    q.Sort,
		Total:   page.Total,
		Matches: page.Matches,
	}
	if page.NextPage > 0 {
		data.NextURL = q.pageURL(page.NextPage)
	}
	return data, nil
}

// bookDelete backs DELETE /books/{id}, where id is a library entry id: 204
// on success, 404 if the entry doesn't exist or doesn't belong to the
// caller, 500 otherwise. For htmx requests an HX-Redirect back to /library
// is set so the client navigates away from the deleted book's page.
func (h *handlers) bookDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.api.Delete(r.Context(), token(r), r.PathValue("id")); err != nil {
		if api.IsCode(err, api.CodeNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h.log.Error("delete entry failed", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/library")
	}
	w.WriteHeader(http.StatusNoContent)
}
