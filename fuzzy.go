package textanchor

import (
	"strings"

	"github.com/agnivade/levenshtein"
)

// similarity calculates the similarity between two strings.
// Returns a value from 0.0 (completely different) to 1.0 (identical).
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	// For short strings, use Levenshtein distance
	if len(a) < 100 && len(b) < 100 {
		return levenshteinSimilarity(a, b)
	}

	// For longer strings, use token-based Jaccard similarity
	return jaccardSimilarity(a, b)
}

// levenshteinSimilarity calculates similarity based on edit distance.
func levenshteinSimilarity(a, b string) float64 {
	dist := levenshtein.ComputeDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1.0
	}
	return 1.0 - float64(dist)/float64(maxLen)
}

// jaccardSimilarity calculates the Jaccard similarity coefficient between two strings
// based on their word tokens.
func jaccardSimilarity(a, b string) float64 {
	tokensA := tokenize(a)
	tokensB := tokenize(b)

	if len(tokensA) == 0 && len(tokensB) == 0 {
		return 1.0
	}
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0.0
	}

	// Create sets
	setA := map[string]bool{}
	for _, t := range tokensA {
		setA[t] = true
	}

	setB := map[string]bool{}
	for _, t := range tokensB {
		setB[t] = true
	}

	// Calculate intersection
	intersection := 0
	for t := range setA {
		if setB[t] {
			intersection++
		}
	}

	// Calculate union
	union := len(setA)
	for t := range setB {
		if !setA[t] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// tokenize splits a string into lowercase word tokens.
func tokenize(s string) []string {
	words := strings.Fields(strings.ToLower(s))
	return words
}

// fuzzyMatch holds information about a fuzzy match.
type fuzzyMatch struct {
	text       string
	start      int
	end        int
	similarity float64
}

// findFuzzyMatches finds substrings in text that are similar to the query.
func findFuzzyMatches(text, query string, minSimilarity float64) []fuzzyMatch {
	var matches []fuzzyMatch

	// For very short queries, don't try fuzzy matching
	if len(query) < 5 {
		return matches
	}

	paragraphs := splitParagraphs(text)
	offset := 0

	for _, para := range paragraphs {
		// Find the paragraph's position in the original text
		paraStart := strings.Index(text[offset:], para)
		if paraStart == -1 {
			continue
		}
		paraStart += offset

		sim := similarity(query, para)
		if sim >= minSimilarity {
			// Find the best substring match within the paragraph
			match := bestSubstringMatch(para, query)
			if match.similarity >= minSimilarity {
				matches = append(matches, fuzzyMatch{
					text:       match.text,
					start:      paraStart + match.start,
					end:        paraStart + match.end,
					similarity: match.similarity,
				})
			}
		}

		offset = paraStart + len(para)
	}

	return matches
}

// bestSubstringMatch finds the substring in text most similar to query.
func bestSubstringMatch(text, query string) fuzzyMatch {
	if len(text) == 0 || len(query) == 0 {
		return fuzzyMatch{}
	}

	// If the text is shorter or similar length to query, compare directly
	if len(text) <= len(query)*2 {
		return fuzzyMatch{
			text:       text,
			start:      0,
			end:        len(text),
			similarity: similarity(text, query),
		}
	}

	// Slide a window of approximately query length across text
	windowSize := len(query)
	bestMatch := fuzzyMatch{}

	// Try different window sizes around query length
	for size := windowSize - windowSize/4; size <= windowSize+windowSize/4; size++ {
		if size <= 0 || size > len(text) {
			continue
		}

		for i := 0; i <= len(text)-size; i++ {
			substring := text[i : i+size]
			sim := similarity(substring, query)
			if sim > bestMatch.similarity {
				bestMatch = fuzzyMatch{
					text:       substring,
					start:      i,
					end:        i + size,
					similarity: sim,
				}
			}
		}
	}

	return bestMatch
}
