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

// Tuning constants for the fuzzy phase. They exist to bound the work done per
// paragraph, since bestSubstringMatch is O(windows x levenshtein) and the
// resolve path runs per anchor on a request.
const (
	// fuzzyMinSimilarity is the score a windowed match must reach to be
	// offered as a candidate. It is the only similarity gate in the phase.
	fuzzyMinSimilarity = 0.6

	// fuzzyMaxComparisons caps the TOTAL number of similarity calls one
	// bestSubstringMatch may make, across every window size it tries.
	//
	// Capping per size instead would not bound anything useful: the size range
	// widens with the query, so a per-size cap of n silently authorises n times
	// the number of sizes. At a 76-byte query that is 39 sizes, which on a large
	// paragraph reached hundreds of thousands of Levenshtein calls — seconds of
	// CPU on one resolve, on what is meant to be a request path.
	//
	// This is a genuine accuracy/latency trade, not free: the budget is spent as
	// a stride over start positions, so lowering it samples a long paragraph
	// more coarsely and can miss the best window. 1024 is already low enough to
	// mislocate a match in the CJK reflow test. Re-tune against those tests
	// rather than by benchmark alone.
	fuzzyMaxComparisons = 4096

	// fuzzyPerfectMatch is the score at or above which the search stops early;
	// nothing better is worth paying for.
	fuzzyPerfectMatch = 0.995
)

// findFuzzyMatches finds substrings in text that are similar to the query.
//
// Each paragraph is scanned with a sliding window and judged on the BEST WINDOW
// it contains, not on the paragraph as a whole. Scoring the whole paragraph
// asks the wrong question: similarity is length-sensitive, so a short quote
// inside a long paragraph scores low however exactly it appears there — a
// 35-character quote in a 151-character paragraph scores 0.250 even when the
// paragraph contains it verbatim. Gating on that number made the windowed
// search unreachable in exactly the cases it was written to rescue.
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
		offset = paraStart + len(para)

		// The whole paragraph is handed over in one call: bestSubstringMatch
		// strides its start positions within a fixed comparison budget, so it
		// already covers a long paragraph at reduced resolution rather than
		// linearly in its length. Splitting it into chunks first would multiply
		// that budget by the chunk count, which is what made a 60KB paragraph
		// cost seconds.
		match := trimMatch(bestSubstringMatch(para, query))
		if match.similarity >= minSimilarity {
			matches = append(matches, fuzzyMatch{
				text:       match.text,
				start:      paraStart + match.start,
				end:        paraStart + match.end,
				similarity: match.similarity,
			})
		}
	}

	return matches
}

// bestSubstringMatch finds the substring in text most similar to query.
//
// The number of similarity calls is bounded by fuzzyMaxComparisons regardless
// of how long text is; see windowStride for how that budget is spent.
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

	// Try every window size around the query length. Sampling sizes rather than
	// sweeping them looks like a cheap win but is not sound: the size that fits
	// is not predictable from the query length once whitespace collapse and
	// multi-byte runes are involved. A 12-rune CJK query is 36 bytes while the
	// span that matches it is 37, so a stride of 3 steps straight over the
	// answer and settles for a worse-scoring neighbour. The budget is spent on
	// start positions instead, where striding only samples where a span begins
	// rather than skipping the span outright.
	minSize := windowSize - windowSize/4
	maxSize := windowSize + windowSize/4

	for size := minSize; size <= maxSize; size++ {
		if size <= 0 || size > len(text) {
			continue
		}

		posStep := windowStride(len(text), size, minSize, maxSize)

		for i := 0; i <= len(text)-size; i += posStep {
			// Only consider windows delimited by rune boundaries. A window
			// starting or ending inside a multi-byte rune scores worse than the
			// aligned window covering the same characters — the split rune reads
			// as an edit — so in text without spaces to break on (CJK) the
			// correct span loses to a misaligned neighbour. Skipping is also
			// cheaper than scoring a window that can never win.
			end := i + size
			if isContinuationByte(text[i]) || (end < len(text) && isContinuationByte(text[end])) {
				continue
			}

			sim := similarity(text[i:end], query)
			if sim > bestMatch.similarity {
				bestMatch = fuzzyMatch{
					text:       text[i:end],
					start:      i,
					end:        end,
					similarity: sim,
				}
				if sim >= fuzzyPerfectMatch {
					return bestMatch
				}
			}
		}
	}

	return bestMatch
}

// windowStride returns the step between start positions for one window size,
// sharing fuzzyMaxComparisons evenly across every size in [minSize, maxSize].
//
// Striding rather than truncating keeps the whole text reachable: a cap that
// simply stopped after n positions would leave the tail of a long paragraph
// unsearched, so a quote near the end could never be found.
func windowStride(textLen, size, minSize, maxSize int) int {
	sizeCount := maxSize - minSize + 1
	if sizeCount < 1 {
		sizeCount = 1
	}
	startsPerSize := fuzzyMaxComparisons / sizeCount
	if startsPerSize < 1 {
		startsPerSize = 1
	}

	starts := textLen - size + 1
	if starts <= startsPerSize {
		return 1
	}
	return (starts + startsPerSize - 1) / startsPerSize
}

// trimMatch pulls whitespace off both ends of a windowed match.
//
// The window size is chosen by arithmetic around the query length, so its edges
// land wherever they land — typically half a word out. Leading or trailing
// whitespace inside the span is never part of what the user selected, and it
// would otherwise be handed back inside the resolved Range.
func trimMatch(m fuzzyMatch) fuzzyMatch {
	for len(m.text) > 0 && isASCIISpace(m.text[0]) {
		m.text = m.text[1:]
		m.start++
	}
	for len(m.text) > 0 && isASCIISpace(m.text[len(m.text)-1]) {
		m.text = m.text[:len(m.text)-1]
		m.end--
	}
	return m
}

// isASCIISpace reports whether b is whitespace. The collapsed form this runs
// over contains no whitespace other than the single ASCII space it emits, so a
// byte test is sufficient and a rune decode would be wasted work.
func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
