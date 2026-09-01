// Package textanchor creates and resolves durable references to spans of text
// in Markdown documents.
//
// An anchor describes a span by its content and surroundings rather than by a
// byte offset, so it can be relocated after the document has been edited. Each
// anchor stores the quoted text, ~50 characters of prefix and suffix context,
// the containing sentence (for short quotes), and structural context (the
// nearest preceding heading and the paragraph index within that section).
//
// Resolution finds candidate matches, scores each by context similarity, and
// returns the best one along with a confidence score. When no candidate scores
// above the threshold the anchor is reported as orphaned rather than guessed at
// — callers are expected to surface orphans to the user, not to discard them.
//
// Typical use:
//
//	a, err := textanchor.New(document, start, end, nil)
//	// ... persist a ...
//	res := textanchor.Resolve(editedDocument, a, nil)
//	if res.Orphaned {
//		// show the anchor's Quote as context; do not silently drop it
//	}
//
// Use [ResolveAll] to resolve many anchors against one document; it hoists the
// per-document structural analysis out of the per-anchor loop.
//
// Anchors index the document by BYTE offset, matching Go's string indexing.
//
// The subpackage quotefind maps a selection made over RENDERED markdown back to
// offsets in the markdown source, which is what a browser text selection needs
// before an anchor can be created from it.
package textanchor

import (
	"errors"
	"strings"
	"unicode"
)

// Sentinel errors for the anchor package.
var (
	ErrInvalidRange = errors.New("invalid range: start must be less than end")
	ErrOutOfBounds  = errors.New("range out of bounds")
	ErrEmptyQuote   = errors.New("quote cannot be empty")
)

// Anchor represents a persistent reference to a span of text in a document.
// It stores enough context to relocate the text even after the document
// has been edited.
type Anchor struct {
	// Quote is the exact text that was selected/annotated.
	Quote string `toml:"quote" json:"quote"`

	// Prefix is approximately 50 characters of text immediately before
	// the quote. Used for disambiguation when the quote appears multiple
	// times in the document.
	Prefix string `toml:"prefix" json:"prefix"`

	// Suffix is approximately 50 characters of text immediately after
	// the quote. Used for disambiguation.
	Suffix string `toml:"suffix" json:"suffix"`

	// ContainingSentence holds the full sentence containing the quote,
	// useful for word/phrase-level annotations where the quote itself
	// is very short.
	ContainingSentence string `toml:"containing_sentence,omitempty" json:"containing_sentence,omitempty"`

	// HeadingContext is the text of the nearest preceding heading,
	// providing structural context for resolution.
	HeadingContext string `toml:"heading_context,omitempty" json:"heading_context,omitempty"`

	// ParagraphIndex is the 0-based index of the paragraph within the
	// section defined by HeadingContext. -1 if not applicable.
	ParagraphIndex int `toml:"paragraph_index,omitempty" json:"paragraph_index,omitempty"`
}

// Range represents a character range in a document.
type Range struct {
	Start int // Inclusive, 0-based byte offset
	End   int // Exclusive, 0-based byte offset
}

// ResolveResult contains the result of attempting to resolve an anchor.
type ResolveResult struct {
	// Range is the located character range. Nil if orphaned.
	Range *Range

	// Confidence is a score from 0.0 to 1.0 indicating how confident
	// we are in this match. 1.0 = exact match with perfect context.
	Confidence float64

	// Orphaned is true if the anchor could not be resolved.
	Orphaned bool

	// OrphanReason explains why resolution failed, if applicable.
	OrphanReason string
}

// CreateOptions configures anchor creation.
type CreateOptions struct {
	// PrefixLen is the number of characters to capture before the quote.
	// Default: 50
	PrefixLen int

	// SuffixLen is the number of characters to capture after the quote.
	// Default: 50
	SuffixLen int

	// IncludeContainingSentence controls whether to extract the full
	// sentence for short quotes. Default: true for quotes < 50 chars.
	IncludeContainingSentence bool

	// IncludeStructuralContext controls whether to extract heading
	// and paragraph position. Default: true
	IncludeStructuralContext bool
}

// ResolveOptions configures anchor resolution.
type ResolveOptions struct {
	// MinConfidence is the minimum confidence score required to consider
	// an anchor resolved. Below this threshold, the anchor is orphaned.
	// Default: 0.5
	MinConfidence float64

	// PreferStructuralMatch gives weight to matches that are in the
	// same structural position (same heading, similar paragraph index).
	// Default: true
	PreferStructuralMatch bool
}

// DefaultCreateOptions returns the default options for anchor creation.
func DefaultCreateOptions() *CreateOptions {
	return &CreateOptions{
		PrefixLen:                 50,
		SuffixLen:                 50,
		IncludeContainingSentence: true,
		IncludeStructuralContext:  true,
	}
}

// DefaultResolveOptions returns the default options for anchor resolution.
func DefaultResolveOptions() *ResolveOptions {
	return &ResolveOptions{
		MinConfidence:         0.5,
		PreferStructuralMatch: true,
	}
}

// New creates an anchor for the given text range in a document.
//
// Parameters:
//   - document: The full text of the Markdown document
//   - start: Start byte offset of the selection (inclusive)
//   - end: End byte offset of the selection (exclusive)
//   - opts: Optional configuration (nil for defaults)
//
// Returns an Anchor that can be serialized and later resolved.
func New(document string, start, end int, opts *CreateOptions) (Anchor, error) {
	if opts == nil {
		opts = DefaultCreateOptions()
	}

	// Validate range
	if start >= end {
		return Anchor{}, ErrInvalidRange
	}
	if start < 0 || end > len(document) {
		return Anchor{}, ErrOutOfBounds
	}

	quote := document[start:end]
	if strings.TrimSpace(quote) == "" {
		return Anchor{}, ErrEmptyQuote
	}

	anchor := Anchor{
		Quote:          quote,
		ParagraphIndex: -1,
	}

	// Extract prefix
	prefixStart := start - opts.PrefixLen
	if prefixStart < 0 {
		prefixStart = 0
	}
	anchor.Prefix = document[prefixStart:start]

	// Extract suffix
	suffixEnd := end + opts.SuffixLen
	if suffixEnd > len(document) {
		suffixEnd = len(document)
	}
	anchor.Suffix = document[end:suffixEnd]

	// Extract containing sentence for short quotes
	if opts.IncludeContainingSentence && len(quote) < 50 {
		anchor.ContainingSentence = extractContainingSentence(document, start, end)
	}

	// Extract structural context
	if opts.IncludeStructuralContext {
		anchor.HeadingContext = extractHeadingContext(document, start)
		anchor.ParagraphIndex = extractParagraphIndex(document, start, anchor.HeadingContext)
	}

	return anchor, nil
}

// extractHeadingContext finds the nearest preceding ATX heading.
func extractHeadingContext(doc string, offset int) string {
	lines := strings.Split(doc[:offset], "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "#") {
			// Strip leading #s and whitespace
			heading := strings.TrimLeft(line, "#")
			return strings.TrimSpace(heading)
		}
	}
	return ""
}

// extractParagraphIndex finds the 0-based index of the paragraph within the section.
func extractParagraphIndex(doc string, offset int, headingContext string) int {
	// Find the start of the current section
	sectionStart := 0
	if headingContext != "" {
		lines := strings.Split(doc[:offset], "\n")
		lineOffset := 0
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
				if heading == headingContext {
					// Start after this heading line
					sectionStart = lineOffset + len(line) + 1
					if sectionStart > offset {
						sectionStart = 0
					}
				}
			}
			lineOffset += len(line) + 1
			if i < len(lines)-1 && lineOffset > offset {
				break
			}
		}
	}

	// Count paragraphs from section start to offset
	text := doc[sectionStart:offset]
	paragraphs := splitParagraphs(text)

	// The last paragraph is the one containing our offset
	if len(paragraphs) == 0 {
		return 0
	}
	return len(paragraphs) - 1
}

// splitParagraphs splits text into paragraphs (blank-line separated).
func splitParagraphs(text string) []string {
	var paragraphs []string
	var current strings.Builder

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if current.Len() > 0 {
				paragraphs = append(paragraphs, current.String())
				current.Reset()
			}
		} else {
			if current.Len() > 0 {
				current.WriteString("\n")
			}
			current.WriteString(line)
		}
	}

	if current.Len() > 0 {
		paragraphs = append(paragraphs, current.String())
	}

	return paragraphs
}

// extractContainingSentence extracts the full sentence containing the selection.
func extractContainingSentence(doc string, start, end int) string {
	// Find sentence start (look backwards for sentence terminators)
	sentenceStart := 0
	for i := start - 1; i >= 0; i-- {
		if isSentenceEnd(doc, i) {
			sentenceStart = i + 1
			// Skip whitespace after sentence terminator
			for sentenceStart < start && unicode.IsSpace(rune(doc[sentenceStart])) {
				sentenceStart++
			}
			break
		}
	}

	// Find sentence end (look forwards for sentence terminators)
	sentenceEnd := len(doc)
	for i := end; i < len(doc); i++ {
		if isSentenceEnd(doc, i) {
			sentenceEnd = i + 1
			break
		}
	}

	sentence := strings.TrimSpace(doc[sentenceStart:sentenceEnd])
	return sentence
}

// isSentenceEnd checks if the character at pos is a sentence terminator.
func isSentenceEnd(doc string, pos int) bool {
	if pos < 0 || pos >= len(doc) {
		return false
	}
	c := doc[pos]
	if c == '.' || c == '?' || c == '!' {
		// Check if followed by space or newline or end of document
		if pos+1 >= len(doc) {
			return true
		}
		next := doc[pos+1]
		return next == ' ' || next == '\n' || next == '\t'
	}
	return false
}
