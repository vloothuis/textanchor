package quotefind

import (
	"strings"
	"testing"
)

// span returns the range of the first occurrence of from through the end of
// the first occurrence of to after it.
func span(t *testing.T, doc, from, to string) (int, int) {
	t.Helper()
	s := strings.Index(doc, from)
	e := strings.Index(doc[s:], to)
	if s < 0 || e < 0 {
		t.Fatalf("span %q..%q not in document", from, to)
	}
	return s, s + e + len(to)
}

func TestSegments(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		from, to string
		want     []string
	}{
		{
			name: "heading into its body",
			doc:  "## Configuration\n\nConfigure the package by creating a file.\n",
			from: "Configuration", to: "the package",
			want: []string{"Configuration", "Configure the package"},
		},
		{
			name: "single block is one segment",
			doc:  "One paragraph with a phrase\nwrapped over two lines.\n",
			from: "phrase", to: "two lines",
			want: []string{"phrase\nwrapped over two lines"},
		},
		{
			name: "tight list items drop their markers",
			doc:  "- first item\n- second item\n- third item\n",
			from: "item", to: "third",
			want: []string{"item", "second item", "third"},
		},
		{
			name: "task items drop their checkboxes",
			doc:  "- [ ] write tests\n- [x] ship it\n",
			from: "tests", to: "ship",
			want: []string{"tests", "ship"},
		},
		{
			name: "covered task item starting with emphasis keeps it balanced",
			doc:  "Intro text.\n\n- [ ] **bold** rest\n- [x] [link](https://example.com) more\n",
			from: "text.", to: "more",
			want: []string{"text.", "**bold** rest", "[link](https://example.com) more"},
		},
		{
			name: "covered block ending in a backslash leaves it out",
			doc:  "Path is C:\\dir\\\n\nNext paragraph.\n",
			from: "Path", to: "Next",
			want: []string{"Path is C:\\dir", "Next"},
		},
		{
			name: "escaped backslash at the end stays in",
			doc:  "Ends with \\\\\n\nNext paragraph.\n",
			from: "Ends", to: "Next",
			want: []string{"Ends with \\\\", "Next"},
		},
		{
			name: "start inside emphasis moves to its opening delimiter",
			doc:  "Read the **important** text.\n\nNext paragraph.\n",
			from: "portant", to: "Next",
			want: []string{"**important** text.", "Next"},
		},
		{
			name: "start inside link text moves to its opening bracket",
			doc:  "See [the docs](https://example.com) here.\n\nNext paragraph.\n",
			from: "docs", to: "Next",
			want: []string{"[the docs](https://example.com) here.", "Next"},
		},
		{
			name: "start inside nested emphasis moves to the outer delimiter",
			doc:  "A ***very*** **bold *claim*** here.\n\nNext paragraph.\n",
			from: "claim", to: "Next",
			want: []string{"**bold *claim*** here.", "Next"},
		},
		{
			name: "range inside one emphasis is left alone",
			doc:  "Read the **important** text.\n",
			from: "port", to: "ant",
			want: []string{"portant"},
		},
		{
			name: "covered block keeps edge emphasis balanced",
			doc:  "Intro text.\n\n**Bold** start and *end*\n\nOutro text.\n",
			from: "text.", to: "Outro",
			want: []string{"text.", "**Bold** start and *end*", "Outro"},
		},
		{
			name: "covered heading keeps edge emphasis balanced",
			doc:  "Before.\n\n## **Bold** title ##\n\nAfter.\n",
			from: "Before", to: "After",
			want: []string{"Before.", "**Bold** title", "After"},
		},
		{
			name: "fenced code block is skipped",
			doc:  "Before the code.\n\n```go\nx := 1\n```\n\nAfter the code.\n",
			from: "the code.", to: "After",
			want: []string{"the code.", "After"},
		},
		{
			name: "inline code is included whole",
			doc:  "Heading\n=======\n\nRun `make test` before pushing.\n",
			from: "Heading", to: "pushing",
			want: []string{"Heading", "Run `make test` before pushing"},
		},
		{
			name: "double-backtick code span is included whole",
			doc:  "Use `` a`b `` here.\n",
			from: "Use", to: "here",
			want: []string{"Use `` a`b `` here"},
		},
		{
			name: "start inside code widens to its backtick",
			doc:  "Run `make test` now.\n\nNext.\n",
			from: "test", to: "Next",
			want: []string{"`make test` now.", "Next"},
		},
		{
			name: "end inside code widens to its backtick",
			doc:  "Intro.\n\nRun `make test` now.\n",
			from: "Intro", to: "make",
			want: []string{"Intro.", "Run `make test`"},
		},
		{
			name: "range inside one code span marks the span",
			doc:  "Run `make test` now.\n",
			from: "make", to: "make",
			want: []string{"`make test`"},
		},
		{
			name: "table cells are separate",
			doc:  "| a | b |\n|---|---|\n| one | two |\n",
			from: "one", to: "two",
			want: []string{"one", "two"},
		},
		{
			name: "blockquote paragraph is one segment",
			doc:  "Intro.\n\n> quote line\n> second line\n",
			from: "Intro", to: "second",
			want: []string{"Intro.", "quote line\n> second"},
		},
		{
			name: "html block is skipped",
			doc:  "Before.\n\n<div>\nraw\n</div>\n\nAfter.\n",
			from: "Before", to: "After",
			want: []string{"Before.", "After"},
		},
		{
			name: "multibyte text",
			doc:  "# Überschrift\n\nÄpfel und Birnen.\n",
			from: "Überschrift", to: "und",
			want: []string{"Überschrift", "Äpfel und"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, e := span(t, tc.doc, tc.from, tc.to)
			var got []string
			for _, r := range Segments(tc.doc, s, e) {
				got = append(got, tc.doc[r.Start:r.End])
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("segments = %q\nwant      %q", got, tc.want)
			}
		})
	}
}

func TestSegmentsDegenerate(t *testing.T) {
	const doc = "# Title\n\nBody.\n"
	tests := []struct {
		name       string
		start, end int
	}{
		{"empty range", 3, 3},
		{"inverted range", 8, 3},
		{"only markup", 0, 2},
		{"only blank line", 7, 9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Segments(doc, tc.start, tc.end); got != nil {
				t.Errorf("Segments(%d, %d) = %v, want nil", tc.start, tc.end, got)
			}
		})
	}

	// Out-of-range offsets clamp rather than panic.
	if got := Segments(doc, -5, len(doc)+10); len(got) != 2 {
		t.Errorf("clamped range: got %v, want two segments", got)
	}
	if got := Segments("", 0, 0); got != nil {
		t.Errorf("empty document: got %v", got)
	}
}

func TestFindQuoteAcrossTableCells(t *testing.T) {
	const doc = "| name | value |\n|------|-------|\n| alpha | beta |\n"
	start, end, ok := Find(doc, "alpha beta")
	if !ok {
		t.Fatal("quote across table cells not found")
	}
	if got := doc[start:end]; got != "alpha | beta" {
		t.Errorf("found %q, want %q", got, "alpha | beta")
	}
}
