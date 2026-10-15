// Package web embeds and renders the HTML templates and static assets
// (CSS/JS) served by the application.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/larskristianhaga/hyllis.no/internal/auth"
)

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// pageNames lists every page template under templates/pages that the
// Renderer parses at startup, keyed by the name handlers pass to Page.
var pageNames = []string{
	"home",
	"scan",
	"library",
	"book_detail",
	"login",
	"register",
}

// pageTitles are the per-page <title>s (before the " – hyllis" suffix).
// Pages whose title depends on their data implement Titled instead.
var pageTitles = map[string]string{
	"scan":     "Skann bok",
	"library":  "Bibliotek",
	"login":    "Logg inn",
	"register": "Registrer deg",
}

// Titled can be implemented by a page's data to override pageTitles, e.g.
// to use a book's title as the page title.
type Titled interface {
	PageTitle() string
}

const siteName = "hyllis"

// pageTitle builds the full <title> for a page.
func pageTitle(name string, data any) string {
	title := pageTitles[name]
	if t, ok := data.(Titled); ok {
		title = t.PageTitle()
	}
	if title == "" {
		return siteName + " – skann, lagre og finn bøkene dine"
	}
	return title + " – " + siteName
}

// isActive reports whether the request path belongs to a nav item: "/"
// matches only the exact root, any other prefix matches itself and its
// sub-paths.
func isActive(path string, prefixes ...string) bool {
	for _, p := range prefixes {
		if p == "/" {
			if path == "/" {
				return true
			}
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// httpsURL upgrades http:// URLs (metadata sources such as Google Books
// still return plain-http cover links) so covers aren't blocked as mixed
// content.
func httpsURL(u string) string {
	if rest, ok := strings.CutPrefix(u, "http://"); ok {
		return "https://" + rest
	}
	return u
}

// assetVersion hashes every embedded static file so asset URLs change
// exactly when their content does (and can then be cached "immutable").
func assetVersion() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := staticFS.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func newFuncMap() template.FuncMap {
	version := assetVersion()
	return template.FuncMap{
		"initial":     initial,
		"sourceLabel": sourceLabel,
		"isActive":    isActive,
		"https":       httpsURL,
		// asset returns a cache-busted /static URL, e.g.
		// {{asset "css/styles.css"}} -> /static/css/styles.css?v=<hash>.
		"asset": func(path string) string {
			return "/static/" + path + "?v=" + version
		},
	}
}

// initial returns an uppercased, one-character placeholder used for the
// text-only book cover shown in cards and detail views.
func initial(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "?"
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0]))
}

// sourceLabels maps book.Book.Source values (as stored, per CLAUDE.md's
// "kilde" field) to the human-readable names shown in the UI.
var sourceLabels = map[string]string{
	"google_books":  "Google Books",
	"open_library":  "Open Library",
	"nb":            "Nasjonalbiblioteket",
	"bibliotekenes": "Bibliotekenes",
	"manual":        "Manuelt registrert",
}

// sourceLabel returns the full display name for a book's source. Unknown
// values are returned unchanged so the field never renders empty.
func sourceLabel(source string) string {
	if label, ok := sourceLabels[source]; ok {
		return label
	}
	return source
}

// Renderer renders full pages (wrapped in the shared layout) and standalone
// HTMX partials from the embedded templates.
type Renderer struct {
	pages    map[string]*template.Template
	partials *template.Template
}

// New parses all templates and returns a ready-to-use Renderer.
func New() (*Renderer, error) {
	base, err := template.New("base").Funcs(newFuncMap()).ParseFS(templatesFS, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
	}

	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone base template for page %q: %w", name, err)
		}
		clone, err = clone.ParseFS(templatesFS, "templates/layout.html", "templates/pages/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse page %q: %w", name, err)
		}
		pages[name] = clone
	}

	return &Renderer{pages: pages, partials: base}, nil
}

// isHTMXRequest reports whether r was issued by htmx (hx-get/hx-post/htmx.ajax
// etc.), identified by the HX-Request request header htmx sets on every
// request it makes.
func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Page renders the named page with a 200 status. Plain requests get the
// full HTML5 document (layout + page content); HTMX requests (HX-Request
// header present) get just the page's inner content, without the
// surrounding <html>/<head>/nav shell, so htmx can swap it into the DOM.
func (rd *Renderer) Page(w http.ResponseWriter, r *http.Request, name string, data any) error {
	return rd.PageWithStatus(w, r, name, http.StatusOK, data)
}

// layoutData wraps a page's own data with the auth state layout.html needs
// to decide whether to show the login/register links or a logout button —
// kept separate from every page's data struct so handlers don't each need
// their own Authenticated field (see homeData's, which predates this and is
// now redundant but harmless).
type layoutData struct {
	Authenticated bool
	Title         string
	// Path is the request path, used to mark the active nav item.
	Path string
	Page any
}

// PageWithStatus renders the named page like Page, but with an explicit
// status code (e.g. 404 for a missing book). The status must be set before
// any header is written, so callers should not call w.WriteHeader
// themselves — pass the status here instead.
func (rd *Renderer) PageWithStatus(w http.ResponseWriter, r *http.Request, name string, status int, data any) error {
	tmpl, ok := rd.pages[name]
	if !ok {
		return fmt.Errorf("unknown page %q", name)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	if isHTMXRequest(r) {
		return tmpl.ExecuteTemplate(w, "page", data)
	}
	_, authenticated := auth.UserFromContext(r.Context())
	return tmpl.ExecuteTemplate(w, "layout", layoutData{
		Authenticated: authenticated,
		Title:         pageTitle(name, data),
		Path:          r.URL.Path,
		Page:          data,
	})
}

// Partial renders the named partial directly, with no layout wrapper. It is
// used by HTMX endpoints that always return a fragment (scan lookups,
// library search, etc.), regardless of the HX-Request header.
func (rd *Renderer) Partial(w http.ResponseWriter, name string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return rd.partials.ExecuteTemplate(w, name, data)
}

// StaticFS returns the embedded static assets (CSS/JS), rooted so paths
// don't include the "static/" prefix, ready to be served over HTTP.
func (rd *Renderer) StaticFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// staticFS is embedded at compile time with a "static" directory
		// present, so this can't fail in practice.
		panic(err)
	}
	return sub
}
