package server

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/auth"
	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
)

// Limits for CSV import.
const (
	maxImportBytes = 2 << 20 // 2 MiB
	maxImportRows  = 5000
)

// exportColumns is the header of the CSV written by libraryExport. Import
// accepts these names plus the Goodreads export's (see importAliases).
var exportColumns = []string{
	"isbn", "title", "author", "publisher", "year", "language", "pages",
	"source", "status", "notes", "location", "added_at",
}

// importAliases maps each logical import field to the (lower-cased) header
// names it can be found under, most preferred first. It covers both this
// app's own export and Goodreads' "Export Library" CSV.
var importAliases = map[string][]string{
	"isbn":      {"isbn13", "isbn"},
	"title":     {"title", "tittel"},
	"author":    {"author", "forfatter"},
	"publisher": {"publisher", "forlag"},
	"year":      {"year", "year published", "original publication year"},
	"language":  {"language", "språk"},
	"pages":     {"pages", "number of pages"},
	"status":    {"status", "exclusive shelf"},
	"notes":     {"notes", "private notes"},
	"location":  {"location", "plassering"},
}

// libraryExport backs GET /library/export.csv: the caller's whole library
// as a UTF-8 CSV download.
func (h *handlers) libraryExport(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.UserFromContext(r.Context())

	rows, err := h.userRows(r, claims.UserID())
	if err != nil {
		http.Error(w, "Kunne ikke hente biblioteket.", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="hyllis-bibliotek.csv"`)

	// A BOM makes Excel pick UTF-8 (æøå) instead of the system code page.
	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return
	}
	if err := writeLibraryCSV(w, rows); err != nil {
		h.log.Error("library export failed mid-stream", "error", err)
	}
}

func writeLibraryCSV(w io.Writer, rows []libraryRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(exportColumns); err != nil {
		return err
	}
	for _, row := range rows {
		b, e := row.Book, row.Entry
		record := []string{
			b.ISBN,
			csvSafe(b.Title),
			csvSafe(b.Author),
			csvSafe(b.Publisher),
			optionalInt(b.Year),
			csvSafe(b.Language),
			optionalInt(b.Pages),
			b.Source,
			e.Status,
			csvSafe(e.Notes),
			csvSafe(e.Location),
			e.AddedAt.UTC().Format(time.RFC3339),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func optionalInt(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// csvFormulaPrefixes are the leading characters spreadsheet apps treat as
// the start of a formula.
const csvFormulaPrefixes = "=+-@\t\r"

// csvSafe defuses CSV/formula injection: a cell starting with a formula
// character gets a leading apostrophe. csvUnsafe undoes that on import so
// export → import round-trips.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune(csvFormulaPrefixes, rune(s[0])) {
		return "'" + s
	}
	return s
}

func csvUnsafe(s string) string {
	if len(s) > 1 && s[0] == '\'' && strings.ContainsRune(csvFormulaPrefixes, rune(s[1])) {
		return s[1:]
	}
	return s
}

// importRecord is one parsed row of an import file.
type importRecord struct {
	ISBN      string
	Title     string
	Author    string
	Publisher string
	Language  string
	Year      int
	Pages     int
	Status    string
	Notes     string
	Location  string
}

// libraryImport backs POST /library/import. It adds the books of an
// uploaded CSV (this app's own export, or a Goodreads export) to the
// caller's library using only the data in the file — no external lookups,
// per CLAUDE.md's rule that only the scan endpoint talks to external
// services. Books the user already owns are left untouched.
func (h *handlers) libraryImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+(64<<10))
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Kunne ikke lese opplastingen (maks 2 MB)."})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: "Velg en CSV-fil å importere."})
		return
	}
	defer file.Close()

	records, skipped, err := parseImportCSV(file)
	if err != nil {
		h.render.Partial(w, "error-message", errorMessageData{Message: err.Error()})
		return
	}

	result := importResultData{Skipped: skipped}
	for _, rec := range records {
		author := rec.Author
		if author == "" {
			author = "Ukjent forfatter"
		}
		b := &book.Book{
			ISBN:      rec.ISBN,
			Title:     rec.Title,
			Author:    author,
			Publisher: rec.Publisher,
			Year:      rec.Year,
			Language:  rec.Language,
			Pages:     rec.Pages,
			Source:    "manual",
		}
		details := &library.Entry{Status: rec.Status, Notes: rec.Notes, Location: rec.Location}

		_, existed, err := h.saveBook(r, b, details)
		if err != nil {
			h.log.Error("import: failed to save book", "isbn", rec.ISBN, "error", err)
			result.Skipped++
			continue
		}
		if existed {
			result.Existing++
		} else {
			result.Added++
		}
	}

	h.render.Partial(w, "import-result", result)
}

// parseImportCSV reads an import file, returning the importable records and
// how many data rows were skipped (no usable ISBN or title).
func parseImportCSV(r io.Reader) ([]importRecord, int, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, 0, errors.New("Filen er tom eller er ikke en gyldig CSV-fil.")
	}
	cols := make(map[string]int, len(header))
	for i, name := range header {
		name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))
		if _, seen := cols[name]; !seen {
			cols[name] = i
		}
	}

	// field returns the first non-empty value among a field's aliases.
	field := func(row []string, logical string) string {
		for _, alias := range importAliases[logical] {
			if i, ok := cols[alias]; ok && i < len(row) {
				if v := strings.TrimSpace(row[i]); v != "" {
					return v
				}
			}
		}
		return ""
	}

	hasColumn := func(logical string) bool {
		for _, alias := range importAliases[logical] {
			if _, ok := cols[alias]; ok {
				return true
			}
		}
		return false
	}
	if !hasColumn("isbn") || !hasColumn("title") {
		return nil, 0, errors.New("CSV-filen må ha kolonnene «isbn» og «title» (eller ISBN13 og Title fra Goodreads).")
	}

	var (
		records []importRecord
		skipped int
	)
	for n := 0; ; n++ {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			skipped++
			continue
		}
		if n >= maxImportRows {
			return nil, 0, fmt.Errorf("Filen har for mange rader (maks %d).", maxImportRows)
		}

		// Goodreads fills ISBN13 with ="" for books it has no ISBN-13 for, so
		// try each ISBN column in turn instead of taking the first non-empty one.
		isbn := ""
		for _, alias := range importAliases["isbn"] {
			if i, ok := cols[alias]; ok && i < len(row) {
				if isbn = normalizeISBN(row[i]); isbn != "" {
					break
				}
			}
		}
		title := csvUnsafe(field(row, "title"))
		if isbn == "" || title == "" {
			skipped++
			continue
		}

		year, _ := strconv.Atoi(field(row, "year"))
		pages, _ := strconv.Atoi(field(row, "pages"))
		records = append(records, importRecord{
			ISBN:      isbn,
			Title:     title,
			Author:    csvUnsafe(field(row, "author")),
			Publisher: csvUnsafe(field(row, "publisher")),
			Language:  csvUnsafe(field(row, "language")),
			Year:      year,
			Pages:     pages,
			Status:    importStatus(field(row, "status")),
			Notes:     truncateRunes(csvUnsafe(field(row, "notes")), maxNotesLen),
			Location:  truncateRunes(csvUnsafe(field(row, "location")), maxLocationLen),
		})
	}
	return records, skipped, nil
}

// importStatus maps a status column (ours, or Goodreads' "Exclusive Shelf")
// to a library status, defaulting to unread.
func importStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read", library.StatusRead:
		return library.StatusRead
	case "currently-reading", "reading":
		return library.StatusReading
	default:
		return library.StatusUnread
	}
}

// normalizeISBN returns a 13-digit ISBN for s, or "" if s isn't one.
// Goodreads exports ISBNs as ="9780143127550", so everything but digits and
// X is dropped first. ISBN-10s are converted to ISBN-13.
func normalizeISBN(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == 'X' || r == 'x' {
			sb.WriteRune(r)
		}
	}
	clean := sb.String()

	switch len(clean) {
	case 13:
		if !isValidEAN13(clean) {
			return ""
		}
		return clean
	case 10:
		body := "978" + clean[:9]
		if !isValidEAN13(body + "0") {
			return ""
		}
		sum := 0
		for i, r := range body {
			d := int(r - '0')
			if i%2 == 1 {
				d *= 3
			}
			sum += d
		}
		return body + strconv.Itoa((10-sum%10)%10)
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
