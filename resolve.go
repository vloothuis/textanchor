package textanchor

import (
	"strings"
)

// candidate represents a potential match during resolution.
type candidate struct {
	rng   Range
	score float64
}

// Resolve attempts to locate an anchor in a document.
//
// The resolution algorithm:
//  1. Find all exact matches for anchor.Quote
//  2. Score each match by prefix/suffix similarity
//  3. Apply structural context as a tiebreaker
//  4. Return the best match above the confidence threshold
//
// If no match meets the threshold, returns Orphaned: true.
func Resolve(document string, anchor Anchor, opts *ResolveOptions) ResolveResult {
	if opts == nil {
		opts = DefaultResolveOptions()
	}

	candidates := findCandidates(document, anchor)

	if len(candidates) == 0 {
		return ResolveResult{
			Orphaned:     true,
			OrphanReason: "quote not found in document",
		}
	}

	// Score candidates by context
	for i := range candidates {
		candidates[i].score = scoreCandidate(document, anchor, candidates[i], opts)
	}

	// Find the best candidate
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}

	if best.score < opts.MinConfidence {
		return ResolveResult{
			Orphaned:     true,
			OrphanReason: "confidence too low",
			Confidence:   best.score,
		}
	}

	return ResolveResult{
		Range:      &Range{Start: best.rng.Start, End: best.rng.End},
		Confidence: best.score,
		Orphaned:   false,
	}
}

// ResolveAll resolves multiple anchors efficiently.
// This may be faster than calling Resolve repeatedly as it can
// share preprocessing work.
func ResolveAll(document string, anchors []Anchor, opts *ResolveOptions) []ResolveResult {
	if opts == nil {
		opts = DefaultResolveOptions()
	}

	results := make([]ResolveResult, len(anchors))

	// Pre-compute document structure for structural matching
	// This is shared across all resolutions
	headingPositions := extractHeadingPositions(document)
	paragraphBoundaries := extractParagraphBoundaries(document)

	for i, anchor := range anchors {
		candidates := findCandidates(document, anchor)

		if len(candidates) == 0 {
			results[i] = ResolveResult{
				Orphaned:     true,
				OrphanReason: "quote not found in document",
			}
			continue
		}

		// Score candidates
		for j := range candidates {
			candidates[j].score = scoreCandidateWithCache(
				document, anchor, candidates[j], opts,
				headingPositions, paragraphBoundaries,
			)
		}

		// Find best
		best := candidates[0]
		for _, c := range candidates[1:] {
			if c.score > best.score {
				best = c
			}
		}

		if best.score < opts.MinConfidence {
			results[i] = ResolveResult{
				Orphaned:     true,
				OrphanReason: "confidence too low",
				Confidence:   best.score,
			}
		} else {
			results[i] = ResolveResult{
				Range:      &Range{Start: best.rng.Start, End: best.rng.End},
				Confidence: best.score,
				Orphaned:   false,
			}
		}
	}

	return results
}

// findCandidates finds all potential matches for an anchor's quote.
func findCandidates(document string, anchor Anchor) []candidate {
	var candidates []candidate

	// Phase 1: Exact quote matching
	quote := anchor.Quote
	offset := 0
	for {
		idx := strings.Index(document[offset:], quote)
		if idx == -1 {
			break
		}
		start := offset + idx
		end := start + len(quote)
		candidates = append(candidates, candidate{
			rng:   Range{Start: start, End: end},
			score: 1.0, // Start with perfect score for exact match
		})
		offset = start + 1 // Allow overlapping matches
	}

	// Phase 2: Fuzzy quote matching if no exact matches
	if len(candidates) == 0 {
		fuzzyMatches := findFuzzyMatches(document, quote, 0.6)
		for _, match := range fuzzyMatches {
			candidates = append(candidates, candidate{
				rng:   Range{Start: match.start, End: match.end},
				score: match.similarity * 0.8, // Penalty for fuzzy match
			})
		}
	}

	return candidates
}

// scoreCandidate scores a candidate based on context matching.
func scoreCandidate(document string, anchor Anchor, c candidate, opts *ResolveOptions) float64 {
	return scoreCandidateWithCache(document, anchor, c, opts, nil, nil)
}

// scoreCandidateWithCache scores a candidate with optional cached document structure.
func scoreCandidateWithCache(
	document string,
	anchor Anchor,
	c candidate,
	opts *ResolveOptions,
	headingPositions []headingPos,
	paragraphBoundaries []int,
) float64 {
	// Start with the base score (1.0 for exact match, lower for fuzzy)
	baseScore := c.score

	// Calculate prefix similarity
	prefixScore := 0.0
	if len(anchor.Prefix) > 0 {
		prefixStart := c.rng.Start - len(anchor.Prefix)
		if prefixStart < 0 {
			prefixStart = 0
		}
		actualPrefix := document[prefixStart:c.rng.Start]
		prefixScore = similarity(actualPrefix, anchor.Prefix)
	} else {
		prefixScore = 1.0 // No prefix to match
	}

	// Calculate suffix similarity
	suffixScore := 0.0
	if len(anchor.Suffix) > 0 {
		suffixEnd := c.rng.End + len(anchor.Suffix)
		if suffixEnd > len(document) {
			suffixEnd = len(document)
		}
		actualSuffix := document[c.rng.End:suffixEnd]
		suffixScore = similarity(actualSuffix, anchor.Suffix)
	} else {
		suffixScore = 1.0 // No suffix to match
	}

	// Calculate structural score
	structuralScore := 0.0
	if opts.PreferStructuralMatch && anchor.HeadingContext != "" {
		if headingPositions == nil {
			headingPositions = extractHeadingPositions(document)
		}
		if paragraphBoundaries == nil {
			paragraphBoundaries = extractParagraphBoundaries(document)
		}
		structuralScore = computeStructuralSimilarity(
			document, c.rng, anchor,
			headingPositions, paragraphBoundaries,
		)
	} else {
		structuralScore = 0.5 // Neutral if not using structural matching
	}

	// Calculate uniqueness bonus
	uniquenessBonus := 0.0
	occurrences := strings.Count(document, anchor.Quote)
	if occurrences == 1 {
		uniquenessBonus = 1.0
	} else if occurrences > 0 {
		uniquenessBonus = 1.0 / float64(occurrences)
	}

	// Combine scores according to weights from the design doc
	// Quote match: 40%, Prefix: 20%, Suffix: 20%, Structural: 10%, Uniqueness: 10%
	finalScore := 0.40*baseScore +
		0.20*prefixScore +
		0.20*suffixScore +
		0.10*structuralScore +
		0.10*uniquenessBonus

	return finalScore
}

// headingPos stores the position and text of a heading.
type headingPos struct {
	text  string
	start int
	end   int
}

// extractHeadingPositions finds all headings in the document.
func extractHeadingPositions(document string) []headingPos {
	var headings []headingPos

	lines := strings.Split(document, "\n")
	offset := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			headingText := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			headings = append(headings, headingPos{
				text:  headingText,
				start: offset,
				end:   offset + len(line),
			})
		}
		offset += len(line) + 1 // +1 for newline
	}

	return headings
}

// extractParagraphBoundaries returns the starting positions of paragraphs.
func extractParagraphBoundaries(document string) []int {
	var boundaries []int
	inParagraph := false
	offset := 0

	lines := strings.Split(document, "\n")
	for _, line := range lines {
		isBlank := strings.TrimSpace(line) == ""

		if !isBlank && !inParagraph {
			boundaries = append(boundaries, offset)
			inParagraph = true
		} else if isBlank {
			inParagraph = false
		}

		offset += len(line) + 1
	}

	return boundaries
}

// computeStructuralSimilarity calculates how well a candidate matches structural context.
func computeStructuralSimilarity(
	_ string,
	rng Range,
	anchor Anchor,
	headingPositions []headingPos,
	paragraphBoundaries []int,
) float64 {
	// Find the heading containing this position
	currentHeading := ""
	for i := len(headingPositions) - 1; i >= 0; i-- {
		if headingPositions[i].start < rng.Start {
			currentHeading = headingPositions[i].text
			break
		}
	}

	// Compare heading context
	headingMatch := 0.0
	if anchor.HeadingContext == currentHeading {
		headingMatch = 1.0
	} else if anchor.HeadingContext != "" && currentHeading != "" {
		headingMatch = similarity(anchor.HeadingContext, currentHeading)
	}

	// Compare paragraph index
	paragraphMatch := 0.0
	if anchor.ParagraphIndex >= 0 {
		// Find current paragraph index within the section
		currentParagraphIndex := 0
		sectionStart := 0

		// Find section start
		for i := len(headingPositions) - 1; i >= 0; i-- {
			if headingPositions[i].start < rng.Start {
				sectionStart = headingPositions[i].end + 1
				break
			}
		}

		// Count paragraphs from section start to current position
		for _, boundary := range paragraphBoundaries {
			if boundary >= sectionStart && boundary < rng.Start {
				currentParagraphIndex++
			}
		}
		if currentParagraphIndex > 0 {
			currentParagraphIndex-- // Convert to 0-based
		}

		// Score paragraph position similarity
		diff := abs(currentParagraphIndex - anchor.ParagraphIndex)
		if diff == 0 {
			paragraphMatch = 1.0
		} else if diff <= 2 {
			paragraphMatch = 0.5
		} else {
			paragraphMatch = 0.1
		}
	} else {
		paragraphMatch = 0.5 // Neutral if no paragraph index stored
	}

	// Combine heading (70%) and paragraph (30%) matches
	return 0.7*headingMatch + 0.3*paragraphMatch
}

// abs returns the absolute value of an integer.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
