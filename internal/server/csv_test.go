package server

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/larskristianhaga/hyllis.no/internal/book"
	"github.com/larskristianhaga/hyllis.no/internal/library"
)

func TestNormalizeISBN(t *testing.T) {
	tests := map[string]string{
		`="9780143127550"`: "9780143127550",
		"978-0-14-312755-0": "9780143127550",
		"0143127551":        "9780143127550",
		"9780143127551":     "9780143127551", // checksum is not verified, see isValidEAN13
		"12345":             "",
		"":                  "",
		"abcdefghijklm":     "",
	}
	for in, want := range tests {
		if got := normalizeISBN(in); got != want {
			t.Errorf("normalizeISBN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseImportCSV_Goodreads(t *testing.T) {
	in := "Book Id,Title,Author,ISBN,ISBN13,Publisher,Number of Pages,Year Published,Exclusive Shelf,Private Notes\n" +
		`1,Sult,Knut Hamsun,="8205123456",="9788205123456",Gyldendal,200,1890,read,Fin bok` + "\n" +
		`2,Uten ISBN,Ola,="",="",,,,to-read,` + "\n" +
		`3,Pågående,Kari,="",="9780143127550",,,,currently-reading,` + "\n"

	got, skipped, err := parseImportCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseImportCSV: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(got), got)
	}
	if got[0].ISBN != "9788205123456" || got[0].Title != "Sult" || got[0].Year != 1890 ||
		got[0].Pages != 200 || got[0].Status != library.StatusRead || got[0].Notes != "Fin bok" {
		t.Errorf("first record = %+v", got[0])
	}
	if got[1].Status != library.StatusReading {
		t.Errorf("second record status = %q, want %q", got[1].Status, library.StatusReading)
	}
}

func TestParseImportCSV_MissingColumns(t *testing.T) {
	if _, _, err := parseImportCSV(strings.NewReader("foo,bar\n1,2\n")); err == nil {
		t.Fatal("want an error for a CSV without isbn/title columns")
	}
}

func TestCSVExportImportRoundTrip(t *testing.T) {
	rows := []libraryRow{{
		Book: &book.Book{ISBN: "9780143127550", Title: "=cmd()", Author: "Å, Ø", Source: "nb", Year: 2001},
		Entry: &library.Entry{
			Status: library.StatusReading, Notes: "linje 1\nlinje 2", Location: "Stue",
			AddedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		},
	}}

	var buf bytes.Buffer
	if err := writeLibraryCSV(&buf, rows); err != nil {
		t.Fatalf("writeLibraryCSV: %v", err)
	}
	if !strings.Contains(buf.String(), "'=cmd()") {
		t.Errorf("export did not defuse the formula cell: %s", buf.String())
	}

	got, skipped, err := parseImportCSV(&buf)
	if err != nil || skipped != 0 || len(got) != 1 {
		t.Fatalf("parseImportCSV = %+v, %d, %v", got, skipped, err)
	}
	r := got[0]
	if r.Title != "=cmd()" || r.Author != "Å, Ø" || r.Year != 2001 ||
		r.Status != library.StatusReading || r.Notes != "linje 1\nlinje 2" || r.Location != "Stue" {
		t.Errorf("round-tripped record = %+v", r)
	}
}
