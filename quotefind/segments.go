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
// The GFM block and inline extensions are enabled so the AST matches what a
// GitHub-flavoured renderer (marked with gfm: true, for instance) shows the
// user. Without the table extension a table is one paragraph, and a highlight
// over two cells would be one range straddling the cell boundary.
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

// textBlock is a leaf block that holds inline text: its content span and the
// code spans inside it.
type textBlock struct {
	content Range
	code    []Range
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
	// it turns the checkbox into literal brackets.
	if first := n.FirstChild(); first != nil && first.Kind() == extast.KindTaskCheckBox {
		if t := firstText(first.NextSibling()); t != nil {
			content.Start = t.Segment.Start
		}
	}

	content = trimRange(src, content)
	if content.Start >= content.End {
		return textBlock{}, false
	}

	b := textBlock{content: content}
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && c.Kind() == ast.KindCodeSpan {
			if r, ok := codeSpanRange(c, src); ok {
				b.code = append(b.code, r)
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return b, true
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
// block's edge stays balanced inside the segment. Only the range's own start
// and end are kept as given; if the caller placed them inside inline markup,
// that is the caller's selection.
//
// Code is cut out: inline HTML inside a code span renders literally. Code
// blocks and HTML blocks produce no segment at all, for the same reason.
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
		for _, piece := range subtract(r, b.code) {
			if piece = trimRange([]byte(d.source), piece); piece.Start < piece.End {
				out = append(out, piece)
			}
		}
	}
	return out
}

// subtract returns r minus every range in cut, in order. cut is sorted and
// non-overlapping, as code spans in one block are.
func subtract(r Range, cut []Range) []Range {
	var out []Range
	at := r.Start
	for _, c := range cut {
		if c.End <= at || c.Start >= r.End {
			continue
		}
		if c.Start > at {
			out = append(out, Range{Start: at, End: c.Start})
		}
		at = max(at, c.End)
	}
	if at < r.End {
		out = append(out, Range{Start: at, End: r.End})
	}
	return out
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
