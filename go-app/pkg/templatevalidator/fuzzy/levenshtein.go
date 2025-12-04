package fuzzy

import (
	"sort"
)

// ================================================================================
// TN-156: Template Validator - Fuzzy Matcher (Levenshtein Distance)
// ================================================================================
// Fuzzy string matching using Levenshtein distance algorithm.
//
// Features:
// - Levenshtein distance calculation
// - Find closest match from candidates
// - Top-N results with distance threshold
// - Case-insensitive matching
//
// Performance Target: < 1ms for 100 candidates
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// FuzzyMatcher is the interface for fuzzy string matching
//
// FuzzyMatcher finds similar strings using edit distance algorithms.
// Used for function name suggestions in error messages.
//
// Example:
//
//	matcher := NewLevenshteinMatcher()
//	closest := matcher.FindClosest("toUpperCase", []string{"toUpper", "toLower", "default"}, 3)
//	// Returns "toUpper" (distance: 4)
type FuzzyMatcher interface {
	// FindClosest finds the closest match from candidates
	//
	// Returns the closest matching string with edit distance <= threshold.
	// Returns empty string if no match found within threshold.
	//
	// Parameters:
	// - target: string to match
	// - candidates: slice of candidate strings
	// - threshold: maximum edit distance (0 = exact match only)
	//
	// Example:
	//   closest := matcher.FindClosest("toUpperCase", functions, 3)
	FindClosest(target string, candidates []string, threshold int) string

	// FindTopN finds the top N closest matches
	//
	// Returns up to N closest matches sorted by distance (ascending).
	// Only includes matches with distance <= threshold.
	//
	// Parameters:
	// - target: string to match
	// - candidates: slice of candidate strings
	// - n: maximum number of results
	// - threshold: maximum edit distance
	//
	// Example:
	//   matches := matcher.FindTopN("toUpperCase", functions, 3, 5)
	//   // Returns: ["toUpper", "toLower", "toTitle"]
	FindTopN(target string, candidates []string, n int, threshold int) []string
}

// ================================================================================

// LevenshteinMatcher implements FuzzyMatcher using Levenshtein distance
//
// Levenshtein distance is the minimum number of single-character edits
// (insertions, deletions, substitutions) required to change one string into another.
//
// Example:
//
//	"toUpperCase" → "toUpper" (distance: 4)
//	"toUpperCase" → "toUppercase" (distance: 1)
type LevenshteinMatcher struct {
	// caseSensitive controls case sensitivity (default: false)
	caseSensitive bool
}

// NewLevenshteinMatcher creates a new Levenshtein matcher
//
// Default: case-insensitive matching
func NewLevenshteinMatcher() *LevenshteinMatcher {
	return &LevenshteinMatcher{
		caseSensitive: false,
	}
}

// NewCaseSensitiveLevenshteinMatcher creates a case-sensitive matcher
func NewCaseSensitiveLevenshteinMatcher() *LevenshteinMatcher {
	return &LevenshteinMatcher{
		caseSensitive: true,
	}
}

// ================================================================================

// FindClosest finds the closest match from candidates
func (m *LevenshteinMatcher) FindClosest(
	target string,
	candidates []string,
	threshold int,
) string {
	if len(candidates) == 0 {
		return ""
	}

	// Prepare target for comparison
	targetCmp := target
	if !m.caseSensitive {
		targetCmp = toLower(target)
	}

	// Find minimum distance
	minDistance := threshold + 1
	closestMatch := ""

	for _, candidate := range candidates {
		candidateCmp := candidate
		if !m.caseSensitive {
			candidateCmp = toLower(candidate)
		}

		distance := levenshteinDistance(targetCmp, candidateCmp)

		// Update closest match if this is better
		if distance < minDistance {
			minDistance = distance
			closestMatch = candidate
		}
	}

	// Return empty if no match within threshold
	if minDistance > threshold {
		return ""
	}

	return closestMatch
}

// FindTopN finds the top N closest matches
func (m *LevenshteinMatcher) FindTopN(
	target string,
	candidates []string,
	n int,
	threshold int,
) []string {
	if len(candidates) == 0 || n <= 0 {
		return []string{}
	}

	// Prepare target for comparison
	targetCmp := target
	if !m.caseSensitive {
		targetCmp = toLower(target)
	}

	// Calculate distances for all candidates
	type match struct {
		candidate string
		distance  int
	}

	matches := make([]match, 0, len(candidates))

	for _, candidate := range candidates {
		candidateCmp := candidate
		if !m.caseSensitive {
			candidateCmp = toLower(candidate)
		}

		distance := levenshteinDistance(targetCmp, candidateCmp)

		// Only include matches within threshold
		if distance <= threshold {
			matches = append(matches, match{
				candidate: candidate,
				distance:  distance,
			})
		}
	}

	// Sort by distance (ascending)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].distance < matches[j].distance
	})

	// Return top N
	limit := n
	if limit > len(matches) {
		limit = len(matches)
	}

	results := make([]string, limit)
	for i := 0; i < limit; i++ {
		results[i] = matches[i].candidate
	}

	return results
}

// ================================================================================

// levenshteinDistance calculates the Levenshtein distance between two strings
//
// Algorithm: Dynamic programming with O(m*n) time and O(min(m,n)) space.
//
// Example:
//
//	levenshteinDistance("kitten", "sitting") = 3
//	  kitten → sitten (substitute k with s)
//	  sitten → sittin (substitute e with i)
//	  sittin → sitting (insert g)
func levenshteinDistance(s1, s2 string) int {
	// Handle empty strings
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	// Ensure s1 is the shorter string (space optimization)
	if len(s1) > len(s2) {
		s1, s2 = s2, s1
	}

	// Create distance matrix (2 rows only)
	// We only need previous and current row
	lenS1 := len(s1)
	lenS2 := len(s2)

	// Previous row (initialized to 0, 1, 2, 3, ...)
	prevRow := make([]int, lenS1+1)
	for i := range prevRow {
		prevRow[i] = i
	}

	// Current row
	currRow := make([]int, lenS1+1)

	// Fill the matrix
	for i := 1; i <= lenS2; i++ {
		currRow[0] = i

		for j := 1; j <= lenS1; j++ {
			// Cost of substitution
			cost := 1
			if s2[i-1] == s1[j-1] {
				cost = 0
			}

			// Minimum of:
			// - deletion: currRow[j-1] + 1
			// - insertion: prevRow[j] + 1
			// - substitution: prevRow[j-1] + cost
			currRow[j] = min3(
				currRow[j-1]+1,    // deletion
				prevRow[j]+1,      // insertion
				prevRow[j-1]+cost, // substitution
			)
		}

		// Swap rows (current becomes previous)
		prevRow, currRow = currRow, prevRow
	}

	// Result is in prevRow[lenS1] (last cell of last computed row)
	return prevRow[lenS1]
}

// ================================================================================

// min3 returns the minimum of three integers
func min3(a, b, c int) int {
	if a <= b && a <= c {
		return a
	}
	if b <= c {
		return b
	}
	return c
}

// toLower converts string to lowercase (ASCII only for performance)
func toLower(s string) string {
	// Fast path for ASCII strings
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + ('a' - 'A')
		} else {
			result[i] = c
		}
	}
	return string(result)
}

// ================================================================================
