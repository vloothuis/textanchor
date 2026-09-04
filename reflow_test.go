package textanchor

import (
	"strings"
	"testing"
)

// The reflow corpus: one paragraph, stored twice. `reflowRaw` is how an author
// types it; `reflowWrapped` is the same text after a formatter has hard-wrapped
// it at 80 columns. An anchor created against one must resolve against the
// other, because a Markdown formatter runs on save and rewrites the whitespace
// under anchors that were created moments earlier.
const (
	reflowRaw = "Renaming an entity leaves the old id in the search index until a restart, " +
		"which is confusing because the store itself is already correct at that point.\n"

	reflowWrapped = "Renaming an entity leaves the old id in the search index until a restart, which\n" +
		"is confusing because the store itself is already correct at that point.\n"
)

// TestResolveAcrossReflow covers resolution when the document's whitespace no
// longer matches the whitespace the anchor was created against.
//
// The wrap-spanning row is the regression this file exists for: before the fix
// it orphaned at confidence 0.00, because the exact-match phase compared bytes
// (a space is not a newline) and the fuzzy phase gated on whole-paragraph
// similarity, which a 35-character quote inside a 151-character paragraph can
// never clear.
func TestResolveAcrossReflow(t *testing.T) {
	tests := []struct {
		name string
		// source is the document the anchor is created against.
		source string
		// target is the document the anchor is resolved against.
		target string
		quote  string
		// wantOrphaned expects resolution to fail. Quotes that are genuinely
		// gone must keep failing: whitespace tolerance must not become a
		// licence to match deleted text.
		wantOrphaned bool
		// wantConfidence is the lower bound on a successful match.
		wantConfidence float64
		// wantSpan is the substring of target the returned Range must select,
		// in the target's ORIGINAL coordinates — newlines included.
		wantSpan string
	}{
		{
			name:           "quote spanning a line break",
			source:         reflowRaw,
			target:         reflowWrapped,
			quote:          "until a restart, which is confusing",
			wantConfidence: 0.90,
			wantSpan:       "until a restart, which\nis confusing",
		},
		{
			name:           "quote within a single line is unaffected",
			source:         reflowRaw,
			target:         reflowWrapped,
			quote:          "the old id in the search index",
			wantConfidence: 0.90,
			wantSpan:       "the old id in the search index",
		},
		{
			name:           "quote ending exactly at the wrap point",
			source:         reflowRaw,
			target:         reflowWrapped,
			quote:          "until a restart, which",
			wantConfidence: 0.90,
			wantSpan:       "until a restart, which",
		},
		{
			name:           "quote starting immediately after the wrap point",
			source:         reflowRaw,
			target:         reflowWrapped,
			quote:          "is confusing because the store",
			wantConfidence: 0.90,
			wantSpan:       "is confusing because the store",
		},
		{
			name:           "unwrapping resolves as well as wrapping",
			source:         reflowWrapped,
			target:         reflowRaw,
			quote:          "until a restart, which\nis confusing",
			wantConfidence: 0.90,
			wantSpan:       "until a restart, which is confusing",
		},
		{
			name:   "deleted text still orphans",
			source: reflowRaw,
			target: "Renaming an entity is handled by the store.\n" +
				"Nothing here resembles the sentence that was removed.\n",
			quote:        "until a restart, which is confusing",
			wantOrphaned: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(tt.source, tt.quote)
			if start < 0 {
				t.Fatalf("test setup: quote %q not present in source", tt.quote)
			}
			anchor, err := New(tt.source, start, start+len(tt.quote), nil)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			res := Resolve(tt.target, anchor, nil)

			if tt.wantOrphaned {
				if !res.Orphaned {
					t.Fatalf("Resolve() resolved at %.3f to %q, want orphaned",
						res.Confidence, tt.target[res.Range.Start:res.Range.End])
				}
				return
			}

			if res.Orphaned {
				t.Fatalf("Resolve() orphaned (%s) at confidence %.3f, want a match",
					res.OrphanReason, res.Confidence)
			}
			if res.Confidence < tt.wantConfidence {
				t.Errorf("Resolve() confidence = %.3f, want >= %.3f", res.Confidence, tt.wantConfidence)
			}
			// The returned Range must index the ORIGINAL document, not the
			// whitespace-collapsed form used internally for matching.
			got := tt.target[res.Range.Start:res.Range.End]
			if got != tt.wantSpan {
				t.Errorf("target[%d:%d] = %q, want %q", res.Range.Start, res.Range.End, got, tt.wantSpan)
			}
		})
	}
}

// TestResolveAcrossReflowMultiByte verifies that offsets survive the round trip
// through the collapsed form when the document contains multi-byte runes. The
// library indexes bytes, so a position map built per byte is the only thing
// keeping a returned Range from landing mid-rune.
func TestResolveAcrossReflowMultiByte(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		target   string
		quote    string
		wantSpan string
	}{
		{
			name: "accented text spanning a line break",
			source: "Le café est déjà préparé par la première équipe, mais la deuxième équipe " +
				"préfère le thé.\n",
			target: "Le café est déjà préparé par la première équipe, mais la\n" +
				"deuxième équipe préfère le thé.\n",
			quote:    "première équipe, mais la deuxième",
			wantSpan: "première équipe, mais la\ndeuxième",
		},
		{
			name: "CJK text spanning a line break",
			source: "この文書は日本語で書かれています。改行が入っても解決できるはずです。" +
				"それが期待される動作です。\n",
			target: "この文書は日本語で書かれています。改行が入っても\n" +
				"解決できるはずです。それが期待される動作です。\n",
			quote:    "改行が入っても解決できる",
			wantSpan: "改行が入っても\n解決できる",
		},
		{
			name: "emoji adjacent to the wrap point",
			source: "The deploy finished 🎉 and the dashboard turned green again after the " +
				"long incident review.\n",
			target: "The deploy finished 🎉 and the dashboard turned green\n" +
				"again after the long incident review.\n",
			quote:    "🎉 and the dashboard turned green again",
			wantSpan: "🎉 and the dashboard turned green\nagain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(tt.source, tt.quote)
			if start < 0 {
				t.Fatalf("test setup: quote %q not present in source", tt.quote)
			}
			anchor, err := New(tt.source, start, start+len(tt.quote), nil)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			res := Resolve(tt.target, anchor, nil)
			if res.Orphaned {
				t.Fatalf("Resolve() orphaned (%s) at confidence %.3f", res.OrphanReason, res.Confidence)
			}

			got := tt.target[res.Range.Start:res.Range.End]
			if got != tt.wantSpan {
				t.Errorf("target[%d:%d] = %q, want %q", res.Range.Start, res.Range.End, got, tt.wantSpan)
			}
			// A Range that splits a rune would make the slice above contain
			// U+FFFD; assert the boundaries land on rune starts explicitly so
			// the failure names the real cause.
			if !isRuneBoundary(tt.target, res.Range.Start) || !isRuneBoundary(tt.target, res.Range.End) {
				t.Errorf("Range{%d, %d} does not land on rune boundaries", res.Range.Start, res.Range.End)
			}
		})
	}
}

// TestFindFuzzyMatchesReachesSubstringMatch pins the second defect directly.
// The whole-paragraph pre-gate scored this pair at 0.250 and returned before
// bestSubstringMatch — which scores the same input at 0.971 — could run.
func TestFindFuzzyMatchesReachesSubstringMatch(t *testing.T) {
	quote := "until a restart, which is confusing"

	matches := findFuzzyMatches(reflowWrapped, quote, 0.6)
	if len(matches) == 0 {
		t.Fatalf("findFuzzyMatches() found nothing; bestSubstringMatch scores this input at 0.971")
	}

	best := matches[0]
	for _, m := range matches[1:] {
		if m.similarity > best.similarity {
			best = m
		}
	}
	if best.similarity < 0.9 {
		t.Errorf("best fuzzy similarity = %.3f, want >= 0.9", best.similarity)
	}
	const want = "until a restart, which\nis confusing"
	if got := reflowWrapped[best.start:best.end]; got != want {
		t.Errorf("match span = %q, want %q", got, want)
	}
}

// TestFindFuzzyMatchesRejectsAbsentText is the counterweight to the test above:
// dropping the pre-gate must not turn the fuzzy phase into a text generator.
func TestFindFuzzyMatchesRejectsAbsentText(t *testing.T) {
	const doc = "The store writes markdown files to disk and reloads them on change.\n"

	if matches := findFuzzyMatches(doc, "completely unrelated wording about billing invoices", 0.6); len(matches) > 0 {
		t.Errorf("findFuzzyMatches() = %d matches, want 0 (best %.3f %q)",
			len(matches), matches[0].similarity, matches[0].text)
	}
}

// isRuneBoundary reports whether offset starts a rune in s.
func isRuneBoundary(s string, offset int) bool {
	if offset <= 0 || offset >= len(s) {
		return true
	}
	// Continuation bytes are 0b10xxxxxx; anything else starts a rune.
	return s[offset]&0xC0 != 0x80
}
