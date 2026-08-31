package lookup

import "regexp"

// yearPattern matches a plausible 4-digit publication year (1000-2099)
// anywhere in a date string. Providers format dates inconsistently —
// Google Books uses "1954-07-29", Open Library uses "Sep 29, 1954" — so
// this is more robust than assuming the year is a fixed prefix.
var yearPattern = regexp.MustCompile(`\b(1[0-9]{3}|20[0-9]{2})\b`)

// extractYear pulls the first 4-digit year out of date, or returns 0 if
// none is found.
func extractYear(date string) int {
	match := yearPattern.FindString(date)
	if match == "" {
		return 0
	}
	year := 0
	for _, c := range match {
		year = year*10 + int(c-'0')
	}
	return year
}
