// Package fuzzy implements small, dependency-free typo-tolerant text
// matching, shared by the in-memory book repository and the library search
// handlers.
package fuzzy

import "strings"

// Match reports whether needle fuzzily matches haystack: every
// whitespace-separated word in needle must either appear as a substring of
// some word in haystack, or be within a small Levenshtein edit-distance of
// one — enough to tolerate a typo or two per word without turning into an
// unbounded approximate match. Matching is case-insensitive on both sides.
// An empty (or whitespace-only) needle matches everything.
func Match(haystack, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return true
	}

	haystackWords := strings.Fields(strings.ToLower(haystack))
	for _, needleWord := range strings.Fields(needle) {
		if !matchesAnyWord(needleWord, haystackWords) {
			return false
		}
	}
	return true
}

// matchesAnyWord reports whether needleWord fuzzily matches any word in
// haystackWords. Words of two characters or fewer only match by substring —
// edit-distance tolerance on something that short would match almost
// anything.
func matchesAnyWord(needleWord string, haystackWords []string) bool {
	for _, w := range haystackWords {
		if strings.Contains(w, needleWord) {
			return true
		}
	}
	if len(needleWord) <= 2 {
		return false
	}

	tolerance := wordTolerance(needleWord)
	for _, w := range haystackWords {
		if levenshtein(needleWord, w) <= tolerance {
			return true
		}
	}
	return false
}

// wordTolerance scales edit-distance tolerance with word length: short
// words (3-4 letters) tolerate a single typo, longer words tolerate two.
func wordTolerance(word string) int {
	if len(word) <= 4 {
		return 1
	}
	return 2
}

// levenshtein returns the edit distance between a and b, computed over
// runes so multi-byte characters (e.g. æ/ø/å) count as one position each.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
