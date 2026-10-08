package quotefind

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Range is a [Start, End) byte range in the markdown source.
type Range struct {
	Start int
	End   int
}

// newParser returns the parser every function in this package uses.
//
// The GFM table, strikethrough and task-list extensions are enabled so the
// block structure matches a GitHub-flavoured renderer (marked with gfm: true,
// for instance). Without the table extension a table is one paragraph, and a
// highlight over two cells would be one range straddling the cell boundary.
// Linkify is not enabled: a bare URL is plain text here and a link in such a
// renderer, which changes no block boundary.
func newParser() parser.Parser {
	return goldmark.New(goldmark.WithExtensions(
		extension.Table,
		extension.Strikethrough,
		extension.TaskList,
	)).Parser()
}

// Document is markdown source parsed once, for computing [Document.Segments]
// for many ranges.
type Document struct {
	source string
	blocks []textBlock
}

// textBlock is a leaf block that holds inline text: its content span, the
// code spans inside it, and its inline containers.
type textBlock struct {
	content Range
	// code holds each code span, backticks included, in source order.
	code []Range
	// inline holds each emphasis, link, image and strikethrough as the range
	// from its opening delimiter to the end of its last text.
	inline []Range
}

// NewDocument parses source for segmenting.
func NewDocument(source string) *Document {
	src := []byte(source)
	root := newParser().Parse(text.NewReader(src))

	d := &Document{source: source}
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindParagraph, ast.KindHeading, ast.KindTextBlock, extast.KindTableCell:
			if b, ok := newTextBlock(n, src); ok {
				d.blocks = append(d.blocks, b)
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return d
}

// newTextBlock builds the content span of a leaf text block from its lines.
func newTextBlock(n ast.Node, src []byte) (textBlock, bool) {
	lines := n.Lines()
	if lines.Len() == 0 {
		return textBlock{}, false
	}
	content := Range{Start: lines.At(0).Start, End: lines.At(lines.Len() - 1).Stop}

	// A task item's "[ ] " is part of its first line but is not text: marked
	// recognises it only at the very start of the item, so a mark in front of
	// it turns the checkbox into literal brackets. It is skipped in the source
	// rather than by jumping to the first text node, which would land inside
	// the "**" of an item that starts with emphasis.
	if first := n.FirstChild(); first != nil && first.Kind() == extast.KindTaskCheckBox {
		content.Start = skipCheckBox(src, content.Start)
	}

	content = trimRange(src, content)
	if content.Start >= content.End {
		return textBlock{}, false
	}

	b := textBlock{content: content}
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if c.Kind() == ast.KindCodeSpan {
			if r, ok := codeSpanRange(c, src); ok {
				b.code = append(b.code, r)
			}
			return ast.WalkSkipChildren, nil
		}
		if _, ok := delimiter(c); ok {
			open, okOpen := openOf(c, src)
			last := lastText(c)
			if okOpen && last != nil {
				b.inline = append(b.inline, Range{Start: open, End: last.Segment.Stop})
			}
		}
		return ast.WalkContinue, nil
	})
	return b, true
}

// skipCheckBox returns the offset after a task item's "[ ]" or "[x]" and the
// blanks that follow it, or at unchanged when the source has no checkbox there.
func skipCheckBox(src []byte, at int) int {
	if at+3 > len(src) || src[at] != '[' || src[at+2] != ']' {
		return at
	}
	at += 3
	for at < len(src) && (src[at] == ' ' || src[at] == '\t') {
		at++
	}
	return at
}

// delimiter returns the characters an inline container's opening delimiter
// is made of, and whether n is such a container.
func delimiter(n ast.Node) (string, bool) {
	switch n.Kind() {
	case ast.KindEmphasis:
		return "*_", true
	case ast.KindLink:
		return "[", true
	case ast.KindImage:
		return "![", true
	case extast.KindStrikethrough:
		return "~", true
	}
	return "", false
}

// openOf returns where n begins in the source, delimiters included. goldmark
// records positions only on text, so a container's start is found by taking
// its first child's start and stepping back over its opening delimiter, which
// is checked to consist of the expected characters. ok is false when n's start
// cannot be determined that way.
func openOf(n ast.Node, src []byte) (int, bool) {
	switch n.Kind() {
	case ast.KindText:
		return n.(*ast.Text).Segment.Start, true
	case ast.KindCodeSpan:
		r, ok := codeSpanRange(n, src)
		return r.Start, ok
	}
	chars, ok := delimiter(n)
	if !ok || n.FirstChild() == nil {
		return 0, false
	}
	at, ok := openOf(n.FirstChild(), src)
	if !ok {
		return 0, false
	}
	width := 1
	switch e := n.(type) {
	case *ast.Emphasis:
		width = e.Level
	case *ast.Image:
		width = 2
	case *extast.Strikethrough:
		width = 0
		for at-width-1 >= 0 && src[at-width-1] == '~' && width < 2 {
			width++
		}
	}
	if width == 0 || at-width < 0 {
		return 0, false
	}
	for _, c := range src[at-width : at] {
		if !strings.ContainsRune(chars, rune(c)) {
			return 0, false
		}
	}
	return at - width, true
}

// Segments splits source[start:end] into one range per block of text it
// touches, for wrapping each in its own inline element.
//
// A single inline element cannot cross a block boundary: an HTML parser closes
// it at the first block end and drops the stray closing tag, so only the first
// block would show the highlight. One element per block avoids that.
//
// Each segment is the range cut to one block's inline CONTENT, which excludes
// block markup such as "## ", "- " and "[ ] ". A block the range covers whole
// gets its whole content, delimiters included, so emphasis or a link at the
// block's edge stays balanced inside the segment. A start inside emphasis or a
// link that the segment runs past is moved to the opening delimiter; an end
// inside one is kept, since an HTML parser reopens the formatting after the
// mark closes. No segment edge sits next to an escaping backslash.
//
// A code span the range touches is included whole, since inline HTML inside a
// code span renders literally. Code blocks and HTML blocks produce no segment
// at all, for the same reason.
//
// Returns nil when the range covers no text.
func Segments(source string, start, end int) []Range {
	return NewDocument(source).Segments(start, end)
}

// Segments is the package-level [Segments] over a parsed document.
func (d *Document) Segments(start, end int) []Range {
	if start < 0 {
		start = 0
	}
	if end > len(d.source) {
		end = len(d.source)
	}
	if start >= end {
		return nil
	}

	var out []Range
	for _, b := range d.blocks {
		if b.content.End <= start {
			continue
		}
		if b.content.Start >= end {
			break
		}
		r := Range{Start: max(start, b.content.Start), End: min(end, b.content.End)}
		r = b.widenToCode(r)
		r.Start = b.liftStart(r)
		if r = d.clean(r); r.Start < r.End {
			out = append(out, r)
		}
	}
	return out
}

// liftStart moves r's start out of any inline container that r leaves before
// the container ends, to the container's opening delimiter. A segment starting
// inside "**important** text" would otherwise open a mark inside the strong
// element and close it outside, and the HTML parser ends the mark at
// </strong>. A segment that stays inside one container is balanced already.
// Repeated until nothing moves, which lifts out of nested containers too.
func (b textBlock) liftStart(r Range) int {
	start := r.Start
	for moved := true; moved; {
		moved = false
		for _, c := range b.inline {
			if c.Start < start && start < c.End && r.End > c.End {
				start, moved = c.Start, true
			}
		}
	}
	return start
}

// clean trims a segment's whitespace and keeps its edges off a backslash that
// escapes the next character. A segment ending in such a backslash would
// escape the "<" of the closing tag inserted after it, and the reader would
// see "</mark>" as text; one starting right after it would escape the "<" of
// the opening tag. The end is pulled back before the backslash and the start
// is moved back onto it.
func (d *Document) clean(r Range) Range {
	src := []byte(d.source)
	r = trimRange(src, r)
	if r.Start < r.End && escapes(src, r.End-1) {
		r = trimRange(src, Range{Start: r.Start, End: r.End - 1})
	}
	if r.Start < r.End && r.Start > 0 && escapes(src, r.Start-1) {
		r.Start--
	}
	return r
}

// escapes reports whether src[i] is a backslash that escapes the character
// after it: one preceded by an even number of backslashes.
func escapes(src []byte, i int) bool {
	if src[i] != '\\' {
		return false
	}
	n := 0
	for j := i - 1; j >= 0 && src[j] == '\\'; j-- {
		n++
	}
	return n%2 == 0
}

// widenToCode moves an edge of r that falls inside a code span to the span's
// delimiter, so the segment holds every code span it touches whole. Inline
// HTML inside a code span renders literally, so a mark edge there would show
// as text; around the whole span, backticks included, it renders as markup.
func (b textBlock) widenToCode(r Range) Range {
	for _, c := range b.code {
		if c.Start < r.Start && r.Start < c.End {
			r.Start = c.Start
		}
		if c.Start < r.End && r.End < c.End {
			r.End = c.End
		}
	}
	return r
}

// codeSpanRange returns a code span's source range INCLUDING its backticks.
// goldmark records only the content; the delimiters are recovered by counting
// backticks outward, which is exact because a code span's opening and closing
// runs have the same length.
func codeSpanRange(n ast.Node, src []byte) (Range, bool) {
	first, last := firstText(n), lastText(n)
	if first == nil || last == nil {
		return Range{}, false
	}
	s, e := first.Segment.Start, last.Segment.Stop
	// A space just inside the backticks is stripped from the content.
	for s > 0 && src[s-1] == ' ' {
		s--
	}
	for e < len(src) && src[e] == ' ' {
		e++
	}
	for s > 0 && src[s-1] == '`' {
		s--
	}
	for e < len(src) && src[e] == '`' {
		e++
	}
	return Range{Start: s, End: e}, true
}

func firstText(n ast.Node) *ast.Text {
	for ; n != nil; n = n.NextSibling() {
		if t, ok := n.(*ast.Text); ok {
			return t
		}
		if t := firstText(n.FirstChild()); t != nil {
			return t
		}
	}
	return nil
}

func lastText(n ast.Node) *ast.Text {
	var found *ast.Text
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			found = t
		}
		return ast.WalkContinue, nil
	})
	return found
}

// trimRange shrinks r past whitespace at both ends.
func trimRange(src []byte, r Range) Range {
	for r.Start < r.End && strings.ContainsRune(" \t\r\n", rune(src[r.Start])) {
		r.Start++
	}
	for r.End > r.Start && strings.ContainsRune(" \t\r\n", rune(src[r.End-1])) {
		r.End--
	}
	return r
}
