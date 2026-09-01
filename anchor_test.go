package textanchor

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	doc := `# Getting Started

This is a test document. It has multiple paragraphs.

You can install the package using npm or yarn.

## Configuration

Configure the package by creating a config file.
`

	// Find actual positions
	thisStart := strings.Index(doc, "This is a test")
	thisEnd := thisStart + 4 // "This"

	installStart := strings.Index(doc, "install the package")
	installEnd := installStart + len("install the package")

	tests := []struct {
		name      string
		start     int
		end       int
		wantQuote string
		wantErr   error
	}{
		{
			name:      "simple word",
			start:     thisStart,
			end:       thisEnd,
			wantQuote: "This",
			wantErr:   nil,
		},
		{
			name:      "phrase",
			start:     installStart,
			end:       installEnd,
			wantQuote: "install the package",
			wantErr:   nil,
		},
		{
			name:    "invalid range - start >= end",
			start:   10,
			end:     10,
			wantErr: ErrInvalidRange,
		},
		{
			name:    "invalid range - start > end",
			start:   20,
			end:     10,
			wantErr: ErrInvalidRange,
		},
		{
			name:    "out of bounds - negative start",
			start:   -1,
			end:     10,
			wantErr: ErrOutOfBounds,
		},
		{
			name:    "out of bounds - end too large",
			start:   0,
			end:     10000,
			wantErr: ErrOutOfBounds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anchor, err := New(doc, tt.start, tt.end, nil)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("New() unexpected error = %v", err)
				return
			}

			if anchor.Quote != tt.wantQuote {
				t.Errorf("New() Quote = %q, want %q", anchor.Quote, tt.wantQuote)
			}
		})
	}
}

func TestNewWithOptions(t *testing.T) {
	doc := `# Hello World

This is a sentence with some important text in it.

Another paragraph here.
`

	// Find "important text"
	start := strings.Index(doc, "important text")
	end := start + len("important text")

	opts := &CreateOptions{
		PrefixLen:                 20,
		SuffixLen:                 20,
		IncludeContainingSentence: true,
		IncludeStructuralContext:  true,
	}

	anchor, err := New(doc, start, end, opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if anchor.Quote != "important text" {
		t.Errorf("Quote = %q, want %q", anchor.Quote, "important text")
	}

	if len(anchor.Prefix) > 20 {
		t.Errorf("Prefix too long: %d > 20", len(anchor.Prefix))
	}

	if len(anchor.Suffix) > 20 {
		t.Errorf("Suffix too long: %d > 20", len(anchor.Suffix))
	}

	if anchor.HeadingContext != "Hello World" {
		t.Errorf("HeadingContext = %q, want %q", anchor.HeadingContext, "Hello World")
	}
}

func TestNewExtractsContainingSentence(t *testing.T) {
	doc := `First sentence here. This is the target word in context. Final sentence.`

	// Find "target"
	start := strings.Index(doc, "target")
	end := start + len("target")

	anchor, err := New(doc, start, end, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if anchor.Quote != "target" {
		t.Errorf("Quote = %q, want %q", anchor.Quote, "target")
	}

	expected := "This is the target word in context."
	if anchor.ContainingSentence != expected {
		t.Errorf("ContainingSentence = %q, want %q", anchor.ContainingSentence, expected)
	}
}

func TestResolve(t *testing.T) {
	doc := `# Getting Started

This is a test document. It has multiple paragraphs.

You can install the package using npm or yarn.

## Configuration

Configure the package by creating a config file.
`

	// Create anchor for "install the package"
	start := strings.Index(doc, "install the package")
	end := start + len("install the package")

	anchor, err := New(doc, start, end, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Resolve in the same document
	result := Resolve(doc, anchor, nil)

	if result.Orphaned {
		t.Errorf("Resolve() orphaned = true, want false; reason: %s", result.OrphanReason)
	}

	if result.Range == nil {
		t.Fatal("Resolve() Range is nil")
	}

	if result.Range.Start != start || result.Range.End != end {
		t.Errorf("Resolve() Range = %d-%d, want %d-%d", result.Range.Start, result.Range.End, start, end)
	}

	if result.Confidence < 0.9 {
		t.Errorf("Resolve() Confidence = %f, want >= 0.9", result.Confidence)
	}
}

func TestResolveAfterEdit(t *testing.T) {
	originalDoc := `# Getting Started

This is a test document. It has multiple paragraphs.

You can install the package using npm or yarn.

## Configuration

Configure the package by creating a config file.
`

	// Create anchor for "install the package"
	start := strings.Index(originalDoc, "install the package")
	end := start + len("install the package")

	anchor, err := New(originalDoc, start, end, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Modified document with text added before the anchor
	modifiedDoc := `# Getting Started

Welcome to the guide!

This is a test document. It has multiple paragraphs.

You can install the package using npm or yarn.

## Configuration

Configure the package by creating a config file.
`

	result := Resolve(modifiedDoc, anchor, nil)

	if result.Orphaned {
		t.Errorf("Resolve() orphaned = true after edit, want false; reason: %s", result.OrphanReason)
	}

	if result.Range == nil {
		t.Fatal("Resolve() Range is nil")
	}

	// Verify the resolved text is correct
	resolved := modifiedDoc[result.Range.Start:result.Range.End]
	if resolved != "install the package" {
		t.Errorf("Resolved text = %q, want %q", resolved, "install the package")
	}
}

func TestResolveWithMultipleOccurrences(t *testing.T) {
	doc := `# First Section

The word test appears here.

# Second Section

The word test appears here too.

# Third Section

Something else entirely.
`

	// Find the second occurrence of "test" (in Second Section)
	firstTest := strings.Index(doc, "test")
	secondTestOffset := strings.Index(doc[firstTest+1:], "test")
	start := firstTest + 1 + secondTestOffset
	end := start + len("test")

	anchor, err := New(doc, start, end, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if anchor.HeadingContext != "Second Section" {
		t.Errorf("HeadingContext = %q, want %q", anchor.HeadingContext, "Second Section")
	}

	// Resolve should find the correct occurrence based on context
	result := Resolve(doc, anchor, nil)

	if result.Orphaned {
		t.Errorf("Resolve() orphaned = true, want false; reason: %s", result.OrphanReason)
	}

	if result.Range == nil {
		t.Fatal("Resolve() Range is nil")
	}

	// Verify it found the second occurrence
	resolved := doc[result.Range.Start:result.Range.End]
	if resolved != "test" {
		t.Errorf("Resolved text = %q, want %q", resolved, "test")
	}
}

func TestResolveOrphaned(t *testing.T) {
	doc := `# Hello

This document has some content.
`

	anchor := Anchor{
		Quote:          "nonexistent text that does not appear",
		Prefix:         "some prefix",
		Suffix:         "some suffix",
		HeadingContext: "Hello",
	}

	result := Resolve(doc, anchor, nil)

	if !result.Orphaned {
		t.Errorf("Resolve() orphaned = false, want true")
	}

	if result.OrphanReason == "" {
		t.Error("Resolve() OrphanReason is empty")
	}
}

func TestResolveAll(t *testing.T) {
	doc := `# Document

First paragraph with word one.

Second paragraph with word two.

Third paragraph with word three.
`

	// Find positions for "one", "two", "three"
	oneStart := strings.Index(doc, "one")
	twoStart := strings.Index(doc, "two")
	threeStart := strings.Index(doc, "three")

	positions := []struct {
		start, end int
		quote      string
	}{
		{oneStart, oneStart + 3, "one"},
		{twoStart, twoStart + 3, "two"},
		{threeStart, threeStart + 5, "three"},
	}

	anchors := make([]Anchor, 3)
	for i, pos := range positions {
		var err error
		anchors[i], err = New(doc, pos.start, pos.end, nil)
		if err != nil {
			t.Fatalf("New() error for %q: %v", pos.quote, err)
		}
	}

	results := ResolveAll(doc, anchors, nil)

	if len(results) != 3 {
		t.Fatalf("ResolveAll() returned %d results, want 3", len(results))
	}

	for i, result := range results {
		if result.Orphaned {
			t.Errorf("Result[%d] orphaned = true, want false; reason: %s", i, result.OrphanReason)
		}
		if result.Range == nil {
			t.Errorf("Result[%d] Range is nil", i)
		}
	}
}

func TestExtractHeadingContext(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		offset   int
		expected string
	}{
		{
			name:     "simple heading",
			doc:      "# Hello\n\nSome text",
			offset:   15,
			expected: "Hello",
		},
		{
			name:     "nested heading",
			doc:      "# Main\n\n## Sub\n\nContent",
			offset:   20,
			expected: "Sub",
		},
		{
			name:     "no heading",
			doc:      "Just some text without heading",
			offset:   10,
			expected: "",
		},
		{
			name:     "multiple hashes",
			doc:      "### Third Level\n\nContent",
			offset:   20,
			expected: "Third Level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractHeadingContext(tt.doc, tt.offset)
			if result != tt.expected {
				t.Errorf("extractHeadingContext() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSplitParagraphs(t *testing.T) {
	text := `First paragraph.

Second paragraph.
Still second.

Third paragraph.`

	paragraphs := splitParagraphs(text)

	if len(paragraphs) != 3 {
		t.Errorf("splitParagraphs() returned %d paragraphs, want 3", len(paragraphs))
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		a, b     string
		minScore float64
	}{
		{"hello", "hello", 1.0},
		{"hello", "hallo", 0.7},
		{"", "", 1.0},
		{"abc", "", 0.0},
	}

	for _, tt := range tests {
		score := similarity(tt.a, tt.b)
		if score < tt.minScore {
			t.Errorf("similarity(%q, %q) = %f, want >= %f", tt.a, tt.b, score, tt.minScore)
		}
	}
}
