package textanchor

import (
	"strings"
	"testing"
)

// crossDoc is a document with a heading followed by its body, the selection
// the cross-block phase exists for, plus neighbouring sections.
const crossDoc = `# Guide

Some introduction that nobody comments on.

## Configuration

Configure the package by creating a config file in the project root. The file
is read once at startup, so restart after changing it.

## Deployment

Deploy with the provided script. It builds, tests and uploads the release.
`

// newCrossAnchor builds an anchor for the span from the first occurrence of
// from to the end of the first occurrence of to after it, the way a caller
// would after mapping a selection to source offsets.
func newCrossAnchor(t *testing.T, doc, from, to string) Anchor {
	t.Helper()
	start := strings.Index(doc, from)
	if start < 0 {
		t.Fatalf("%q not in document", from)
	}
	end := strings.Index(doc[start:], to)
	if end < 0 {
		t.Fatalf("%q not after %q", to, from)
	}
	a, err := New(doc, start, start+end+len(to), nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestResolveCrossBlockSurvivesEdits(t *testing.T) {
	anchor := newCrossAnchor(t, crossDoc, "Configuration\n", "config file")

	tests := []struct {
		name string
		edit func(string) string
		want string // the span the resolved range must cover
	}{
		{
			name: "unchanged",
			edit: func(s string) string { return s },
			want: "Configuration\n\nConfigure the package by creating a config file",
		},
		{
			name: "typo in the heading",
			edit: func(s string) string { return strings.Replace(s, "## Configuration", "## Configuraton", 1) },
			want: "Configuraton\n\nConfigure the package by creating a config file",
		},
		{
			name: "word changed in the body",
			edit: func(s string) string { return strings.Replace(s, "by creating", "by writing", 1) },
			want: "Configuration\n\nConfigure the package by writing a config file",
		},
		{
			name: "paragraph inserted between heading and body",
			edit: func(s string) string {
				return strings.Replace(s, "## Configuration\n\n", "## Configuration\n\nA short note.\n\n", 1)
			},
			want: "Configuration\n\nA short note.\n\nConfigure the package by creating a config file",
		},
		{
			name: "section moved below another",
			edit: func(s string) string {
				i := strings.Index(s, "## Configuration")
				j := strings.Index(s, "## Deployment")
				return s[:i] + s[j:] + "\n" + s[i:j]
			},
			want: "Configuration\n\nConfigure the package by creating a config file",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.edit(crossDoc)
			res := Resolve(doc, anchor, nil)
			if res.Orphaned {
				t.Fatalf("orphaned (%s, confidence %.2f)", res.OrphanReason, res.Confidence)
			}
			if got := doc[res.Range.Start:res.Range.End]; got != tc.want {
				t.Errorf("resolved to %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestResolveCrossBlockThreeParagraphs(t *testing.T) {
	const doc = "First paragraph talks about apples and pears.\n\n" +
		"Second paragraph is about the orchard and its keeper.\n\n" +
		"Third paragraph closes with the harvest festival.\n"
	anchor := newCrossAnchor(t, doc, "apples", "the harvest")

	edited := strings.Replace(doc, "the orchard", "the old orchard", 1)
	edited = strings.Replace(edited, "closes with", "ends with", 1)
	res := Resolve(edited, anchor, nil)
	if res.Orphaned {
		t.Fatalf("orphaned (%s, confidence %.2f)", res.OrphanReason, res.Confidence)
	}
	got := edited[res.Range.Start:res.Range.End]
	if !strings.HasPrefix(got, "apples") || !strings.HasSuffix(got, "the harvest") {
		t.Errorf("resolved to %q", got)
	}
}

func TestResolveCrossBlockLooseList(t *testing.T) {
	const doc = "Steps:\n\n- Install the toolchain\n\n- Configure the build\n\n- Run the tests\n"
	anchor := newCrossAnchor(t, doc, "the toolchain", "Run the")

	edited := strings.Replace(doc, "Configure the build", "Configure the whole build", 1)
	edited = strings.Replace(edited, "Install the", "Install all of the", 1)
	res := Resolve(edited, anchor, nil)
	if res.Orphaned {
		t.Fatalf("orphaned (%s, confidence %.2f)", res.OrphanReason, res.Confidence)
	}
	if got, want := edited[res.Range.Start:res.Range.End],
		"the toolchain\n\n- Configure the whole build\n\n- Run the"; got != want {
		t.Errorf("resolved to %q\nwant %q", got, want)
	}
}

// A phase-2 candidate covering only the body must not beat the full span.
// The body here is long, so on its own it scores well above the fuzzy floor
// against the whole quote; only the full-span candidate also matches the
// stored prefix.
func TestResolveCrossBlockBeatsBodyOnlyMatch(t *testing.T) {
	body := strings.Repeat("The service reads its settings from the environment. ", 6)
	doc := "Intro text before the section.\n\n## Setup\n\n" + body + "\n\nAfter.\n"
	anchor := newCrossAnchor(t, doc, "Setup", strings.TrimSpace(body))

	edited := strings.Replace(doc, "reads its settings", "loads its settings", 1)
	res := Resolve(edited, anchor, nil)
	if res.Orphaned {
		t.Fatalf("orphaned (%s, confidence %.2f)", res.OrphanReason, res.Confidence)
	}
	if got := edited[res.Range.Start:res.Range.End]; !strings.HasPrefix(got, "Setup\n\n") {
		t.Errorf("resolved to a span without the heading: %q", got)
	}
}

func TestResolveCrossBlockOrphans(t *testing.T) {
	anchor := newCrossAnchor(t, crossDoc, "Configuration\n", "config file")

	tests := []struct {
		name string
		edit func(string) string
	}{
		{
			name: "section deleted",
			edit: func(s string) string {
				i := strings.Index(s, "## Configuration")
				j := strings.Index(s, "## Deployment")
				return s[:i] + s[j:]
			},
		},
		{
			// The surrounding text is intact, which alone is worth up to 0.6
			// of the score, and the heading endpoint matches exactly. The
			// similarity floor must still orphan it.
			// The heading and the text on both sides are intact; only the
			// quoted body sentence is rewritten, to the same length so the
			// end lands exactly where the suffix still matches. Without the
			// similarity floor this resolves at 0.53.
			name: "quoted body rewritten, context intact",
			edit: func(s string) string {
				return strings.Replace(s, "Configure the package by creating a config file",
					"Nothing written here applies to Windows machine", 1)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.edit(crossDoc)
			res := Resolve(doc, anchor, nil)
			if !res.Orphaned {
				t.Errorf("resolved to %q at %.2f; want orphaned",
					doc[res.Range.Start:res.Range.End], res.Confidence)
			}
		})
	}
}

// Two identical sections: the stored prefix decides. Heading context cannot,
// because a selection starting in a heading stores none.
func TestResolveCrossBlockRepeatedSections(t *testing.T) {
	const section = "## Notes\n\nCheck the logs before you restart anything.\n\n"
	doc := "Alpha section intro.\n\n" + section + "Beta section intro.\n\n" + section
	second := strings.LastIndex(doc, "Notes")
	end := strings.LastIndex(doc, "the logs") + len("the logs")
	anchor, err := New(doc, second, end, nil)
	if err != nil {
		t.Fatal(err)
	}

	edited := strings.ReplaceAll(doc, "Check the logs", "Check all the logs")
	res := Resolve(edited, anchor, nil)
	if res.Orphaned {
		t.Fatalf("orphaned (%s, confidence %.2f)", res.OrphanReason, res.Confidence)
	}
	if res.Range.Start < strings.Index(edited, "Beta") {
		t.Errorf("resolved to the first section; want the second")
	}
}

// Library callers may pass a quote with no paragraph structure at all; phase 3
// must leave it alone.
func TestFindCrossBlockSingleChunkQuote(t *testing.T) {
	if got := findCrossBlock(NewDocument(crossDoc), "Configuration Configure the package"); got != nil {
		t.Errorf("got %d candidates for a single-chunk quote; want none", len(got))
	}
}

func TestFindCrossBlockTooManyChunks(t *testing.T) {
	quote := strings.Repeat("para\n\n", crossBlockMaxChunks+1)
	if got := findCrossBlock(NewDocument(quote), quote); got != nil {
		t.Errorf("got %d candidates; want phase 3 skipped", len(got))
	}
}

func TestAlignerAtStart(t *testing.T) {
	tests := []struct {
		q, text string
		wantN   int
		minSim  float64
	}{
		{"Configure", "Configure the package", 9, 1},
		{"Configure the", "Configur the package", 12, 0.9},
		{"abc", "", 0, 0},
		{"", "abc", 0, 0},
	}
	for _, tc := range tests {
		var al aligner
		n, sim := al.atStart([]rune(tc.q), []rune(tc.text))
		if n != tc.wantN || sim < tc.minSim {
			t.Errorf("atStart(%q, %q) = %d, %.2f; want %d, >= %.2f",
				tc.q, tc.text, n, sim, tc.wantN, tc.minSim)
		}
	}
}

// Phase 3 screens every paragraph of the document, so its work must stay
// linear in the paragraph count and independent of the quote length. The bound
// is on edit-distance cells, not elapsed time, for the reason
// TestFuzzyPhaseIsBounded gives.
func TestCrossBlockIsBounded(t *testing.T) {
	var b strings.Builder
	for b.Len() < 100_000 {
		b.WriteString("## Section heading\n\nA paragraph of filler text about nothing in particular, ")
		b.WriteString("long enough to look like real prose in a real document.\n\n")
	}
	d := NewDocument(b.String())

	long := strings.Repeat("a long endpoint sentence that keeps going ", 50)
	quotes := map[string]string{
		"short endpoints": "Configuration\n\nConfigure the package by creating a config file",
		"long endpoints":  long + "\n\nmiddle\n\n" + long,
	}
	for name, quote := range quotes {
		t.Run(name, func(t *testing.T) {
			var al aligner
			findCrossBlockWith(d, quote, &al)

			probeWindow := alignWindow(make([]rune, crossBlockProbeRunes))
			endpointWindow := alignWindow(make([]rune, crossBlockEndpointRunes))
			screening := len(d.chunks) * 2 * crossBlockProbeRunes * probeWindow
			evaluation := crossBlockShortlist * 2 * crossBlockEndpointRunes * endpointWindow
			if limit := screening + evaluation; al.cells > limit {
				t.Errorf("%d cells over %d paragraphs, want <= %d", al.cells, len(d.chunks), limit)
			}
			t.Logf("%d paragraphs: %d cells", len(d.chunks), al.cells)
		})
	}
}

func BenchmarkResolveCrossBlock(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 100_000 {
		sb.WriteString("## Section heading\n\nA paragraph of filler text about nothing in particular.\n\n")
	}
	doc := sb.String() + crossDoc
	anchor := newCrossAnchorB(b, doc, "Configuration\n", "config file")
	edited := strings.Replace(doc, "by creating", "by writing", 1)
	d := NewDocument(edited)

	b.ResetTimer()
	for b.Loop() {
		d.Resolve(anchor, nil)
	}
}

func newCrossAnchorB(b *testing.B, doc, from, to string) Anchor {
	b.Helper()
	start := strings.Index(doc, from)
	end := strings.Index(doc[start:], to)
	a, err := New(doc, start, start+end+len(to), nil)
	if err != nil {
		b.Fatal(err)
	}
	return a
}
