package textanchor

import (
	"strings"
	"testing"
)

// TestFuzzyPhaseIsBounded guards the comparison budget.
//
// Removing the whole-paragraph pre-gate made bestSubstringMatch reachable for
// every resolve that misses the exact phase, so the budget in fuzzy.go is now
// the only thing standing between a large document and an unbounded
// O(windows x levenshtein) scan.
//
// The assertion is on the number of similarity calls, not on elapsed time: wall
// clock measures the machine and the build mode as much as the algorithm, and
// the -race build alone is slow enough to trip a threshold the ordinary build
// clears comfortably.
func TestFuzzyPhaseIsBounded(t *testing.T) {
	tests := []struct {
		name    string
		textLen int
		query   int
	}{
		{name: "short quote, large paragraph", textLen: 60000, query: 35},
		{name: "long quote, large paragraph", textLen: 60000, query: 76},
		{name: "long quote, huge paragraph", textLen: 1 << 20, query: 76},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minSize := tt.query - tt.query/4
			maxSize := tt.query + tt.query/4

			total := 0
			for size := minSize; size <= maxSize; size++ {
				if size <= 0 || size > tt.textLen {
					continue
				}
				step := windowStride(tt.textLen, size, minSize, maxSize)
				total += (tt.textLen-size)/step + 1
			}

			// The budget is shared across sizes, so integer division can leave a
			// little slack; allow one extra window per size rather than pinning
			// an exact figure.
			limit := fuzzyMaxComparisons + (maxSize - minSize + 1)
			if total > limit {
				t.Errorf("window count = %d over %d bytes, want <= %d", total, tt.textLen, limit)
			}
			t.Logf("%d bytes, %d-byte query: %d windows", tt.textLen, tt.query, total)
		})
	}
}

// TestFuzzyPhaseCoversWholeParagraph is the counterweight to the budget: the
// stride must keep the tail of a long paragraph reachable. A cap that truncated
// instead of striding would pass the budget test above while quietly making a
// quote near the end of a long paragraph unfindable.
func TestFuzzyPhaseCoversWholeParagraph(t *testing.T) {
	filler := strings.Repeat("the store writes markdown files to disk and reloads them on change ", 300)
	quote := "the audit log records every successful write to the graph"
	doc := filler + quote + "\n"

	anchor := Anchor{Quote: quote, ParagraphIndex: -1}
	res := Resolve(doc, anchor, nil)

	if res.Orphaned {
		t.Fatalf("Resolve() orphaned (%s) for a quote at the end of a %d-byte paragraph",
			res.OrphanReason, len(doc))
	}
	if got := doc[res.Range.Start:res.Range.End]; got != quote {
		t.Errorf("resolved to %q, want %q", got, quote)
	}
}

// TestFuzzyPhaseOrphansAbsentQuote checks the worst case end to end: an absent
// quote spends the entire budget without matching, and must still orphan.
func TestFuzzyPhaseOrphansAbsentQuote(t *testing.T) {
	para := strings.Repeat("the store writes markdown files to disk and reloads them on change ", 900)
	doc := para + "\n"

	anchor := Anchor{
		Quote:          "a completely different sentence that appears nowhere in this document at all",
		ParagraphIndex: -1,
	}

	res := Resolve(doc, anchor, nil)
	if !res.Orphaned {
		t.Errorf("Resolve() found %q for an absent quote, want orphaned", doc[res.Range.Start:res.Range.End])
	}
}

// BenchmarkResolveWrapped measures the common path: a quote that resolves
// through the exact phase after whitespace collapse.
func BenchmarkResolveWrapped(b *testing.B) {
	quote := "until a restart, which is confusing"
	start := strings.Index(reflowRaw, quote)
	anchor, err := New(reflowRaw, start, start+len(quote), nil)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Resolve(reflowWrapped, anchor, nil)
	}
}

// BenchmarkResolveFuzzyWorstCase measures the bounded fuzzy path: an absent
// quote over a large paragraph, where the full comparison budget is spent.
func BenchmarkResolveFuzzyWorstCase(b *testing.B) {
	para := strings.Repeat("the store writes markdown files to disk and reloads them on change ", 900)
	doc := para + "\n"
	anchor := Anchor{
		Quote:          "a completely different sentence that appears nowhere in this document at all",
		ParagraphIndex: -1,
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Resolve(doc, anchor, nil)
	}
}
