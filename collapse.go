package textanchor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// collapsed is a document with every run of whitespace reduced to a single
// space, paired with a map from each byte of that form back to the byte offset
// it came from in the original.
//
// Matching runs against text; ranges are reported in original coordinates via
// [collapsed.sourceRange]. This is what makes an anchor survive a formatter
// that rewraps the document: a line break inside a quote becomes the same
// single space on both sides of the comparison, while the Range handed back to
// the caller still selects the original bytes, newline and all.
//
// The equivalent mapping for RENDERED markdown lives in the quotefind
// subpackage ([quotefind.RenderedTextWithMapping]). It is not reused here: that
// one walks a goldmark AST to strip markdown syntax, which is a much larger job
// than collapsing whitespace and would put a parser dependency on the resolve
// path. The two normalise for different purposes and stay separate.
type collapsed struct {
	// text is the whitespace-collapsed form.
	text string

	// original is the string text was built from. It is retained so a range
	// can be walked to a rune boundary in the original coordinates.
	original string

	// posMap has one entry per byte of text, holding that byte's offset in the
	// original string, plus a final entry holding the original length so an
	// exclusive end offset can be mapped without a special case.
	posMap []int
}

// collapseWhitespace builds the collapsed form of s.
//
// Every maximal run of whitespace becomes one space mapped to the run's FIRST
// byte, and leading/trailing whitespace is dropped entirely. Bytes are copied
// one at a time rather than by rune so that posMap stays byte-indexed, matching
// the byte offsets used throughout the package; a multi-byte rune contributes
// one entry per byte, all pointing at successive original offsets.
func collapseWhitespace(s string) collapsed {
	var b strings.Builder
	b.Grow(len(s))
	posMap := make([]int, 0, len(s)+1)

	pendingSpace := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) {
			// Defer the separator: emitting it only once a non-space follows
			// drops trailing whitespace without a second pass.
			if b.Len() > 0 {
				pendingSpace = true
			}
			i += size
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			posMap = append(posMap, i)
			pendingSpace = false
		}
		for j := 0; j < size; j++ {
			b.WriteByte(s[i+j])
			posMap = append(posMap, i+j)
		}
		i += size
	}

	// The sentinel lets an exclusive end offset one past the last byte map to
	// the end of the original rather than clamping onto the last byte.
	posMap = append(posMap, len(s))

	return collapsed{text: b.String(), original: s, posMap: posMap}
}

// sourceRange converts a [start, end) range over c.text into the equivalent
// range over the original string.
//
// The end offset is derived from the LAST included byte rather than from
// posMap[end]: the byte after a match may be the space standing in for a run of
// whitespace, whose map entry points at the start of that run, which would cut
// the span short. Taking the last included byte and walking to the end of its
// rune keeps the returned range on rune boundaries.
// An out-of-range or empty span returns the zero Range rather than panicking:
// this is a mapping helper, and a caller that asks about a span that is not
// there wants "nothing" back, not a crash.
func (c collapsed) sourceRange(start, end int) Range {
	// Clamp both ends into c.text before either is used as an index. Clamping
	// start only against negatives would still admit an offset past the end,
	// which indexes posMap out of range; clamping end without re-checking the
	// ordering would then let end-1 reach -1 on an empty collapsed form (which
	// every all-whitespace input produces).
	if start < 0 {
		start = 0
	}
	if start > len(c.text) {
		start = len(c.text)
	}
	if end > len(c.text) {
		end = len(c.text)
	}
	if start >= end || end <= 0 {
		return Range{}
	}

	srcStart := c.posMap[start]
	lastByte := c.posMap[end-1]

	// Retreat to the start of the rune the offset falls in. A window boundary
	// chosen by the fuzzy phase is a byte offset into the collapsed text, which
	// carries no obligation to sit on a rune boundary — text without spaces to
	// break on (CJK, say) will land mid-rune routinely.
	for srcStart > 0 && isContinuationByte(c.original[srcStart]) {
		srcStart--
	}

	// Advance past the final rune's continuation bytes so the exclusive end
	// never lands inside it.
	srcEnd := lastByte + 1
	for srcEnd < len(c.original) && isContinuationByte(c.original[srcEnd]) {
		srcEnd++
	}

	return Range{Start: srcStart, End: srcEnd}
}

// isContinuationByte reports whether b is a UTF-8 continuation byte (0b10xxxxxx).
func isContinuationByte(b byte) bool {
	return b&0xC0 == 0x80
}
