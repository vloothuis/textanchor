package textanchor

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCollapseWhitespace checks the collapsed form and the invariant the
// position map must satisfy: one entry per byte of the collapsed text, plus a
// sentinel holding the original length.
func TestCollapseWhitespace(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "all whitespace", in: "   \n\t ", want: ""},
		{name: "single char", in: "a", want: "a"},
		{name: "leading and trailing stripped", in: "  hello  ", want: "hello"},
		{name: "interior run collapsed", in: "a  \n\t b", want: "a b"},
		{name: "newline becomes space", in: "wrapped\nline", want: "wrapped line"},
		{name: "blank line becomes one space", in: "para\n\nnext", want: "para next"},
		{name: "multi-byte preserved", in: " café  déjà ", want: "café déjà"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := collapseWhitespace(tt.in)
			if c.text != tt.want {
				t.Errorf("text = %q, want %q", c.text, tt.want)
			}
			if len(c.posMap) != len(c.text)+1 {
				t.Errorf("len(posMap) = %d, want %d (one per byte plus sentinel)",
					len(c.posMap), len(c.text)+1)
			}
			if got := c.posMap[len(c.posMap)-1]; got != len(tt.in) {
				t.Errorf("sentinel = %d, want %d", got, len(tt.in))
			}
			// Every mapped byte must be the byte it claims to be.
			for i := 0; i < len(c.text); i++ {
				src := c.posMap[i]
				if src < 0 || src >= len(tt.in) {
					t.Fatalf("posMap[%d] = %d, out of range for a %d-byte input", i, src, len(tt.in))
				}
				if c.text[i] != ' ' && c.text[i] != tt.in[src] {
					t.Errorf("posMap[%d] = %d points at %q, but collapsed byte is %q",
						i, src, tt.in[src], c.text[i])
				}
			}
		})
	}
}

// TestSourceRangeDegenerate pins the guard clauses. sourceRange is a mapping
// helper, so an out-of-range or empty span must come back as the zero Range
// rather than panicking — both offsets are used as slice indices, and clamping
// one without the other previously indexed past the end (or, on an
// all-whitespace input whose collapsed form is empty, at -1).
func TestSourceRangeDegenerate(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		start, end int
	}{
		{name: "start past end of text", in: "hello world", start: 100, end: 200},
		{name: "all whitespace input", in: "   ", start: 0, end: 1},
		{name: "empty input", in: "", start: 0, end: 5},
		{name: "negative offsets", in: "hello", start: -5, end: -1},
		{name: "inverted range", in: "hello", start: 4, end: 2},
		{name: "end far past the text", in: "hello", start: 0, end: 1 << 30},
		{name: "zero-length span", in: "hello", start: 2, end: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := collapseWhitespace(tt.in)
			r := c.sourceRange(tt.start, tt.end)

			if r.Start < 0 || r.End > len(tt.in) || r.Start > r.End {
				t.Fatalf("sourceRange(%d, %d) = %+v, not a valid span of a %d-byte string",
					tt.start, tt.end, r, len(tt.in))
			}
			// Must be safe to slice with.
			_ = tt.in[r.Start:r.End]
		})
	}
}

// TestSourceRangeRuneBoundaries checks that a span chosen by byte arithmetic —
// which is what the fuzzy phase's sliding window produces — never comes back
// splitting a rune.
func TestSourceRangeRuneBoundaries(t *testing.T) {
	inputs := []string{
		"café déjà vu",
		"この文書は日本語です",
		"deploy 🎉 finished",
		strings.Repeat("αβγ ", 20),
	}

	for _, in := range inputs {
		c := collapseWhitespace(in)
		for start := 0; start <= len(c.text); start++ {
			for end := start; end <= len(c.text); end++ {
				r := c.sourceRange(start, end)
				if r.Start == r.End {
					continue
				}
				got := in[r.Start:r.End]
				if !utf8.ValidString(got) {
					t.Fatalf("sourceRange(%d, %d) = %+v yields invalid UTF-8 %q from %q",
						start, end, r, got, in)
				}
			}
		}
	}
}
