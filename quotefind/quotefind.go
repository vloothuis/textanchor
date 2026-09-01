// Package quotefind locates text that was selected over RENDERED markdown
// within the markdown SOURCE.
//
// A browser text selection yields the rendered text — without markdown syntax,
// with whitespace collapsed, and possibly spanning several block elements. Those
// offsets do not correspond to positions in the source. This package bridges the
// two by walking the goldmark AST and building a per-byte map from rendered text
// back to source offsets, so a selection can be converted into source offsets
// suitable for [github.com/vloothuis/textanchor.New].
//
// [Find] handles the common case. [FindWithContext] additionally takes prefix
// and suffix context to disambiguate a quote that occurs more than once.
// [RenderedTextWithMapping] exposes the underlying rendered text and position
// map for callers that need to do their own matching.
package quotefind

import (
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Find finds a quote in the document content, using fuzzy matching
// if an exact match is not found. Returns the start and end byte offsets.
// This handles cases where:
// - The browser's selection includes rendered text without markdown formatting
// - Whitespace differs between HTML rendering and markdown source
// - Text spans multiple block elements (headings, paragraphs, list items)
func Find(content, quote string) (start, end int, found bool) {
	return FindWithContext(content, quote, "", "")
}

// FindWithContext finds a quote with optional prefix/suffix context
// for disambiguation when the quote appears multiple times in the document.
func FindWithContext(content, quote, prefix, suffix string) (start, end int, found bool) {
	// Fast path: exact match with single occurrence
	if idx := strings.Index(content, quote); idx != -1 {
		// If there's only one occurrence, return it
		nextIdx := strings.Index(content[idx+1:], quote)
		if nextIdx == -1 {
			return idx, idx + len(quote), true
		}

		// Multiple occurrences - need to use context disambiguation
		// Fall through to rendered text matching which handles context properly
	}
	// No exact match or multiple occurrences - fall through to fuzzy matching

	// Use AST-based extraction for fuzzy matching
	renderedText, posMap := RenderedTextWithMapping(content)
	normalizedQuote := normalizeRenderedText(quote)
	normalizedPrefix := normalizeRenderedText(prefix)
	normalizedSuffix := normalizeRenderedText(suffix)

	if normalizedQuote == "" {
		return -1, -1, false
	}

	// Find all occurrences in rendered text and score by context
	var matches []struct {
		idx   int
		score float64
	}

	offset := 0
	for {
		idx := strings.Index(renderedText[offset:], normalizedQuote)
		if idx == -1 {
			break
		}
		idx += offset
		score := scoreMatchByContext(renderedText, idx, len(normalizedQuote), normalizedPrefix, normalizedSuffix)
		matches = append(matches, struct {
			idx   int
			score float64
		}{idx, score})
		offset = idx + 1
	}

	if len(matches) == 0 {
		// Try with aggressive whitespace normalization
		aggressiveRendered := collapseAllWhitespace(renderedText)
		aggressiveQuote := collapseAllWhitespace(normalizedQuote)

		idx := strings.Index(aggressiveRendered, aggressiveQuote)
		if idx == -1 {
			return -1, -1, false
		}

		// For aggressive matching, we need to rebuild the position map
		aggressiveMap := buildAggressiveMap(renderedText, posMap)
		start = mapToSource(aggressiveMap, idx)
		endIdx := idx + len(aggressiveQuote)
		if endIdx > 0 && endIdx <= len(aggressiveMap) {
			end = mapToSource(aggressiveMap, endIdx-1) + 1
		} else {
			end = mapToSource(aggressiveMap, endIdx)
		}
		return start, end, true
	}

	// Find the best match by context score
	bestMatch := matches[0]
	for _, m := range matches[1:] {
		if m.score > bestMatch.score {
			bestMatch = m
		}
	}

	// Map the positions back to source
	start = mapToSource(posMap, bestMatch.idx)
	endIdx := bestMatch.idx + len(normalizedQuote)
	if endIdx > 0 && endIdx <= len(posMap) {
		end = mapToSource(posMap, endIdx-1) + 1
	} else {
		end = mapToSource(posMap, endIdx)
	}

	return start, end, true
}

// scoreMatchByContext calculates how well the context around a match
// matches the expected prefix and suffix.
func scoreMatchByContext(content string, matchStart, matchLen int, prefix, suffix string) float64 {
	score := 0.0

	// Score prefix match
	if prefix != "" {
		prefixStart := matchStart - len(prefix)
		if prefixStart < 0 {
			prefixStart = 0
		}
		actualPrefix := content[prefixStart:matchStart]
		score += contextSimilarity(actualPrefix, prefix)
	} else {
		score += 0.5 // Neutral score if no prefix provided
	}

	// Score suffix match
	if suffix != "" {
		suffixEnd := matchStart + matchLen + len(suffix)
		if suffixEnd > len(content) {
			suffixEnd = len(content)
		}
		actualSuffix := content[matchStart+matchLen : suffixEnd]
		score += contextSimilarity(actualSuffix, suffix)
	} else {
		score += 0.5 // Neutral score if no suffix provided
	}

	return score
}

// contextSimilarity calculates similarity between two context strings.
// Returns a value between 0.0 and 1.0.
func contextSimilarity(actual, expected string) float64 {
	if expected == "" {
		return 0.5
	}
	if actual == "" {
		return 0.0
	}

	// Normalize whitespace for comparison
	actualNorm := collapseAllWhitespace(actual)
	expectedNorm := collapseAllWhitespace(expected)

	// Calculate longest common substring ratio
	lcs := longestCommonSubstring(actualNorm, expectedNorm)
	if len(expectedNorm) == 0 {
		return 0.5
	}

	return float64(lcs) / float64(len(expectedNorm))
}

// longestCommonSubstring returns the length of the longest common substring.
func longestCommonSubstring(s1, s2 string) int {
	if len(s1) == 0 || len(s2) == 0 {
		return 0
	}

	// Use a simple O(n*m) algorithm
	maxLen := 0
	dp := make([][]int, len(s1)+1)
	for i := range dp {
		dp[i] = make([]int, len(s2)+1)
	}

	for i := 1; i <= len(s1); i++ {
		for j := 1; j <= len(s2); j++ {
			if s1[i-1] == s2[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
				if dp[i][j] > maxLen {
					maxLen = dp[i][j]
				}
			}
		}
	}

	return maxLen
}

// RenderedTextWithMapping walks the AST and extracts the "rendered" text
// (what a browser would display), along with a position map back to source bytes.
func RenderedTextWithMapping(content string) (rendered string, posMap []int) {
	source := []byte(content)

	// Parse the markdown
	md := goldmark.New()
	reader := text.NewReader(source)
	doc := md.Parser().Parse(reader)

	var builder strings.Builder
	posMap = make([]int, 0, len(content))

	// Walk the AST and extract text content
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			// Add spacing after certain block elements
			switch node.Kind() {
			case ast.KindParagraph, ast.KindHeading, ast.KindListItem,
				ast.KindFencedCodeBlock, ast.KindCodeBlock, ast.KindBlockquote:
				// Add a space to separate blocks (normalized to single space)
				if builder.Len() > 0 {
					lastByte := builder.String()[builder.Len()-1]
					if lastByte != ' ' && lastByte != '\n' {
						builder.WriteByte(' ')
						// Map to the end of the block
						if len(posMap) > 0 {
							posMap = append(posMap, posMap[len(posMap)-1])
						}
					}
				}
			}
			return ast.WalkContinue, nil
		}

		switch n := node.(type) {
		case *ast.Text:
			// Regular text node - extract with position mapping
			seg := n.Segment
			for i := seg.Start; i < seg.Stop; i++ {
				builder.WriteByte(source[i])
				posMap = append(posMap, i)
			}
			// Handle soft line breaks (treated as space in rendered output)
			if n.SoftLineBreak() {
				builder.WriteByte(' ')
				posMap = append(posMap, seg.Stop)
			}

		case *ast.String:
			// String nodes (e.g., from certain extensions)
			value := n.Value
			// These don't have direct source mapping, use parent's position if available
			lines := node.Lines()
			sourcePos := 0
			if lines.Len() > 0 {
				sourcePos = lines.At(0).Start
			}
			for i := 0; i < len(value); i++ {
				builder.WriteByte(value[i])
				posMap = append(posMap, sourcePos+i)
			}

		case *ast.CodeSpan:
			// Inline code - extract the text content
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				if t, ok := child.(*ast.Text); ok {
					seg := t.Segment
					for i := seg.Start; i < seg.Stop; i++ {
						builder.WriteByte(source[i])
						posMap = append(posMap, i)
					}
				}
			}
			return ast.WalkSkipChildren, nil

		case *ast.FencedCodeBlock, *ast.CodeBlock:
			// Code blocks - extract line content directly
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				line := lines.At(i)
				for j := line.Start; j < line.Stop; j++ {
					builder.WriteByte(source[j])
					posMap = append(posMap, j)
				}
			}
			return ast.WalkSkipChildren, nil

		case *ast.AutoLink:
			// Auto links - extract the URL/email as text
			// AutoLink stores its content in child text nodes, traverse them
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				if t, ok := child.(*ast.Text); ok {
					seg := t.Segment
					for i := seg.Start; i < seg.Stop; i++ {
						builder.WriteByte(source[i])
						posMap = append(posMap, i)
					}
				}
			}
			return ast.WalkSkipChildren, nil

		case *ast.RawHTML:
			// Raw HTML - skip it entirely (not rendered as text)
			return ast.WalkSkipChildren, nil

		case *ast.HTMLBlock:
			// HTML blocks - skip
			return ast.WalkSkipChildren, nil
		}

		return ast.WalkContinue, nil
	})

	// Add final position for end mapping
	if len(posMap) > 0 {
		posMap = append(posMap, posMap[len(posMap)-1]+1)
	} else {
		posMap = append(posMap, 0)
	}

	return builder.String(), posMap
}

// normalizeRenderedText normalizes the quote text to match how the browser
// would render markdown content (collapsing whitespace, etc.)
func normalizeRenderedText(s string) string {
	// The quote from the browser has already stripped markdown syntax,
	// we just need to normalize whitespace
	var builder strings.Builder
	inWhitespace := false

	for _, r := range s {
		if unicode.IsSpace(r) {
			if !inWhitespace && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			inWhitespace = true
		} else {
			builder.WriteRune(r)
			inWhitespace = false
		}
	}

	return strings.TrimSpace(builder.String())
}

// collapseAllWhitespace collapses all whitespace to single spaces.
func collapseAllWhitespace(s string) string {
	words := strings.Fields(s)
	return strings.Join(words, " ")
}

// buildAggressiveMap builds a position map for aggressively normalized text.
func buildAggressiveMap(rendered string, originalMap []int) []int {
	var result []int
	inWhitespace := false

	for i, r := range rendered {
		if unicode.IsSpace(r) {
			if !inWhitespace {
				if i < len(originalMap) {
					result = append(result, originalMap[i])
				}
			}
			inWhitespace = true
		} else {
			if i < len(originalMap) {
				result = append(result, originalMap[i])
			}
			inWhitespace = false
		}
	}

	// Add end position
	if len(originalMap) > 0 {
		result = append(result, originalMap[len(originalMap)-1])
	}

	return result
}

// mapToSource maps a position in the rendered/normalized text back to source.
func mapToSource(posMap []int, pos int) int {
	if pos < 0 {
		return 0
	}
	if pos >= len(posMap) {
		if len(posMap) > 0 {
			return posMap[len(posMap)-1]
		}
		return 0
	}
	return posMap[pos]
}

// normalizeText is kept for backward compatibility with tests.
// It normalizes text by extracting rendered content from markdown.
func normalizeText(s string) string {
	rendered, _ := RenderedTextWithMapping(s)
	return normalizeRenderedText(rendered)
}
