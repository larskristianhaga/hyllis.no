// Package web embeds and renders the HTML templates and static assets
// (CSS/JS) served by the application.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
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

var funcMap = template.FuncMap{
	"initial": initial,
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

// Renderer renders full pages (wrapped in the shared layout) and standalone
// HTMX partials from the embedded templates.
type Renderer struct {
	pages    map[string]*template.Template
	partials *template.Template
}

// New parses all templates and returns a ready-to-use Renderer.
func New() (*Renderer, error) {
	base, err := template.New("base").Funcs(funcMap).ParseFS(templatesFS, "templates/partials/*.html")
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
	return tmpl.ExecuteTemplate(w, "layout", data)
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
